package executor

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/edgegrid/edgegrid/internal/proto/worker"
	"github.com/edgegrid/edgegrid/internal/utils"
	"google.golang.org/protobuf/proto"
)

// HuggingFaceExecutor runs real Hugging Face models using a Python sidecar subprocess over UDS.
type HuggingFaceExecutor struct {
	mu           sync.Mutex
	processes    []*exec.Cmd
	modelSockets map[string]string
}

// NewHuggingFaceExecutor initializes a HuggingFaceExecutor.
func NewHuggingFaceExecutor() *HuggingFaceExecutor {
	return &HuggingFaceExecutor{
		modelSockets: make(map[string]string),
	}
}

// Start spins up Python runner UDS subprocesses for the specified models.
func (e *HuggingFaceExecutor) Start(ctx context.Context, models []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, model := range models {
		if model == "coordinator-local" || model == "" {
			continue
		}

		if _, exists := e.modelSockets[model]; exists {
			continue
		}

		runnerPath := utils.FindRunnerPath()
		pythonBin, err := utils.EnsureVenv(runnerPath)
		if err != nil {
			return fmt.Errorf("failed to prepare Python virtual environment: %w", err)
		}

		socketPath := filepath.Join(filepath.Dir(runnerPath), fmt.Sprintf("runner-%s.sock", model))
		
		// Clean up any stale socket file before starting
		_ = os.Remove(socketPath)

		log.Printf("Starting Python UDS runner for model '%s' on socket '%s' using venv python...", model, socketPath)

		// Start the runner process in the background. We use context.Background() so it doesn't
		// get killed when the startup context is cancelled.
		cmd := exec.CommandContext(context.Background(), pythonBin, runnerPath, model, socketPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start Python runner for model %s: %w", model, err)
		}

		e.processes = append(e.processes, cmd)

		// Wait for the Unix socket to become dialable (up to 3 minutes for downloading HF models)
		ready := false
		start := time.Now()
		for time.Since(start) < 3*time.Minute {
			// Check if process has exited early
			if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
				break
			}

			conn, err := net.DialTimeout("unix", socketPath, 200*time.Millisecond)
			if err == nil {
				conn.Close()
				ready = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}

		if ready {
			log.Printf("Python UDS runner for model '%s' is ready", model)
			e.modelSockets[model] = socketPath
		} else {
			_ = cmd.Process.Kill()
			_ = os.Remove(socketPath)
			return fmt.Errorf("python runner for model '%s' failed to become ready in time", model)
		}
	}
	return nil
}

// Close kills all running Python subprocesses and removes socket files.
func (e *HuggingFaceExecutor) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.processes) > 0 {
		log.Printf("Stopping %d Python runner processes...", len(e.processes))
		for _, cmd := range e.processes {
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	e.processes = nil

	for _, socketPath := range e.modelSockets {
		_ = os.Remove(socketPath)
	}
	e.modelSockets = make(map[string]string)
	return nil
}

// Execute performs binary Protobuf IPC over UDS to the Python sidecar.
func (e *HuggingFaceExecutor) Execute(ctx context.Context, modelName string, inputText string) ([]float32, error) {
	e.mu.Lock()
	socketPath, ok := e.modelSockets[modelName]
	e.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("no runner active for model %s", modelName)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("failed to dial Python UDS socket: %w", err)
	}
	defer conn.Close()

	// Prepare JobRequest protobuf
	req := &workerpb.JobRequest{
		JobId:     "local-uds-query",
		ModelName: modelName,
		InputText: inputText,
	}

	reqBytes, err := proto.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Write 4-byte big-endian request length
	length := uint32(len(reqBytes))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		return nil, fmt.Errorf("failed to write request length: %w", err)
	}

	// Write request payload bytes
	if _, err := conn.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("failed to write request payload: %w", err)
	}

	// Read 4-byte big-endian response length
	var respLength uint32
	if err := binary.Read(conn, binary.BigEndian, &respLength); err != nil {
		return nil, fmt.Errorf("failed to read response length: %w", err)
	}

	// Read response payload bytes
	respBytes := make([]byte, respLength)
	if _, err := io.ReadFull(conn, respBytes); err != nil {
		return nil, fmt.Errorf("failed to read complete response payload: %w", err)
	}

	// Unmarshal JobResponse protobuf
	resp := &workerpb.JobResponse{}
	if err := proto.Unmarshal(respBytes, resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if !resp.Success {
		return nil, fmt.Errorf("python runner failed: %s", resp.Error)
	}

	return resp.Embedding, nil
}
