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

// The values match task.ResultStatus, so a row can be sent as-is.
const (
	TaskRunning TaskStatus = "running"
	TaskDone    TaskStatus = "done"
	TaskFailed  TaskStatus = "failed"
	TaskLost    TaskStatus = "lost"
)

// MaxTaskOutput is the largest result stored inline. Bigger outputs belong in
// a blob transfer; they fail loudly here instead of being truncated.
const MaxTaskOutput = 64 << 10

// FirstCheckAfter is how long after sending a task this node first goes back
// to ask about it, if nothing is waiting for the answer by then.
const FirstCheckAfter = 10 * time.Minute

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
	Attempts     int       // how many times this node has gone back to ask
	NextCheckAt  time.Time // when to ask next; zero for tasks this node received
}

// RecordTaskStart inserts a running row. peer is the executor for RoleSent and
// the dispatcher for RoleReceived. A sent task gets its first check
// FirstCheckAfter from now.
func (s *Store) RecordTaskStart(role Role, taskID, peerID, peerHostname, kind string) error {
	now := time.Now().UTC()
	var next sql.NullInt64
	if role == RoleSent {
		next = sql.NullInt64{Int64: now.Add(FirstCheckAfter).Unix(), Valid: true}
	}
	_, err := s.db.Exec(
		`INSERT INTO tasks (task_id, role, peer_id, peer_hostname, kind, status, started_at, next_check_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, role, peerID, peerHostname, kind, TaskRunning, now.Unix(), next,
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

// taskColumns is every column readTask expects, in its order.
const taskColumns = `task_id, role, peer_id, peer_hostname, kind, status, output, error,
	started_at, finished_at, attempts, next_check_at`

// Task returns this node's row for taskID in the given role.
func (s *Store) Task(role Role, taskID string) (TaskRecord, error) {
	return s.taskRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ? AND role = ?`, taskID, role)
}

// ReceivedTaskFor is the lookup behind answering a result request: it matches
// only when requesterID is the peer that dispatched the task.
func (s *Store) ReceivedTaskFor(taskID, requesterID string) (TaskRecord, error) {
	return s.taskRow(
		`SELECT `+taskColumns+` FROM tasks WHERE task_id = ? AND role = ? AND peer_id = ?`,
		taskID, RoleReceived, requesterID)
}

// DueSentTasks lists up to limit sent tasks still running whose next check
// is at or before now, the most overdue first.
func (s *Store) DueSentTasks(now time.Time, limit int) ([]TaskRecord, error) {
	rows, err := s.db.Query(
		`SELECT `+taskColumns+` FROM tasks
		 WHERE role = ? AND status = ? AND next_check_at <= ?
		 ORDER BY next_check_at LIMIT ?`,
		RoleSent, TaskRunning, now.UTC().Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("db: list due tasks: %w", err)
	}
	defer rows.Close()

	var out []TaskRecord
	for rows.Next() {
		r, err := readTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: list due tasks: %w", err)
	}
	return out, nil
}

// ClaimSentTask counts one more check on a due sent task and moves its next
// check to next. It returns false if the task is no longer running or no
// longer due, for example because someone else claimed it first.
func (s *Store) ClaimSentTask(taskID string, now, next time.Time) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE tasks SET attempts = attempts + 1, next_check_at = ?
		 WHERE task_id = ? AND role = ? AND status = ? AND next_check_at <= ?`,
		next.UTC().Unix(), taskID, RoleSent, TaskRunning, now.UTC().Unix())
	if err != nil {
		return false, fmt.Errorf("db: claim task: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("db: claim task: %w", err)
	}
	return n == 1, nil
}

func (s *Store) taskRow(query string, args ...any) (TaskRecord, error) {
	r, err := readTask(s.db.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, ErrTaskNotFound
	}
	return r, err
}

// readTask fills a TaskRecord from one row selected with taskColumns.
func readTask(row interface{ Scan(...any) error }) (TaskRecord, error) {
	var (
		r        TaskRecord
		output   []byte
		errMsg   sql.NullString
		started  int64
		finished sql.NullInt64
		next     sql.NullInt64
	)
	err := row.Scan(&r.TaskID, &r.Role, &r.PeerID, &r.PeerHostname, &r.Kind, &r.Status,
		&output, &errMsg, &started, &finished, &r.Attempts, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return TaskRecord{}, err
	}
	if err != nil {
		return TaskRecord{}, fmt.Errorf("db: read task: %w", err)
	}
	r.Output, r.Error = output, errMsg.String
	r.StartedAt = time.Unix(started, 0)
	if finished.Valid {
		r.FinishedAt = time.Unix(finished.Int64, 0)
	}
	if next.Valid {
		r.NextCheckAt = time.Unix(next.Int64, 0)
	}
	return r, nil
}

// ReconcileStaleTasks marks the tasks this node was running itself as lost.
// Call once at startup: those runs died with the old process. Tasks this node
// sent elsewhere are left running, because restarting here doesn't stop them.
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

// PruneTasks deletes finished rows that started before cutoff. Running rows
// are never pruned.
func (s *Store) PruneTasks(cutoff time.Time) error {
	_, err := s.db.Exec(`DELETE FROM tasks WHERE started_at < ? AND status != ?`, cutoff.UTC().Unix(), TaskRunning)
	if err != nil {
		return fmt.Errorf("db: prune tasks: %w", err)
	}
	return nil
}
