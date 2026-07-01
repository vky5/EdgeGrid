package worker

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/jobstate"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

// WorkerStats is published at each heartbeat over NATS Core (not JetStream).
type WorkerStats struct {
	RAMUsedGB   float32 `json:"ram_used_gb"`
	DiskUsedGB  float32 `json:"disk_used_gb"`
	DiskTotalGB float32 `json:"disk_total_gb"`
}

const approvalTimeout = 60 * time.Second

type rejectionMsg struct {
	JobID    string `json:"job_id"`
	WorkerID string `json:"worker_id"`
}

// RegisterWorker publishes the worker's hardware capabilities detected at startup.
func (a *Worker) RegisterWorker() error {
	info := &workerpb.WorkerInfo{
		Id:         a.id,
		HasGpu:     a.hw.HasGPU,
		GpuName:    a.hw.GPUName,
		GpuVramGb:  a.hw.GPUVramGB,
		RamGb:      a.hw.RAMGB,
		DiskFreeGb: a.hw.DiskFreeGB,
		Sandbox:    "none",
	}
	return a.broker.PublishProto(broker.SubjectRegister, info)
}

// StartHeartbeat sends periodic worker status updates to the coordinator.
func (a *Worker) StartHeartbeat(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status := WorkerFree
			if a.busy.Load() {
				status = WorkerBusy
			}
			req := &workerpb.PingRequest{
				Id:     a.id,
				Status: status,
			}
			if err := a.broker.PublishProto(broker.SubjectHeartbeat, req); err != nil {
				log.Printf("failed to publish heartbeat: %v", err)
			}

			// Publish live resource usage on a separate NATS Core subject so
			// the coordinator can update the dashboard without proto changes.
			stats := WorkerStats{
				RAMUsedGB:   liveRAMUsedGB(),
				DiskUsedGB:  liveDiskUsedGB(),
				DiskTotalGB: liveDiskTotalGB(),
			}
			if data, err := json.Marshal(stats); err == nil {
				subject := fmt.Sprintf(broker.SubjectWorkerStatsFmt, a.id)
				_ = a.broker.Conn.Publish(subject, data)
			}
		}
	}
}

// StartCancelListener subscribes to jobs.cancel and cancels any running job
// whose ID matches. Every worker receives every cancel message; only the worker
// that holds the job in its cancels map acts on it.
func (a *Worker) StartCancelListener(ctx context.Context) {
	sub, err := a.broker.JS.Subscribe(broker.SubjectCancel, func(msg *nats.Msg) {
		jobID := string(msg.Data)
		a.mu.Lock()
		if cancel, ok := a.cancels[jobID]; ok {
			cancel()
			log.Printf("cancelling job %s on coordinator request", jobID)
		}
		a.mu.Unlock()
		msg.Ack()
	}, nats.DeliverNew(), nats.ManualAck())
	if err != nil {
		log.Printf("failed to subscribe to cancel events: %v", err)
		return
	}
	defer sub.Unsubscribe()
	<-ctx.Done()
}

// StartJobListener pulls training jobs addressed to this worker from NATS JetStream.
func (a *Worker) StartJobListener(ctx context.Context) {
	subject := broker.SubjectTrainPrefix + a.id
	durableConsumer := "training-consumer-" + a.id

	sub, err := a.broker.JS.PullSubscribe(subject, durableConsumer, nats.ManualAck())
	if err != nil {
		log.Printf("failed to subscribe to %s: %v", subject, err)
		return
	}

	log.Printf("listening for training jobs on %s", subject)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
			if err != nil {
				if err == nats.ErrTimeout {
					continue
				}
				log.Printf("error fetching from %s: %v", subject, err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			a.handleJob(ctx, msgs[0])
		}
	}
}

