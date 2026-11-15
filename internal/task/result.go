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

// resultWireVersion versions both result messages. Same layout as an offer:
// version byte, 4-byte big-endian length, JSON body.
const resultWireVersion = 2

// maxResultSize bounds a result frame. A 64KB output is about 87KB once
// base64'd inside the JSON, so this leaves room for that plus the envelope.
const maxResultSize = 128 * 1024

// ResultRetention is how long the node that ran a task keeps its result for
// the sender to collect. The sender stops asking after the same time.
const ResultRetention = 7 * 24 * time.Hour

// ResultStatus is what the executing node reports about a task.
type ResultStatus string

const (
	StatusRunning ResultStatus = "running"
	StatusDone    ResultStatus = "done"
	// the task itself broke, so sending it again won't help
	StatusFailed ResultStatus = "failed"
	// no result and not the task's fault, so sending it again makes sense
	StatusLost ResultStatus = "lost"
)

// NoRecord is the reason given for a task the executing node can't show you,
// whether it never existed or belongs to someone else.
const NoRecord = "no record of this task"

func (s ResultStatus) valid() bool {
	switch s {
	case StatusRunning, StatusDone, StatusFailed, StatusLost:
		return true
	}
	return false
}

// Finished reports whether polling can stop: the run ended, or never will.
func (s ResultStatus) Finished() bool { return s != StatusRunning }

// ResultRequest asks the executing node how a task went.
type ResultRequest struct {
	TaskID string `json:"task_id"`
}

// ResultResponse is the executing node's answer. Output is set only for done,
// Error only for failed and lost.
type ResultResponse struct {
	Status ResultStatus `json:"status"`
	Output []byte       `json:"output,omitempty"`
	Error  string       `json:"error,omitempty"`
}

// Err is nil for a successful run, otherwise why the task produced no output.
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
// As with an offer, the version sits outside the JSON so a mismatch is caught
// before a body in an unknown shape is decoded. The executor answers lost with
// NoRecord for a missing task and for someone else's, so the two can't be told
// apart.

// writeResultFrame sends v in the format above.
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

// AskResult sends a request for taskID and reads the answer. An error means
// the exchange failed; a task the peer doesn't know comes back as lost.
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

// ServeResult reads one request and answers it with lookup's result. A
// malformed task ID is answered as lost without calling lookup.
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
