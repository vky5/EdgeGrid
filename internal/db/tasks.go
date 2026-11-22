package db

import (
	"bytes"
	"database/sql"
	"encoding/json"
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

// the most output saved with a task, 64 KB; for longer output only the end is kept
// the end of a log is where the result is, and files carry anything bigger
const MaxTaskOutput = 64 << 10

// the first line of output that was cut to fit
const outputCutNote = "[earlier output cut, only the last 64 KB is kept]\n"

// cuts output to its last 64 KB, starting at a line break, with a note saying it was cut
//
//	"hello world\n"                     → unchanged
//	165 KB of "epoch 1 batch 1/938 …" lines ending "test accuracy 0.9871\n"
//	    → "[earlier output cut, only the last 64 KB is kept]\nepoch 4 batch 117/938 …\n…test accuracy 0.9871\n"
//	70 KB with no line break at all     → the note, then the last bytes as they are
func lastPartOf(output []byte) []byte {
	if len(output) <= MaxTaskOutput {
		return output
	}
	keep := output[len(output)-(MaxTaskOutput-len(outputCutNote)):]
	// start on a whole line rather than half of one
	if i := bytes.IndexByte(keep, '\n'); i >= 0 && i < len(keep)-1 {
		keep = keep[i+1:]
	}
	return append([]byte(outputCutNote), keep...)
}

// one file a finished task produced, kept with its row
// on the node that ran it: a file in results/<task-id>/ waiting for the sender to fetch it
// on the sender: a file it has already fetched into its own results/<task-id>/
//
//	TaskFile{Name: "model.pt",     Size: 4200000, SHA256: "9f2c…"}   // the trained model
//	TaskFile{Name: "metrics.json", Size: 812,     SHA256: "a1b0…"}   // loss per epoch
type TaskFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// how long after sending a task we first go back and ask about it
// e.g. sent at 09:00, first check at 09:10; the live wait covers those 10 minutes
const FirstCheckAfter = 10 * time.Minute

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
	FinishedAt   time.Time  // zero while running
	Attempts     int        // how many times we've gone back to ask, e.g. 2
	NextCheckAt  time.Time  // when to ask next, e.g. 09:30; zero for tasks this node received
	Files        []TaskFile // files the run produced, only for done; nil when it made none
}

// adds a row for a task that just started
// the peer is the node running it for a sent task, and the sender for a received one
// e.g. sending hello to bravo at 09:00 adds a "sent" row: peer bravo, running, first check 09:10
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

