package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTP is a Client backed by the coordinator's backendMux HTTP API. Every
// method is real now — the coordinator's own endpoints back all of them.
type HTTP struct {
	*Stub
	baseURL    string
	adminToken string
	httpClient *http.Client
}

// NewHTTP builds a real Client talking to a coordinator at baseURL (e.g.
// "http://100.x.x.x:8080"), authenticating with the same bearer token the
// coordinator's requireGateway middleware checks everywhere else.
func NewHTTP(baseURL, adminToken string) *HTTP {
	return &HTTP{
		Stub:       &Stub{},
		baseURL:    strings.TrimRight(baseURL, "/"),
		adminToken: adminToken,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// joinRequestWire mirrors joinmgr.JoinRequest's JSON shape without
// importing the coordinator's internal package — this client only ever
// speaks the wire format, same reasoning as JoinRequestSummary itself.
type joinRequestWire struct {
	NodeID      string    `json:"node_id"`
	Role        string    `json:"role"`
	Hostname    string    `json:"hostname"`
	RequestedAt time.Time `json:"requested_at"`
	Status      string    `json:"status"`
}

func (h *HTTP) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, h.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	if h.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.adminToken)
	}
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(b)))
	}
	return resp, nil
}

// workerWire mirrors workerman.Worker's JSON shape without importing the
// coordinator's internal package — same reasoning as joinRequestWire.
type workerWire struct {
	Info struct {
		ID        string  `json:"id"`
		HasGPU    bool    `json:"has_gpu"`
		GPUVramGB float32 `json:"gpu_vram_gb"`
		GPUName   string  `json:"gpu_name"`
		RAMGB     float32 `json:"ram_gb"`
	} `json:"info"`
	LastSeen time.Time `json:"last_seen"`
	State    string    `json:"state"`
	Job      *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"job"`
	Stats struct {
		RAMUsedGB   float32 `json:"ram_used_gb"`
		DiskUsedGB  float32 `json:"disk_used_gb"`
		DiskTotalGB float32 `json:"disk_total_gb"`
	} `json:"stats"`
}

func (h *HTTP) ListWorkers() ([]WorkerSummary, error) {
	resp, err := h.do(http.MethodGet, "/workers", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var workers []workerWire
	if err := json.NewDecoder(resp.Body).Decode(&workers); err != nil {
		return nil, fmt.Errorf("decoding workers list: %w", err)
	}

	summaries := make([]WorkerSummary, 0, len(workers))
	for _, w := range workers {
		s := WorkerSummary{
			ID:          w.Info.ID,
			State:       w.State,
			HasGPU:      w.Info.HasGPU,
			GPUName:     w.Info.GPUName,
			GPUVramGB:   w.Info.GPUVramGB,
			RAMGB:       w.Info.RAMGB,
			RAMUsedGB:   w.Stats.RAMUsedGB,
			DiskTotalGB: w.Stats.DiskTotalGB,
			DiskUsedGB:  w.Stats.DiskUsedGB,
			LastSeen:    relativeTime(w.LastSeen),
		}
		if w.Job != nil {
			s.JobID = w.Job.ID
			s.JobStatus = w.Job.Status
		}
		summaries = append(summaries, s)
	}
	return summaries, nil
}

// jobStatusWire mirrors jobsapi's jobStatusPublic JSON shape without
// importing the coordinator's internal package — same reasoning as
// joinRequestWire/workerWire.
type jobStatusWire struct {
	JobID     string    `json:"job_id"`
	State     string    `json:"state"`
	WorkerID  string    `json:"worker_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (h *HTTP) ListJobs() ([]JobSummary, error) {
	resp, err := h.do(http.MethodGet, "/jobs", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var jobs []jobStatusWire
	if err := json.NewDecoder(resp.Body).Decode(&jobs); err != nil {
		return nil, fmt.Errorf("decoding jobs list: %w", err)
	}

	summaries := make([]JobSummary, 0, len(jobs))
	for _, j := range jobs {
		summaries = append(summaries, JobSummary{
			ID:        j.JobID,
			Status:    j.State,
			Worker:    j.WorkerID,
			Submitted: relativeTime(j.UpdatedAt),
		})
	}
	return summaries, nil
}

func (h *HTTP) SubmitJob(params JobParams) error {
	body, err := json.Marshal(map[string]any{
		"training_script": params.Script,
		"requirements":    params.Requirements,
		"dataset_type":    params.DatasetType,
		"dataset_ref":     params.DatasetRef,
		"base_model_type": params.ModelType,
		"base_model_ref":  params.ModelRef,
		"requires_gpu":    params.RequiresGPU,
	})
	if err != nil {
		return err
	}
	resp, err := h.do(http.MethodPost, "/jobs", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (h *HTTP) CancelJob(jobID string) error {
	resp, err := h.do(http.MethodDelete, "/jobs/"+jobID, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// jobLogsTimeout bounds JobLogs' read of the /logs SSE stream, which
// otherwise stays open until the job finishes — this returns whatever log
// lines arrive within the window as a "logs so far" snapshot instead of
// blocking indefinitely on a still-running job. The caller is expected to
// run this off the UI goroutine (see dashboard's async fetch on open).
// jobLogsTimeout bounds JobLogs' read of the /logs SSE stream. Long enough
// to get historical lines + the coordinator's 2s completion poll; short
// enough that a still-running job returns a "so far" snapshot for the TUI.
const jobLogsTimeout = 5 * time.Second

func (h *HTTP) JobLogs(jobID string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, h.baseURL+"/jobs/"+jobID+"/logs", nil)
	if err != nil {
		return "", err
	}
	if h.adminToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.adminToken)
	}
	resp, err := (&http.Client{Timeout: jobLogsTimeout}).Do(req)
	if err != nil {
		return "", fmt.Errorf("GET /jobs/%s/logs: %w", jobID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GET /jobs/%s/logs: %s: %s", jobID, resp.Status, strings.TrimSpace(string(b)))
	}

	// The stream ends with "event: done\ndata: <state>\n\n" — that data
	// line is a completion marker, not a log line, so it's tracked
	// separately rather than folded into the same "data:" bucket.
	var lines []string
	var doneState string
	sawEvent := false
	scanner := bufio.NewScanner(resp.Body)
	// Log lines can be long; raise the default 64KiB token limit.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "event: done":
			sawEvent = true
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			if sawEvent {
				doneState = data
				sawEvent = false
			} else {
				lines = append(lines, data)
			}
		default:
			sawEvent = false
		}
	}
	// A non-nil scanner.Err() here is the expected case for a still-running
	// job — the client timeout above cuts the stream mid-read, not a real
	// failure, so it's not surfaced as one.
	_ = scanner.Err()

	out := strings.Join(lines, "\n")
	if out == "" {
		out = "(no logs yet)\n\n" +
			"If every job is empty: the worker may have been on the mock executor\n" +
			"(old default) which never ran your Python script. Restart the agent\n" +
			"with --executor training (now the default) and submit again."
	}
	if doneState != "" {
		out += fmt.Sprintf("\n\n-- job %s --", doneState)
	}
	return out, nil
}

func (h *HTTP) ListPendingJoins() ([]JoinRequestSummary, error) {
	resp, err := h.do(http.MethodGet, "/admin/join", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reqs []joinRequestWire
	if err := json.NewDecoder(resp.Body).Decode(&reqs); err != nil {
		return nil, fmt.Errorf("decoding join list: %w", err)
	}

	summaries := make([]JoinRequestSummary, 0, len(reqs))
	for _, r := range reqs {
		if r.Status != "pending" {
			continue // Admin tab only shows what needs a decision
		}
		summaries = append(summaries, JoinRequestSummary{
			NodeID:    r.NodeID,
			Role:      r.Role,
			Hostname:  r.Hostname,
			Submitted: relativeTime(r.RequestedAt),
			Status:    r.Status,
		})
	}
	return summaries, nil
}

func (h *HTTP) ApproveJoin(nodeID string) error {
	resp, err := h.do(http.MethodPost, "/admin/join/"+nodeID+"/approve", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (h *HTTP) RejectJoin(nodeID string) error {
	resp, err := h.do(http.MethodPost, "/admin/join/"+nodeID+"/reject", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// relativeTime gives the same "Ns/Nm/Nh ago" shape the Stub's canned rows
// used, so switching from Stub to HTTP doesn't change how the Admin tab
// reads.
func relativeTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}
