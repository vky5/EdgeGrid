package workerman_test

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/coordinator/workerman"
	workerpb "github.com/edgegrid/edgegrid/internal/proto/worker"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
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

func TestWorkerManager_RegistrationAndHeartbeats(t *testing.T) {
	_, url := runNatsServer(t)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	br, err := broker.NewBroker(nc, 1)
	if err != nil {
		t.Fatalf("failed to initialize broker: %v", err)
	}

	wm, err := workerman.NewWorkerManager(br)
	if err != nil {
		t.Fatalf("failed to initialize workerman: %v", err)
	}

	ctx := context.Background()
	workerInfo := &workerpb.WorkerInfo{
		Id:             "test-worker-1",
		SupportedModel: []string{"gpt2"},
	}

	// 1. Test registration
	err = wm.RegisterWorker(ctx, workerInfo)
	if err != nil {
		t.Fatalf("failed to register worker: %v", err)
	}

	// 2. Verify state in KV
	kv, err := br.JS.KeyValue("workers")
	if err != nil {
		t.Fatalf("failed to get KeyValue: %v", err)
	}

	entry, err := kv.Get("test-worker-1")
	if err != nil {
		t.Fatalf("failed to get entry from KV: %v", err)
	}

	var worker workerman.Worker
	if err := json.Unmarshal(entry.Value(), &worker); err != nil {
		t.Fatalf("failed to unmarshal KV worker: %v", err)
	}

	if worker.Info.Id != "test-worker-1" || worker.State != workerman.WorkerFree {
		t.Errorf("unexpected worker state: %+v", worker)
	}

	// 3. Test Heartbeat State Update
	wm.SetWorkerState("test-worker-1", workerman.WorkerBusy)

	entry, err = kv.Get("test-worker-1")
	if err != nil {
		t.Fatalf("failed to get updated entry: %v", err)
	}

	if err := json.Unmarshal(entry.Value(), &worker); err != nil {
		t.Fatalf("failed to unmarshal KV worker: %v", err)
	}

	if worker.State != workerman.WorkerBusy {
		t.Errorf("expected worker state to be %s, got %s", workerman.WorkerBusy, worker.State)
	}
}

func TestWorkerManager_TTLAutoReaping(t *testing.T) {
	_, url := runNatsServer(t)

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer nc.Close()

	br, err := broker.NewBroker(nc, 1)
	if err != nil {
		t.Fatalf("failed to initialize broker: %v", err)
	}

	// Use a 200ms TTL for testing auto-reaping
	kv, err := br.GetOrCreateKV("workers", 200*time.Millisecond)
	if err != nil {
		t.Fatalf("failed to get or create KV: %v", err)
	}

	wm, err := workerman.NewWorkerManager(br)
	if err != nil {
		t.Fatalf("failed to initialize workerman: %v", err)
	}

	ctx := context.Background()
	workerInfo := &workerpb.WorkerInfo{
		Id:             "temp-worker",
		SupportedModel: []string{"gpt2"},
	}

	// Register worker
	err = wm.RegisterWorker(ctx, workerInfo)
	if err != nil {
		t.Fatalf("failed to register worker: %v", err)
	}

	// Verify key exists
	_, err = kv.Get("temp-worker")
	if err != nil {
		t.Fatalf("expected key to exist immediately: %v", err)
	}

	// Wait past TTL and poll until deleted (up to 2 seconds)
	deleted := false
	for start := time.Now(); time.Since(start) < 2*time.Second; {
		_, err = kv.Get("temp-worker")
		if err == nats.ErrKeyNotFound {
			deleted = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !deleted {
		t.Error("expected key to be reaped by TTL within 2 seconds, but it still exists")
	}
}

func TestWorkerManager_CrossCoordinatorVisibility(t *testing.T) {
	_, url := runNatsServer(t)

	nc1, _ := nats.Connect(url)
	defer nc1.Close()
	nc2, _ := nats.Connect(url)
	defer nc2.Close()

	br1, _ := broker.NewBroker(nc1, 1)
	br2, _ := broker.NewBroker(nc2, 1)

	wm1, _ := workerman.NewWorkerManager(br1)
	wm2, _ := workerman.NewWorkerManager(br2)

	ctx := context.Background()
	workerInfo := &workerpb.WorkerInfo{
		Id:             "shared-worker",
		SupportedModel: []string{"gpt2"},
	}

	// Register on Coordinator 1
	_ = wm1.RegisterWorker(ctx, workerInfo)

	// Verify state updates from Coordinator 2 (heartbeat)
	wm2.SetWorkerState("shared-worker", workerman.WorkerBusy)

	// Coordinator 1 reads the updated state
	kv1, _ := br1.JS.KeyValue("workers")
	entry, _ := kv1.Get("shared-worker")
	var worker workerman.Worker
	_ = json.Unmarshal(entry.Value(), &worker)

	if worker.State != workerman.WorkerBusy {
		t.Errorf("expected Coordinator 1 to read state updated by Coordinator 2 as %s, got %s", workerman.WorkerBusy, worker.State)
	}
}