// saves how a task ended, in one write so nobody asking sees it done before its files are listed
// output and files are kept only when it is done; output over 64 KB keeps only its last 64 KB
//
//	done,   12 bytes of output, no files            → done, output kept
//	done,   "epoch 5 loss 0.03\n",
//	        [{model.pt 4200000 9f2c…}]              → done, output and the file list kept
//	done,   165 KB of output, one file              → done, the last 64 KB with a note at the top, the file kept
//	failed, some output, "exit 3…"                  → failed, output dropped, error kept
//	a task ID with no row in that role              → ErrTaskNotFound
func (s *Store) RecordTaskFinish(role Role, taskID string, status TaskStatus, output []byte, errMsg string, files []TaskFile) error {
	if status != TaskDone {
		output, files = nil, nil
	}
	output = lastPartOf(output)
	var errVal sql.NullString
	if errMsg != "" {
		errVal = sql.NullString{String: errMsg, Valid: true}
	}
	var filesVal sql.NullString
	if len(files) > 0 {
		b, err := json.Marshal(files)
		if err != nil {
			return fmt.Errorf("db: encode task files: %w", err)
		}
		filesVal = sql.NullString{String: string(b), Valid: true}
	}
	res, err := s.db.Exec(
		`UPDATE tasks SET status = ?, output = ?, error = ?, files = ?, finished_at = ? WHERE task_id = ? AND role = ?`,
		status, output, errVal, filesVal, time.Now().UTC().Unix(), taskID, role,
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
	started_at, finished_at, attempts, next_check_at, files`

// this node's row for one task, as sender or as the one running it
func (s *Store) Task(role Role, taskID string) (TaskRecord, error) {
	return s.taskRow(`SELECT `+taskColumns+` FROM tasks WHERE task_id = ? AND role = ?`, taskID, role)
}

// finds a task this node ran, only if the one asking is the node that sent it
//
//	// on bravo, task 3f2b8c1e-… was sent by alpha5-297b (nK7xQ2CNTRL)
//	ReceivedTaskFor("3f2b8c1e-…", "nK7xQ2CNTRL")   → its row
//	ReceivedTaskFor("3f2b8c1e-…", "nT9wL4CNTRL")   → ErrTaskNotFound, alpha5-a720 didn't send it
//	ReceivedTaskFor("a7d40e19-…", "nK7xQ2CNTRL")   → ErrTaskNotFound, no such task here
func (s *Store) ReceivedTaskFor(taskID, requesterID string) (TaskRecord, error) {
	return s.taskRow(
		`SELECT `+taskColumns+` FROM tasks WHERE task_id = ? AND role = ? AND peer_id = ?`,
		taskID, RoleReceived, requesterID)
}

// sent tasks still running whose next check has come, up to limit, most overdue first
//
//	now 10:00, limit 100
//	slow     running   next check 09:30   listed first
//	hello    running   next check 09:55   listed second
//	slower   running   next check 10:20   not yet
//	big      done                         never
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

// counts one more check on a due sent task and moves its next check to next
// false if it finished or isn't due any more, e.g. another process claimed it first
//
//	row before: running, attempts 0, next check 09:10
//	ClaimSentTask(id, 09:11, 09:31)  true    row: attempts 1, next check 09:31
//	ClaimSentTask(id, 09:11, 09:31)  false   next check is 09:31 now, so not due
//	after B answers done:            false   not running any more
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

// fills a TaskRecord from one row selected with taskColumns
func readTask(row interface{ Scan(...any) error }) (TaskRecord, error) {
	var (
		r        TaskRecord
		output   []byte
		errMsg   sql.NullString
		started  int64
		finished sql.NullInt64
		next     sql.NullInt64
		files    sql.NullString
	)
	err := row.Scan(&r.TaskID, &r.Role, &r.PeerID, &r.PeerHostname, &r.Kind, &r.Status,
		&output, &errMsg, &started, &finished, &r.Attempts, &next, &files)
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
	if files.Valid {
		if err := json.Unmarshal([]byte(files.String), &r.Files); err != nil {
			return TaskRecord{}, fmt.Errorf("db: read task %s files: %w", r.TaskID, err)
		}
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

// deletes finished tasks that started before the cutoff, e.g. over a week ago, and running tasks never
// returns the received tasks among them that had files, so the caller can delete their results folders
// sent tasks are never returned: files the sender fetched are its owner's to keep
//
//	cutoff: a week ago
//	received  done     8 days old   [model.pt]   deleted, ID returned
//	received  done     8 days old   no files     deleted
//	sent      done     8 days old   [model.pt]   deleted, its fetched model.pt stays
//	received  running  8 days old                kept
//	received  done     2 days old   [model.pt]   kept
func (s *Store) PruneTasks(cutoff time.Time) (withFiles []string, err error) {
	rows, err := s.db.Query(
		`DELETE FROM tasks WHERE started_at < ? AND status != ?
		 RETURNING task_id, role, files IS NOT NULL`,
		cutoff.UTC().Unix(), TaskRunning)
	if err != nil {
		return nil, fmt.Errorf("db: prune tasks: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			id       string
			role     Role
			hasFiles bool
		)
		if err := rows.Scan(&id, &role, &hasFiles); err != nil {
			return nil, fmt.Errorf("db: prune tasks: %w", err)
		}
		if role == RoleReceived && hasFiles {
			withFiles = append(withFiles, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: prune tasks: %w", err)
	}
	return withFiles, nil
}
