// Package task sends a task to a peer and gets back yes or no, and later the result.
package task

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/edgegrid/edgegrid/internal/blob"
)

// the version byte at the start of each task offer
const taskWireVersion = 1

// the biggest task offer we read, 64 KB, so a peer can't make us allocate more
const maxTaskSize = 64 * 1024

// a peer's task used a version this node doesn't speak, e.g. version 2 when we only know 1
// the only read failure that still gets a refusal back; others get no answer
var ErrUnsupportedVersion = errors.New("task: unsupported version")

// what a task needs to run, e.g. 2 CPU cores or a GPU; not checked yet
// it is already in the message, so checking it later won't break older nodes
type Requirements struct {
	CPUCores int   `json:"cpu_cores,omitempty"`
	MemoryMB int64 `json:"memory_mb,omitempty"`
	GPU      bool  `json:"gpu,omitempty"`
}

// what a sender offers a peer to run, e.g. {"id":"3f2b8c1e-…","kind":"hello","requirements":{}}
// Kind and Requirements are the real fields; running it is still a hardcoded no-op for v1
type Task struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"` // must match with exectuor.go defintions
	Requirements Requirements `json:"requirements"`
	// small JSON given to the run; big data goes as a file instead
	Input json.RawMessage `json:"input,omitempty"`
}

// builds a task with a new random ID, e.g. 3f2b8c1e-…, so nothing forgets to set one
func New(kind string, req Requirements) (*Task, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("task: generate id: %w", err)
	}
	return &Task{ID: id.String(), Kind: kind, Requirements: req}, nil
}

// checks the ID, kind and requirements before accept ever sees them
// e.g. a task with no kind fails with "task: kind is empty"
func (t *Task) Validate() error {
	if t.ID == "" {
		return errors.New("task: id is empty")
	}
	if _, err := uuid.Parse(t.ID); err != nil {
		return fmt.Errorf("task: id is not a valid uuid: %w", err)
	}
	if t.Kind == "" {
		return errors.New("task: kind is empty")
	}
	if t.Requirements.CPUCores < 0 {
		return fmt.Errorf("task: negative cpu_cores %d", t.Requirements.CPUCores)
	}
	if t.Requirements.MemoryMB < 0 {
		return fmt.Errorf("task: negative memory_mb %d", t.Requirements.MemoryMB)
	}
	return nil
}

// One task offer on the wire:
//
//	byte 0      version        (taskWireVersion)
//	bytes 1-4   length         big-endian, of the JSON body that follows
//	bytes 5-N   JSON body      {"id":"...","kind":"...","requirements":{...}}
//
// the version is outside the JSON, so a mismatch is caught before decoding a body we may not understand

// sends a task in the format above
func WriteTask(w io.Writer, t *Task) error {
	body, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("task: encode: %w", err)
	}
	hdr := make([]byte, 5, 5+len(body))
	hdr[0] = taskWireVersion
	binary.BigEndian.PutUint32(hdr[1:5], uint32(len(body)))
	if _, err := w.Write(append(hdr, body...)); err != nil {
		return fmt.Errorf("task: write: %w", err)
	}
	return nil
}

// reads one task, checking the version before the body
// and refusing an oversized length before making room for it
// e.g. a 70000-byte length fails with "task: length 70000 exceeds max 65536"
func ReadTask(r io.Reader) (*Task, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("task: read header: %w", err)
	}
	if hdr[0] != taskWireVersion {
		return nil, fmt.Errorf("%w: peer sent %d, this node speaks %d", ErrUnsupportedVersion, hdr[0], taskWireVersion)
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > maxTaskSize {
		return nil, fmt.Errorf("task: length %d exceeds max %d", n, maxTaskSize)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("task: read body: %w", err)
	}
	var t Task
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, fmt.Errorf("task: decode: %w", err)
	}
	return &t, nil
}

// the yes-or-no answer to an offer, the same as blob's, since a task refusal looks like a file refusal
func WriteVerdict(w io.Writer, refusal error) error { return blob.WriteVerdict(w, refusal) }
func ReadVerdict(r io.Reader) error                 { return blob.ReadVerdict(r) }

// what the sender gets back when a peer says no to a task
// e.g. "peer refused the transfer: this node is already running a task"
type RefusedError = blob.RefusedError

// sends a task and waits for the peer's yes or no
// a *RefusedError means the peer said no; any other error means the connection failed
func Offer(rw io.ReadWriter, t *Task) error {
	if err := WriteTask(rw, t); err != nil {
		return err
	}
	return ReadVerdict(rw)
}
