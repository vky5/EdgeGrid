package broker

import (
	"context"
	"log"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	workerpb "github.com/edgegrid/edgegrid/apps/shared/proto/worker"
)

// JobExecutor defines the contract for executing embedding tasks
type JobExecutor interface {
	Execute(ctx context.Context, modelName string, inputText string) ([]float32, error)
}

type WorkerBroker struct {
	Conn     *nats.Conn
	JS       nats.JetStreamContext
	Executor JobExecutor
}

func NewWorkerBroker(natsURL string, exec JobExecutor) (*WorkerBroker, error) {
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
		Conn:     nc,
		JS:       js,
		Executor: exec,
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
