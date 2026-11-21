package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// which end of a task this node was, the sender or the one running it
type Role string

const (
	RoleSent     Role = "sent"
	RoleReceived Role = "received"
)

// where a task stands, e.g. running, done, failed, lost
// a running row left over from before a restart means a crash
type TaskStatus string

// the same values as task.ResultStatus, so a row can be sent as it is
const (
	TaskRunning TaskStatus = "running"
	TaskDone    TaskStatus = "done"
	TaskFailed  TaskStatus = "failed"
	TaskLost    TaskStatus = "lost"
)

// the biggest output saved with a task, 64 KB
// bigger output should go as a file; here it fails instead of being cut short
const MaxTaskOutput = 64 << 10

// no matching task, including one that belongs to a different peer
// a missing task and someone else's task look the same, so a peer can't guess IDs
var ErrTaskNotFound = errors.New("db: task not found")

// one saved task, as this node sees it
// the same task has a row on both ends: "sent" on the sender, "received" on the node that ran it
//
//	// on alpha5-297b, which sent hello to bravo
//	db.TaskRecord{
//		TaskID: "3f2b8c1e-9a4d-4c71-b2e5-7d1f0a6c9e38", Role: "sent",
//		PeerID: "nPvV3wCNTRL", PeerHostname: "bravo",      // the node running it
//		Kind: "hello", Status: "done", Output: []byte("hello world\n"),
//		StartedAt: 21:30:00, FinishedAt: 21:30:02,
//	}
//	// on bravo, the same task
//	db.TaskRecord{
//		TaskID: "3f2b8c1e-9a4d-4c71-b2e5-7d1f0a6c9e38", Role: "received",
//		PeerID: "nK7xQ2CNTRL", PeerHostname: "alpha5-297b", // the sender, as Tailscale named it
//		Kind: "hello", Status: "done", Output: []byte("hello world\n"),
//	}
//	// other states of a row
//	Status: "running", FinishedAt: zero                                // still going, or the sender hasn't asked yet
//	Status: "failed",  Error: "bare: [sh -c echo boom >&2; exit 3]: exit status 3 (stderr: \"boom\\n\")"
//	Status: "failed",  Error: "verdict not delivered"                  // accepted, but the sender hung up before hearing yes
//	Status: "lost",    Error: "node restarted"                         // was running when this node restarted
//	Status: "lost",    Error: "bare: [sleep 30]: signal: killed (stderr: \"\")"   // node shut down mid-run
type TaskRecord struct {
	TaskID       string
	Role         Role
	PeerID       string
	PeerHostname string // e.g. bravo
	Kind         string // e.g. hello
	Status       TaskStatus
	Output       []byte
	Error        string // e.g. "node restarted" for a run lost to a restart
	StartedAt    time.Time
	FinishedAt   time.Time // zero while running
}

