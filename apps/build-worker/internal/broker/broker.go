package broker

import (
	"context"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	workerpb "github.com/edgegrid/edgegrid/apps/shared/proto/worker"
)

type WorkerBroker struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
}

func NewWorkerBroker(natsURL string) (*WorkerBroker, error) {
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, err
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, err
	}

	return &WorkerBroker{
		Conn: nc,
		JS:   js,
	}, nil
}

func (wb *WorkerBroker) Close() {
	if wb.Conn != nil {
		wb.Conn.Close()
	}
}

func (wb *WorkerBroker) RegisterWorker(id string, models []string) error {
	info := &workerpb.WorkerInfo{
		Id:             id,
		SupportedModel: models,
	}
	data, err := proto.Marshal(info)
	if err != nil {
		return err
	}

	_, err = wb.JS.Publish("workers.register", data)
	return err
}

func (wb *WorkerBroker) StartHeartbeat(ctx context.Context, id string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			req := &workerpb.PingRequest{
				Id:     id,
				Status: "free", // V1 default status
			}
			data, err := proto.Marshal(req)
			if err != nil {
				log.Printf("❌ Failed to marshal heartbeat: %v", err)
				continue
			}

			_, err = wb.JS.Publish("workers.heartbeat", data)
			if err != nil {
				log.Printf("❌ Failed to publish heartbeat: %v", err)
			}
		}
	}
}

func (wb *WorkerBroker) StartJobListener(ctx context.Context, workerID string, model string) {
	subject := "jobs.build." + model
	// Create a pull subscription (competing consumer model)
	// We use the subject name as part of the durable consumer name so workers sharing the same model share the work
	durableConsumer := "consumer-" + model
	sub, err := wb.JS.PullSubscribe(subject, durableConsumer, nats.ManualAck())
	if err != nil {
		log.Printf("❌ Failed to subscribe to subject %s: %v", subject, err)
		return
	}

	log.Printf("👂 Listening for jobs on %s (Durable: %s)", subject, durableConsumer)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Pull a batch of 1 message, wait up to 5 seconds
			msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
			if err != nil {
				if err == nats.ErrTimeout {
					continue
				}
				log.Printf("❌ Error fetching message on %s: %v", subject, err)
				time.Sleep(1 * time.Second)
				continue
			}

			if len(msgs) == 0 {
				continue
			}

			msg := msgs[0]
			var req workerpb.JobRequest
			if err := proto.Unmarshal(msg.Data, &req); err != nil {
				log.Printf("❌ Failed to unmarshal JobRequest: %v", err)
				msg.Term() // terminate message so it is not redelivered
				continue
			}

			log.Printf("📥 Received job %s for model %s. Processing...", req.JobId, req.ModelName)

			// Process the job: generate stub embeddings (e.g. 128 float array)
			embeddingResult := generateStubEmbeddings(req.InputText)

			// Create JobResponse
			resp := &workerpb.JobResponse{
				JobId:     req.JobId,
				Success:   true,
				Embedding: embeddingResult,
				WorkerId:  workerID,
			}

			respData, err := proto.Marshal(resp)
			if err != nil {
				log.Printf("❌ Failed to marshal JobResponse: %v", err)
				msg.Nak() // nak so it can be retried
				continue
			}

			// Publish result back
			_, err = wb.JS.Publish("jobs.results", respData)
			if err != nil {
				log.Printf("❌ Failed to publish result: %v", err)
				msg.Nak()
				continue
			}

			// Acknowledge the message
			if err := msg.Ack(); err != nil {
				log.Printf("⚠️ Failed to ack message: %v", err)
			}
			log.Printf("✅ Successfully processed job %s and published embeddings", req.JobId)
		}
	}
}

// generateStubEmbeddings creates a pseudo-random float vector based on input text length
func generateStubEmbeddings(text string) []float32 {
	vector := make([]float32, 128)
	length := float32(len(text))
	for i := 0; i < 128; i++ {
		vector[i] = length * 0.01 + float32(i)*0.005
	}
	return vector
}
