package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Role is which end of a task this node was.
type Role string

const (
	RoleSent     Role = "sent"
	RoleReceived Role = "received"
)

// TaskStatus is where a task row stands. A stale TaskRunning row is a crash —
// see ReconcileStaleTasks.
type TaskStatus string

const (
	TaskRunning     TaskStatus = "running"
	TaskDone        TaskStatus = "done"
	TaskFailed      TaskStatus = "failed"
	TaskInterrupted TaskStatus = "interrupted"
)

// MaxTaskOutput is the largest result stored inline. Bigger outputs belong in
// a blob transfer; they fail loudly here instead of being truncated.
const MaxTaskOutput = 64 << 10

// ErrTaskNotFound is returned when no row matches — including a row that
// exists but belongs to a different peer, so callers can't probe for IDs.
var ErrTaskNotFound = errors.New("db: task not found")

// TaskRecord is one stored task, as seen from this node.
type TaskRecord struct {
	TaskID       string
	Role         Role
	PeerID       string
	PeerHostname string
	Kind         string
	Status       TaskStatus
	Output       []byte
	Error        string
	StartedAt    time.Time
	FinishedAt   time.Time // zero while running
}

// RecordTaskStart inserts a running row. peer is the executor for RoleSent and
// the dispatcher for RoleReceived.
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

// RecordTaskFinish stores the outcome. Output is kept only for TaskDone, and a
// done result over MaxTaskOutput is turned into a failure rather than cut.
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

// Task returns this node's row for taskID in the given role.
func (s *Store) Task(role Role, taskID string) (TaskRecord, error) {
	return s.scanTask(
		`SELECT task_id, role, peer_id, peer_hostname, kind, status, output, error, started_at, finished_at
		 FROM tasks WHERE task_id = ? AND role = ?`, taskID, role)
}

// ReceivedTaskFor is the lookup behind answering a result request: it matches
// only when requesterID is the peer that dispatched the task.
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

// ReconcileStaleTasks marks every running row interrupted. Call once at
// startup — such a row predates this process, so its run died with it.
func (s *Store) ReconcileStaleTasks() error {
	_, err := s.db.Exec(
		`UPDATE tasks SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		TaskInterrupted, "node restarted", time.Now().UTC().Unix(), TaskRunning,
	)
	if err != nil {
		return fmt.Errorf("db: reconcile stale tasks: %w", err)
	}
	return nil
}

// PruneTasks deletes finished rows that started before cutoff. Running rows
// are never pruned.
func (s *Store) PruneTasks(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE started_at < ? AND status != ?`, cutoff.UTC().Unix(), TaskRunning)
	if err != nil {
		return fmt.Errorf("db: prune tasks: %w", err)
	}
	return nil
}