// adds a row for a task that just started
// the peer is the node running it for a sent task, and the sender for a received one
// e.g. sending hello to bravo adds a "sent" row with peer bravo, status running
func (s *Store) RecordTaskStart(role Role, taskID, peerID, peerHostname, kind string) error {
	_, err := s.db.Exec(
		`INSERT INTO tasks (task_id, role, peer_id, peer_hostname, kind, status, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		taskID, role, peerID, peerHostname, kind, TaskRunning, time.Now().UTC().Unix(),
	)
	if err != nil {
		return fmt.Errorf("db: record task start: %w", err)
	}
	return nil
}

// saves how a task ended; output is kept only when it is done
// done output over 64 KB is saved as a failure instead of being cut
//
//	done,   12 bytes of output         → done, output kept
//	done,   70000 bytes of output      → failed, no output, error "output exceeded 65536 bytes"
//	failed, some output, "exit 3…"     → failed, output dropped, error kept
//	a task ID with no row in that role → ErrTaskNotFound
func (s *Store) RecordTaskFinish(role Role, taskID string, status TaskStatus, output []byte, errMsg string) error {
	if status == TaskDone && len(output) > MaxTaskOutput {
		status, output = TaskFailed, nil
		errMsg = fmt.Sprintf("output exceeded %d bytes", MaxTaskOutput)
	}
	if status != TaskDone {
		output = nil
	}
	var errVal sql.NullString
	if errMsg != "" {
		errVal = sql.NullString{String: errMsg, Valid: true}
	}
	res, err := s.db.Exec(
		`UPDATE tasks SET status = ?, output = ?, error = ?, finished_at = ? WHERE task_id = ? AND role = ?`,
		status, output, errVal, time.Now().UTC().Unix(), taskID, role,
	)
	if err != nil {
		return fmt.Errorf("db: record task finish: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTaskNotFound
	}
	return nil
}

// this node's row for one task, as sender or as the one running it
func (s *Store) Task(role Role, taskID string) (TaskRecord, error) {
	return s.scanTask(
		`SELECT task_id, role, peer_id, peer_hostname, kind, status, output, error, started_at, finished_at
		 FROM tasks WHERE task_id = ? AND role = ?`, taskID, role)
}

// finds a task this node ran, only if the one asking is the node that sent it
//
//	// on bravo, task 3f2b8c1e-… was sent by alpha5-297b (nK7xQ2CNTRL)
//	ReceivedTaskFor("3f2b8c1e-…", "nK7xQ2CNTRL")   → its row
//	ReceivedTaskFor("3f2b8c1e-…", "nT9wL4CNTRL")   → ErrTaskNotFound, alpha5-a720 didn't send it
//	ReceivedTaskFor("a7d40e19-…", "nK7xQ2CNTRL")   → ErrTaskNotFound, no such task here
func (s *Store) ReceivedTaskFor(taskID, requesterID string) (TaskRecord, error) {
	return s.scanTask(
		`SELECT task_id, role, peer_id, peer_hostname, kind, status, output, error, started_at, finished_at
		 FROM tasks WHERE task_id = ? AND role = ? AND peer_id = ?`, taskID, RoleReceived, requesterID)
}

func (s *Store) scanTask(query string, args ...any) (TaskRecord, error) {
	var (
		r        TaskRecord
		output   []byte
		errMsg   sql.NullString
		started  int64
		finished sql.NullInt64
	)
	err := s.db.QueryRow(query, args...).Scan(
		&r.TaskID, &r.Role, &r.PeerID, &r.PeerHostname, &r.Kind, &r.Status, &output, &errMsg, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, ErrTaskNotFound
	}
	if err != nil {
		return TaskRecord{}, fmt.Errorf("db: read task: %w", err)
	}
	r.Output, r.Error = output, errMsg.String
	r.StartedAt = time.Unix(started, 0)
	if finished.Valid {
		r.FinishedAt = time.Unix(finished.Int64, 0)
	}
	return r, nil
}

// marks tasks this node was running itself as lost with the error "node restarted"; call once at startup
// those runs died with the old process; tasks sent elsewhere stay running, since a restart here doesn't stop them
//
//	before                         after
//	received  running              received  lost, "node restarted"
//	sent      running              sent      running        // still running on the peer
//	received  done                 received  done           // finished rows are left alone
func (s *Store) ReconcileStaleTasks() error {
	_, err := s.db.Exec(
		`UPDATE tasks SET status = ?, error = ?, finished_at = ? WHERE status = ? AND role = ?`,
		TaskLost, "node restarted", time.Now().UTC().Unix(), TaskRunning, RoleReceived,
	)
	if err != nil {
		return fmt.Errorf("db: reconcile stale tasks: %w", err)
	}
	return nil
}

// deletes finished tasks that started before the cutoff, e.g. over a week ago
// running tasks are never deleted
func (s *Store) PruneTasks(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE started_at < ? AND status != ?`, cutoff.UTC().Unix(), TaskRunning)
	if err != nil {
		return fmt.Errorf("db: prune tasks: %w", err)
	}
	return nil
}
