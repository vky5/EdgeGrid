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

// how long after sending a task we first go back and ask about it
// e.g. sent at 09:00, first check at 09:10; the live wait covers those 10 minutes
const FirstCheckAfter = 10 * time.Minute

// no matching task, including one that belongs to a different peer
// a missing task and someone else's task look the same, so a peer can't guess IDs
var ErrTaskNotFound = errors.New("db: task not found")

// one saved task, as this node sees it
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

// adds a row for a task that just started
// the peer is the node running it for a sent task, and the sender for a received one
// a sent task gets its first check 10 minutes from now (FirstCheckAfter)
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

// saves how a task ended; output is kept only when it is done
// done output over 64 KB is saved as a failure instead of being cut
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

// every column readTask expects, in its order
const taskColumns = `task_id, role, peer_id, peer_hostname, kind, status, output, error,
	started_at, finished_at, attempts, next_check_at`

// this node's row for one task, as sender or as the one running it
func (s *Store) Task(role Role, taskID string) (TaskRecord, error) {
	return s.taskRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ? AND role = ?`, taskID, role)
}

// finds a task this node ran, only if the one asking is the node that sent it
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

// marks tasks this node was running itself as lost; call once at startup
// those runs died with the old process; tasks sent elsewhere stay running, since a restart here doesn't stop them
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
