package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/edgegrid/edgegrid/internal/agent"
	"github.com/edgegrid/edgegrid/internal/broker"
	"github.com/edgegrid/edgegrid/internal/config"
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

func findFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port for HTTP: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestAgent_EndToEndJobExecution(t *testing.T) {
	_, natsURL := runNatsServer(t)
	httpAddr := findFreePort(t)

	// Create configuration for the agent with both Coordinator and Worker enabled
	cfg := &config.Config{
		NatsURL: natsURL,
		Server: config.ServerConfig{
			Enabled: true,
			Port:    httpAddr,
		},
		Client: config.ClientConfig{
			Enabled:          true,
			WorkerID:         "test-e2e-worker",
			SupportedModels:  []string{"all-minilm"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. Initialize Agent
	a, err := agent.NewAgent(cfg)
	if err != nil {
		t.Fatalf("failed to create agent: %v", err)
	}
	defer a.Close()

	// 2. Start Agent in background
	go func() {
		if err := a.Start(ctx); err != nil {
			t.Logf("agent start returned error (expected on shutdown): %v", err)
		}
	}()

	// Give a bit of time for coordinator/worker to register and start HTTP listener
	time.Sleep(300 * time.Millisecond)

	// 3. Connect a separate NATS client to subscribe to results and verify
	nc, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatalf("failed to connect separate client: %v", err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync(broker.SubjectResults)
	if err != nil {
		t.Fatalf("failed to subscribe to results: %v", err)
	}

	// 4. Submit a job to the HTTP Server
	jobReq := map[string]string{
		"model_name": "all-minilm",
		"input_text": "hello distributed world",
	}
	reqBytes, _ := json.Marshal(jobReq)

	resp, err := http.Post("http://"+httpAddr+"/jobs", "application/json", bytes.NewBuffer(reqBytes))
	if err != nil {
		t.Fatalf("HTTP post failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status 202, got %d. Body: %s", resp.StatusCode, string(body))
	}

	var submitResp struct {
		JobID  string `json:"job_id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResp); err != nil {
		t.Fatalf("failed to decode submit response: %v", err)
	}

	if submitResp.JobID == "" || submitResp.Status != "queued" {
		t.Errorf("unexpected submit response: %+v", submitResp)
	}

	// 5. Wait for the result to be published on the results subject
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("failed to receive result: %v", err)
	}

	var jobResult workerpb.JobResponse
	if err := proto.Unmarshal(msg.Data, &jobResult); err != nil {
		t.Fatalf("failed to unmarshal job response: %v", err)
	}

	if jobResult.JobId != submitResp.JobID {
		t.Errorf("expected job ID %s, got %s", submitResp.JobID, jobResult.JobId)
	}
	if !jobResult.Success {
		t.Errorf("job failed: %s", jobResult.Error)
	}
	if len(jobResult.Embedding) != 128 {
		t.Errorf("expected embedding length of 128, got %d", len(jobResult.Embedding))
	}
	if jobResult.WorkerId != "test-e2e-worker" {
		t.Errorf("expected worker ID test-e2e-worker, got %s", jobResult.WorkerId)
	}
}
