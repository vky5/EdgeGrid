package broker_test

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"
)

func runNatsServer(t *testing.T) (*natsserver.Server, string) {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	addr := l.Addr().(*net.TCPAddr)
	l.Close()

	dir, err := os.MkdirTemp("", "nats-js-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	opts := &natsserver.Options{
		Host:      addr.IP.String(),
		Port:      addr.Port,
		JetStream: true,
		StoreDir:  dir,
	}

	server, err := natsserver.NewServer(opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	go server.Start()

	if !server.ReadyForConnections(5 * time.Second) {
		t.Fatalf("NATS server ready check failed")
	}

	return server, server.ClientURL()
}

func TestBroker_EnsureStreamAndPublish(t *testing.T) {
	_, url := runNatsServer(t)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	b, err := broker.NewBroker(nc, 1)
	if err != nil {
		t.Fatalf("failed to initialize broker: %v", err)
	}

	// 1. Verify stream creation
	if err := b.EnsureStream(); err != nil {
		t.Fatalf("failed to ensure stream first time: %v", err)
	}

	// 2. Verify idempotency
	if err := b.EnsureStream(); err != nil {
		t.Fatalf("failed to ensure stream second time: %v", err)
	}

	// 3. Test protobuf publication
	testMsg := &workerpb.TrainingJobRequest{
		JobId:        "test-job-uuid",
		DatasetType:  "object_store",
		DatasetRef:   "dataset-key-123",
		BaseModelRef: "gpt2",
	}

	if err := b.PublishProto(broker.SubjectTrainPrefix+"gpt2", testMsg); err != nil {
		t.Fatalf("failed to publish proto message: %v", err)
	}

	// 4. Subscribe and verify message content
	sub, err := b.JS.SubscribeSync(broker.SubjectTrainPrefix + "gpt2")
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to fetch message: %v", err)
	}

	var received workerpb.TrainingJobRequest
	if err := proto.Unmarshal(msg.Data, &received); err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if received.JobId != testMsg.JobId || received.DatasetRef != testMsg.DatasetRef {
		t.Errorf("received message does not match: expected %+v, got %+v", testMsg, received)
	}
}