// handleJob runs the full training pipeline for one job.
func (a *Worker) handleJob(ctx context.Context, msg *nats.Msg) {
	msgAcked := false

	defer func() {
		if r := recover(); r != nil {
			log.Printf("recovered panic in job handler: %v", r)
			if !msgAcked {
				msg.Nak()
			}
		}
	}()

	if !a.busy.CompareAndSwap(false, true) {
		msg.NakWithDelay(10 * time.Second)
		return
	}
	defer a.busy.Store(false)

	var req workerpb.TrainingJobRequest
	if err := proto.Unmarshal(msg.Data, &req); err != nil {
		log.Printf("failed to unmarshal training job request: %v", err)
		msg.Term()
		return
	}

	log.Printf("received training job %s (base_model: %s, dataset: %s %s)",
		req.JobId, req.BaseModelRef, req.DatasetType, req.DatasetRef)

	// Approval gate: ACK immediately (taking ownership of the message), then
	// wait for a human decision before proceeding. Any path that doesn't approve
	// sends a rejection notice to the coordinator.
	if a.requireApproval {
		msg.Ack()
		msgAcked = true

		if !a.awaitApproval(ctx, &req) {
			a.sendRejection(req.JobId)
			return
		}
	}

	// Per-job context so this job can be cancelled independently of the worker.
	jobCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.cancels[req.JobId] = cancel
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		delete(a.cancels, req.JobId)
		a.mu.Unlock()
	}()

	kv, kvErr := a.broker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if kvErr == nil {
		_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateRunning, a.id, "", "")
	}

	checkpointKey, err := a.runTrainingPipeline(jobCtx, &req)

	resp := &workerpb.JobResponse{
		JobId:    req.JobId,
		WorkerId: a.id,
	}
	if err != nil {
		log.Printf("job %s failed: %v", req.JobId, err)
		resp.Success = false
		resp.Error = err.Error()
		if kvErr == nil {
			_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateFailed, a.id, err.Error(), "")
		}
	} else {
		log.Printf("job %s completed, checkpoint: %s", req.JobId, checkpointKey)
		resp.Success = true
		resp.CheckpointKey = checkpointKey
		if kvErr == nil {
			_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StateCompleted, a.id, "", checkpointKey)
		}
	}

	if pubErr := a.broker.PublishProto(broker.SubjectResults, resp); pubErr != nil {
		log.Printf("failed to publish result for job %s: %v", req.JobId, pubErr)
		if !msgAcked {
			msg.Nak()
		}
		return
	}

	if !msgAcked {
		msg.Ack()
	}
}

// awaitApproval sets the job to PENDING_REVIEW and waits up to approvalTimeout
// for the coordinator to relay a human decision ("approve", "reject", or "cancel").
// Returns true only when the decision is exactly "approve".
func (a *Worker) awaitApproval(ctx context.Context, req *workerpb.TrainingJobRequest) bool {
	kv, err := a.broker.GetOrCreateKV("jobs_state", 24*time.Hour)
	if err == nil {
		_ = jobstate.UpdateJobStatus(kv, req.JobId, jobstate.StatePendingReview, a.id, "", "")
	}

	log.Printf("job %s awaiting approval (timeout: %v)", req.JobId, approvalTimeout)

	subject := fmt.Sprintf(broker.SubjectWorkerDecisionFmt, a.id, req.JobId)
	decisionCh := make(chan string, 1)

	sub, err := a.broker.Conn.Subscribe(subject, func(msg *nats.Msg) {
		select {
		case decisionCh <- string(msg.Data):
		default:
		}
	})
	if err != nil {
		log.Printf("job %s: failed to subscribe for approval signal: %v", req.JobId, err)
		return false
	}
	defer sub.Unsubscribe()

	select {
	case decision := <-decisionCh:
		log.Printf("job %s: decision received: %q", req.JobId, decision)
		return decision == "approve"
	case <-time.After(approvalTimeout):
		log.Printf("job %s: approval timed out after %v", req.JobId, approvalTimeout)
		return false
	case <-ctx.Done():
		log.Printf("job %s: context cancelled while awaiting approval", req.JobId)
		return false
	}
}

