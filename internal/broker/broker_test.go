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
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})

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

	b, err := broker.NewBroker(nc)
	if err != nil {
		t.Fatalf("failed to initialize broker: %v", err)
	}

	// 1. Verify stream creation
	err = b.EnsureStream()
	if err != nil {
		t.Fatalf("failed to ensure stream first time: %v", err)
	}

	// 2. Verify idempotency (should succeed without error)
	err = b.EnsureStream()
	if err != nil {
		t.Fatalf("failed to ensure stream second time: %v", err)
	}

	// 3. Test protobuf publication
	testMsg := &workerpb.JobRequest{
		JobId:     "test-job-uuid",
		ModelName: "all-minilm",
		InputText: "test payload string",
	}

	err = b.PublishProto(broker.SubjectJobsPrefix+"all-minilm", testMsg)
	if err != nil {
		t.Fatalf("failed to publish proto message: %v", err)
	}

	// Subscribe and verify message content
	sub, err := b.JS.SubscribeSync(broker.SubjectJobsPrefix + "all-minilm")
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to fetch message: %v", err)
	}

	var received workerpb.JobRequest
	err = proto.Unmarshal(msg.Data, &received)
	if err != nil {
		t.Fatalf("failed to unmarshal message: %v", err)
	}

	if received.JobId != testMsg.JobId || received.InputText != testMsg.InputText {
		t.Errorf("received message does not match: expected %+v, got %+v", testMsg, received)
	}
}
