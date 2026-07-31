// Package client is the dashboard TUI's only way of talking to a running
// coordinator. Screens depend on the Client interface below, never on a
// concrete transport — HTTP (the coordinator's backendMux) vs. NATS
// request-reply is still an open decision for Jobs/Workers, and keeping
// screens behind this interface means that decision won't require
// touching any screen code.
package client

import "fmt"

// JobSummary is the subset of job state a screen needs to render a row or
// detail view. Deliberately not internal/broker's proto type — the TUI
// shouldn't couple to the wire format before the transport is chosen.
type JobSummary struct {
	ID        string
	Status    string
	Worker    string
	Submitted string
}

// WorkerSummary is the worker state the Workers tab renders — the list
// view shows a compact subset of these fields, the detail view all of them.
type WorkerSummary struct {
	ID          string
	State       string
	HasGPU      bool
	GPUName     string
	GPUVramGB   float32
	RAMGB       float32
	RAMUsedGB   float32
	DiskTotalGB float32
	DiskUsedGB  float32
	LastSeen    string
	JobID       string // "" if idle
	JobStatus   string
}

// JoinRequestSummary is the subset of joinmgr.JoinRequest the Admin tab
// renders and acts on.
type JoinRequestSummary struct {
	NodeID    string
	Role      string
	Hostname  string
	Submitted string
	Status    string
}

// TokenSummary is a minted Tailscale auth key's status, as the Tokens tab
// renders it — never the raw key itself, only tokenapi's List response.
type TokenSummary struct {
	ID        string
	CreatedAt string
	Revoked   bool
	Activated bool
	NodeID    string
	NodeIP    string
	Hostname  string
	Role      string
}

// MintedToken is what a fresh mint returns — Key is shown exactly once.
type MintedToken struct {
	ID  string
	Key string
}

type JobParams struct {
	Script       string
	Requirements string
	DatasetType  string
	DatasetRef   string
	ModelType    string
	ModelRef     string
	RequiresGPU  bool
}

// Client is everything the dashboard screens need. Jobs/Workers are still
// stubbed — see HTTP's doc comment; Admin is real.
type Client interface {
	ListJobs() ([]JobSummary, error)
	JobLogs(jobID string) (string, error)
	SubmitJob(params JobParams) error
	CancelJob(jobID string) error

	ListWorkers() ([]WorkerSummary, error)

	ListPendingJoins() ([]JoinRequestSummary, error)
	ApproveJoin(nodeID string) error
	RejectJoin(nodeID string) error

	ListTokens() ([]TokenSummary, error)
	MintToken() (MintedToken, error)
	RevokeToken(id string) error
}

// Stub is a placeholder Client returning canned data so screens can be
// built and navigated before a transport backs them for real.
type Stub struct{}

func New() *Stub { return &Stub{} }

func (s *Stub) ListJobs() ([]JobSummary, error) {
	return []JobSummary{
		{ID: "a3f9", Status: "running", Worker: "worker-2", Submitted: "2m ago"},
		{ID: "b812", Status: "queued", Worker: "-", Submitted: "10s ago"},
	}, nil
}

func (s *Stub) JobLogs(jobID string) (string, error) {
	return "(stub) no transport wired yet — logs for " + jobID + " would appear here", nil
}

func (s *Stub) SubmitJob(params JobParams) error { return nil }

func (s *Stub) CancelJob(jobID string) error { return nil }

func (s *Stub) ListWorkers() ([]WorkerSummary, error) {
	return []WorkerSummary{
		{ID: "worker-1", State: "free", HasGPU: true, GPUName: "RTX 4090", GPUVramGB: 24,
			RAMGB: 32, RAMUsedGB: 4.2, DiskTotalGB: 512, DiskUsedGB: 88, LastSeen: "3s ago"},
		{ID: "worker-2", State: "busy", RAMGB: 16, RAMUsedGB: 9.8, DiskTotalGB: 256, DiskUsedGB: 140,
			LastSeen: "1s ago", JobID: "b812", JobStatus: "running"},
	}, nil
}

func (s *Stub) ListPendingJoins() ([]JoinRequestSummary, error) {
	return []JoinRequestSummary{
		{NodeID: "node-9f2c", Role: "worker", Hostname: "vaibhav-laptop", Submitted: "5m ago", Status: "pending"},
	}, nil
}

func (s *Stub) ApproveJoin(nodeID string) error { return nil }

func (s *Stub) RejectJoin(nodeID string) error { return nil }

func (s *Stub) ListTokens() ([]TokenSummary, error) { return nil, nil }

func (s *Stub) MintToken() (MintedToken, error) {
	return MintedToken{}, fmt.Errorf("no transport wired yet — mint requires a real coordinator connection")
}

func (s *Stub) RevokeToken(id string) error { return nil }