// sendRejection notifies the coordinator that this worker declined the job.
// The coordinator will requeue it and try the next available worker.
func (a *Worker) sendRejection(jobID string) {
	data, err := json.Marshal(rejectionMsg{JobID: jobID, WorkerID: a.id})
	if err != nil {
		log.Printf("job %s: failed to marshal rejection: %v", jobID, err)
		return
	}
	if err := a.broker.Conn.Publish(broker.SubjectWorkerReject, data); err != nil {
		log.Printf("job %s: failed to publish rejection: %v", jobID, err)
		return
	}
	log.Printf("job %s: rejection sent to coordinator", jobID)
}

// runTrainingPipeline executes all steps: disk check, dataset pull, train, checkpoint push.
func (a *Worker) runTrainingPipeline(ctx context.Context, req *workerpb.TrainingJobRequest) (string, error) {
	// 1. Disk pre-check
	if req.MinDiskGb > 0 {
		free := detectDiskFreeGB()
		if free < req.MinDiskGb {
			return "", fmt.Errorf("insufficient disk: need %.1fGB, have %.1fGB", req.MinDiskGb, free)
		}
	}

	// 2. Create isolated job directory
	jobDir := filepath.Join(os.TempDir(), "edgegrid-jobs", req.JobId)
	inputDir := filepath.Join(jobDir, "input")
	outputDir := filepath.Join(jobDir, "output")
	defer os.RemoveAll(jobDir)

	for _, dir := range []string{inputDir, outputDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("failed to create job dir: %w", err)
		}
	}

	// 3. Pull dataset from Object Store (HF datasets are handled by the training script)
	if req.DatasetType == "object_store" {
		if err := a.pullDataset(req.JobId, inputDir); err != nil {
			return "", fmt.Errorf("dataset pull failed: %w", err)
		}
	}

	// 4. Periodically snapshot output/ while training runs so progress is not
	// lost if the worker dies before the job completes.
	checkpointStop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-checkpointStop:
				return
			case <-ticker.C:
				entries, _ := os.ReadDir(outputDir)
				if len(entries) == 0 {
					continue // nothing written yet
				}
				if err := a.pushCheckpoint(req.JobId, outputDir); err != nil {
					log.Printf("mid-training checkpoint failed for job %s: %v", req.JobId, err)
				} else {
					log.Printf("mid-training checkpoint saved for job %s", req.JobId)
				}
			}
		}
	}()

	// 5. Run training
	if err := a.executor.Execute(ctx, req, jobDir); err != nil {
		close(checkpointStop)
		return "", err
	}
	close(checkpointStop)

	// 6. Push final checkpoint to Object Store
	if err := a.pushCheckpoint(req.JobId, outputDir); err != nil {
		return "", fmt.Errorf("checkpoint push failed: %w", err)
	}

	return req.JobId, nil
}

// pullDataset downloads the dataset from the Object Store into inputDir/dataset.
func (a *Worker) pullDataset(jobID, inputDir string) error {
	result, err := a.broker.PullDataset(jobID)
	if err != nil {
		return err
	}
	defer result.Close()

	dest := filepath.Join(inputDir, "dataset")
	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("failed to create dataset file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, result)
	return err
}

// pushCheckpoint tars the output directory and uploads it to the Object Store.
func (a *Worker) pushCheckpoint(jobID, outputDir string) error {
	pr, pw := io.Pipe()

	go func() {
		gw := gzip.NewWriter(pw)
		tw := tar.NewWriter(gw)

		err := filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(outputDir, path)
			hdr := &tar.Header{
				Name:    rel,
				Size:    info.Size(),
				Mode:    int64(info.Mode()),
				ModTime: info.ModTime(),
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})

		tw.Close()
		gw.Close()
		pw.CloseWithError(err)
	}()

	return a.broker.PushCheckpoint(jobID, pr)
}
