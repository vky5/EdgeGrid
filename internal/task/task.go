// Package task is the offer/claim wire protocol for dispatching work to a
// peer — the task equivalent of blob's manifest+verdict exchange.
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

// taskWireVersion is this node's Task schema — see the wire diagram above
// WriteTask for how it travels.
const taskWireVersion = 1

// maxTaskSize bounds how much a peer can make us allocate for one offer,
// same reasoning as blob's manifest/hello size caps.
const maxTaskSize = 64 * 1024

// ErrUnsupportedVersion means a peer's Task used a schema this node
// doesn't speak. Claim refuses this one read failure; others get no verdict.
var ErrUnsupportedVersion = errors.New("task: unsupported version")

// Requirements is what a task needs to run — not enforced yet, but here
// so adding enforcement later isn't a breaking wire change.
type Requirements struct {
	CPUCores int   `json:"cpu_cores,omitempty"`
	MemoryMB int64 `json:"memory_mb,omitempty"`
	GPU      bool  `json:"gpu,omitempty"`
}

// Task is what a dispatcher offers a peer to run. Kind and Requirements
// are the real fields; execution itself is still a hardcoded no-op for v1.
type Task struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"`
	Requirements Requirements `json:"requirements"`
}

// New builds a task with a fresh random ID (a UUIDv4) — the normal way to
// construct one, so nothing forgets to set it.
func New(kind string, req Requirements) (*Task, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("task: generate id: %w", err)
	}
	return &Task{ID: id.String(), Kind: kind, Requirements: req}, nil
}

// Validate checks id/kind/requirements before accept() ever sees them —
// the same role blob.Manifest.Validate plays for a file offer.
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
// The version sits outside the JSON so a mismatch is caught before decoding
// a body in a shape this code might not understand.

// WriteTask sends t to w in the format above.
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

// ReadTask decodes one task, checking the version before it reads a single
// byte of body, and rejecting an oversized length before allocating one.
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

// WriteVerdict and ReadVerdict reuse blob's — a task refusal is the same
// accept-or-refuse-with-a-reason shape as a file refusal.
func WriteVerdict(w io.Writer, refusal error) error { return blob.WriteVerdict(w, refusal) }
func ReadVerdict(r io.Reader) error                 { return blob.ReadVerdict(r) }

// RefusedError is what a dispatcher gets back when a peer declines a task.
type RefusedError = blob.RefusedError

// AcceptFunc is a receiver's policy for one offered task. Returning nil
// accepts; an error refuses, its text sent to the dispatcher as the reason.
type AcceptFunc func(t *Task) error

// Offer sends t and waits for the peer's verdict. A *RefusedError means the
// peer declined; anything else is a transport failure.
func Offer(rw io.ReadWriter, t *Task) error {
	if err := WriteTask(rw, t); err != nil {
		return err
	}
	return ReadVerdict(rw)
}

// Claim reads an offered task, asks accept whether to take it, and answers
// with a verdict. It is the mirror image of Offer.
func Claim(rw io.ReadWriter, accept AcceptFunc) (*Task, error) {
	t, err := ReadTask(rw)
	if err != nil {
		if errors.Is(err, ErrUnsupportedVersion) {
			_ = WriteVerdict(rw, err)
		}
		return nil, err
	}
	if err := t.Validate(); err != nil {
		_ = WriteVerdict(rw, err)
		return nil, err
	}
	if err := accept(t); err != nil {
		_ = WriteVerdict(rw, err)
		return nil, fmt.Errorf("task: refused: %w", err)
	}
	if err := WriteVerdict(rw, nil); err != nil {
		return nil, err
	}
	return t, nil
}
