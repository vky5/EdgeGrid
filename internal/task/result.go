package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// the version byte at the start of each result message, ask and answer alike
const resultWireVersion = 2

// the biggest result message, 128 KB
// 64 KB of output grows to about 87 KB as base64 in the JSON, so this leaves room
const maxResultSize = 128 * 1024

// how long the node that ran a task keeps its result for the sender, a week
// the sender stops asking after the same time
const ResultRetention = 7 * 24 * time.Hour

// how a task stands, as reported by the node running it
type ResultStatus string

const (
	StatusRunning ResultStatus = "running"
	StatusDone    ResultStatus = "done"
	// the task itself broke, so sending it again won't help
	StatusFailed ResultStatus = "failed"
	// no result and not the task's fault, so sending it again makes sense
	StatusLost ResultStatus = "lost"
)

// the reason given for a task the node can't show you, whether it never existed or is someone else's
const NoRecord = "no record of this task"

func (s ResultStatus) valid() bool {
	switch s {
	case StatusRunning, StatusDone, StatusFailed, StatusLost:
		return true
	}
	return false
}

// whether the sender can stop asking: the run ended, or never will
func (s ResultStatus) Finished() bool { return s != StatusRunning }

// asks the node that ran a task how it went, e.g. {"task_id":"3f2b8c1e-…"}
type ResultRequest struct {
	TaskID string `json:"task_id"`
}

// the answer from the node that ran the task
// Output is set only for done, Error only for failed and lost
//
//	{"status":"running"}                                      // still going, ask again later
//	{"status":"done","output":"aGVsbG8gd29ybGQK"}             // kind hello printed "hello world\n" (base64 on the wire)
//	{"status":"failed","error":"bare: [sh -c echo boom \u003e\u00262; exit 3]: exit status 3 (stderr: \"boom\\n\")"}
//	                                                          // kind fail exited 3; Go's JSON escapes > and & as \u003e \u0026
//	{"status":"lost","error":"node restarted"}                // bravo restarted mid-run
//	{"status":"lost","error":"no record of this task"}        // pruned after a week, never existed, or someone else's
type ResultResponse struct {
	Status ResultStatus `json:"status"`
	Output []byte       `json:"output,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// nil for a successful run, otherwise why the task gave no output
//
//	Status: "done", Output: "hello world\n"          → nil
//	Status: "running"                               → "task is still running"
//	Status: "failed", Error: ""                     → "task failed"
//	Status: "failed", Error: "bare: [sh -c echo boom >&2; exit 3]: exit status 3 (stderr: \"boom\\n\")"
//	                                                → "task failed: bare: [sh -c echo boom >&2; exit 3]: …"
//	Status: "lost", Error: "no record of this task" → "task lost: no record of this task"
func (r ResultResponse) Err() error {
	switch r.Status {
	case StatusDone:
		return nil
	case StatusRunning:
		return errors.New("task is still running")
	}
	if r.Error == "" {
		return fmt.Errorf("task %s", r.Status)
	}
	return fmt.Errorf("task %s: %s", r.Status, r.Error)
}

// One result exchange on the wire, after the hello (intent "result") has gone
// both ways on the same TCP connection. Two frames, one each direction:
//
//	dispatcher -> executor   request
//	executor   -> dispatcher response
//
// Each frame is laid out the same way:
//
//	byte 0      version        (resultWireVersion)
//	bytes 1-4   length         big-endian, of the JSON body that follows
//	bytes 5-N   JSON body
//
// request body:
//
//	{"task_id":"<uuid>"}
//
// response body (output is base64, as encoding/json does for []byte):
//
//	{"status":"running|done|failed|lost",
//	 "output":"<base64>",   only when done, at most 64KB before encoding
//	 "error":"<text>"}      only when failed or lost
//
// a missing task and someone else's both get lost with NoRecord, so a peer can't guess IDs

// sends one result message in the format above
func writeResultFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("task: encode result frame: %w", err)
	}
	if len(body) > maxResultSize {
		return fmt.Errorf("task: result frame %d bytes exceeds max %d", len(body), maxResultSize)
	}
	frame := make([]byte, 5, 5+len(body))
	frame[0] = resultWireVersion
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	if _, err := w.Write(append(frame, body...)); err != nil {
		return fmt.Errorf("task: write result frame: %w", err)
	}
	return nil
}

func readResultFrame(r io.Reader, v any) error {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return fmt.Errorf("task: read result header: %w", err)
	}
	if hdr[0] != resultWireVersion {
		return fmt.Errorf("%w: peer sent %d, this node speaks %d", ErrUnsupportedVersion, hdr[0], resultWireVersion)
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > maxResultSize {
		return fmt.Errorf("task: result length %d exceeds max %d", n, maxResultSize)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return fmt.Errorf("task: read result body: %w", err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("task: decode result frame: %w", err)
	}
	return nil
}

// asks the peer how a task went and reads the answer
// an error means the connection failed; a task the peer doesn't know comes back as lost
//
//	peer answers {"status":"done","output":"aGVsbG8gd29ybGQK"}   → ResultResponse{Status: "done", Output: "hello world\n"}, nil
//	peer answers {"status":"lost","error":"node restarted"}     → ResultResponse{Status: "lost", …}, nil; not a Go error
//	peer answers {"status":"paused"}                            → "task: peer sent unknown result status \"paused\""
//	peer answers with version 1                                 → "task: unsupported version: peer sent 1, this node speaks 2"
func AskResult(rw io.ReadWriter, taskID string) (ResultResponse, error) {
	if err := writeResultFrame(rw, ResultRequest{TaskID: taskID}); err != nil {
		return ResultResponse{}, err
	}
	var resp ResultResponse
	if err := readResultFrame(rw, &resp); err != nil {
		return ResultResponse{}, err
	}
	if !resp.Status.valid() {
		return ResultResponse{}, fmt.Errorf("task: peer sent unknown result status %q", resp.Status)
	}
	return resp, nil
}

// reads one ask and answers it from lookup
// a task ID that isn't a valid ID is answered as lost without calling lookup
//
//	{"task_id":"3f2b8c1e-9a4d-4c71-b2e5-7d1f0a6c9e38"}   → whatever lookup returns, e.g. {"status":"running"}
//	{"task_id":"../../etc/passwd"}                     → {"status":"lost","error":"no record of this task"}, lookup not called
func ServeResult(rw io.ReadWriter, lookup func(taskID string) ResultResponse) error {
	var req ResultRequest
	if err := readResultFrame(rw, &req); err != nil {
		return err
	}
	resp := ResultResponse{Status: StatusLost, Error: NoRecord}
	if _, err := uuid.Parse(req.TaskID); err == nil {
		resp = lookup(req.TaskID)
	}
	return writeResultFrame(rw, resp)
}
