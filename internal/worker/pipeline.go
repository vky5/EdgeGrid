package worker

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/edgegrid/edgegrid/internal/worker/hardware"
	"github.com/nats-io/nats.go"
)

// runTrainingPipeline executes all steps: disk check, dataset pull, train, checkpoint push.
func (a *Worker) runTrainingPipeline(ctx context.Context, req *workerpb.TrainingJobRequest) (string, error) {
	// 1. Disk pre-check
	if req.MinDiskGb > 0 {
		free := hardware.DiskFreeGB()
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

	// 2b. Resume from a prior checkpoint if this job was requeued after a worker crash.
	if err := a.pullCheckpoint(req.JobId, outputDir); err != nil {
		return "", fmt.Errorf("checkpoint pull failed: %w", err)
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

// pullCheckpoint extracts a prior checkpoint into outputDir if one exists.
// Returns nil, not an error, when there isn't one yet (a job's first attempt).
func (a *Worker) pullCheckpoint(jobID, outputDir string) error {
	result, err := a.broker.PullCheckpoint(jobID)
	if err == nats.ErrObjectNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	defer result.Close()

	gr, err := gzip.NewReader(result)
	if err != nil {
		return fmt.Errorf("failed to open checkpoint gzip stream: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read checkpoint tar entry: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		dest := filepath.Join(outputDir, hdr.Name)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return fmt.Errorf("failed to create checkpoint dir: %w", err)
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
		if err != nil {
			return fmt.Errorf("failed to create checkpoint file %s: %w", hdr.Name, err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("failed to write checkpoint file %s: %w", hdr.Name, err)
		}
		f.Close()
	}

	log.Printf("resumed job %s from prior checkpoint", jobID)
	return nil
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
