package task

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestOfferAndClaimRoundTrip(t *testing.T) {
	dispatcher, target := net.Pipe()
	defer dispatcher.Close()
	defer target.Close()

	sent, err := New("noop", Requirements{CPUCores: 2, MemoryMB: 512, GPU: true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	go func() {
		_, err := ProcessTask(target, func(got *Task) error {
			if got.ID != sent.ID {
				t.Errorf("ProcessTask saw ID = %q, want %q", got.ID, sent.ID)
			}
			if got.Kind != "noop" {
				t.Errorf("ProcessTask saw Kind = %q, want noop", got.Kind)
			}
			if got.Requirements != sent.Requirements {
				t.Errorf("ProcessTask saw Requirements = %+v, want %+v", got.Requirements, sent.Requirements)
			}
			return nil
		})
		if err != nil {
			t.Errorf("ProcessTask: %v", err)
		}
	}()

	if err := Offer(dispatcher, sent); err != nil {
		t.Fatalf("Offer: %v", err)
	}
}

func TestNewIDsDontCollide(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		tk, err := New("noop", Requirements{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		id := tk.ID
		if seen[id] {
			t.Fatalf("New repeated id %q", id)
		}
		seen[id] = true
	}
}

func TestNewSetsAllFields(t *testing.T) {
	req := Requirements{CPUCores: 4, MemoryMB: 1024, GPU: true}
	tk, err := New("noop", req)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tk.ID == "" {
		t.Error("New left ID empty")
	}
	if tk.Kind != "noop" {
		t.Errorf("Kind = %q, want noop", tk.Kind)
	}
	if tk.Requirements != req {
		t.Errorf("Requirements = %+v, want %+v", tk.Requirements, req)
	}
}

func TestValidateAcceptsAWellFormedTask(t *testing.T) {
	tk, err := New("noop", Requirements{CPUCores: 2, MemoryMB: 512})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := tk.Validate(); err != nil {
		t.Errorf("Validate rejected a well-formed task: %v", err)
	}
}

func TestValidateRejectsBadTasks(t *testing.T) {
	good, err := New("noop", Requirements{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cases := []struct {
		name string
		tk   Task
	}{
		{"empty id", Task{ID: "", Kind: good.Kind}},
		{"malformed id", Task{ID: "not-a-uuid", Kind: good.Kind}},
		{"empty kind", Task{ID: good.ID, Kind: ""}},
		{"negative cpu cores", Task{ID: good.ID, Kind: good.Kind, Requirements: Requirements{CPUCores: -1}}},
		{"negative memory", Task{ID: good.ID, Kind: good.Kind, Requirements: Requirements{MemoryMB: -1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.tk.Validate(); err == nil {
				t.Errorf("Validate accepted a task with %s", c.name)
			}
		})
	}
}

// Validate runs before accept, so a malformed task never reaches policy —
// the refusal reason should say what was actually wrong, not "busy" or
// whatever accept would have said.
func TestProcessTaskRefusesAnInvalidTaskWithoutCallingAccept(t *testing.T) {
	dispatcher, target := net.Pipe()
	defer dispatcher.Close()
	defer target.Close()

	acceptCalled := false
	go func() {
		_, _ = ProcessTask(target, func(*Task) error {
			acceptCalled = true
			return nil
		})
	}()

	err := Offer(dispatcher, &Task{ID: "", Kind: "noop"})
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want *RefusedError", err)
	}
	if !strings.Contains(refused.Reason, "id") {
		t.Errorf("refusal reason = %q, want it to mention id", refused.Reason)
	}
	if acceptCalled {
		t.Error("accept was called for a task that failed Validate")
	}
}

func TestProcessTaskRefusalReachesTheOfferer(t *testing.T) {
	dispatcher, target := net.Pipe()
	defer dispatcher.Close()
	defer target.Close()

	// ProcessTask's own return value isn't checked here — it's determined after
	// WriteVerdict, which is what unblocks Offer below, so asserting on it
	// in this goroutine would race with the test function returning.
	go func() {
		_, _ = ProcessTask(target, func(*Task) error {
			return errors.New("busy")
		})
	}()

	sent, err := New("noop", Requirements{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = Offer(dispatcher, sent)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Offer error = %v, want *RefusedError", err)
	}
	if !strings.Contains(refused.Reason, "busy") {
		t.Errorf("refusal reason = %q, want it to mention busy", refused.Reason)
	}
}

// header writes [version][length] the way WriteTask would, so tests can
// hand-build a body after it without going through json.Marshal.
func header(version byte, length uint32) []byte {
	hdr := make([]byte, 5)
	hdr[0] = version
	binary.BigEndian.PutUint32(hdr[1:5], length)
	return hdr
}

func TestReadTaskRejectsOversizedLength(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(header(taskWireVersion, maxTaskSize+1))

	if _, err := ReadTask(&buf); err == nil {
		t.Error("an oversized length was accepted")
	}
}

func TestReadTaskRejectsGarbledBody(t *testing.T) {
	var buf bytes.Buffer
	body := []byte("not json")
	buf.Write(header(taskWireVersion, uint32(len(body))))
	buf.Write(body)

	if _, err := ReadTask(&buf); err == nil {
		t.Error("garbled JSON was accepted")
	}
}

func TestReadTaskRejectsAWrongVersionBeforeReadingTheBody(t *testing.T) {
	var buf bytes.Buffer
	buf.Write(header(taskWireVersion+1, maxTaskSize*10)) // huge length, never read
	buf.WriteString("garbage that must never be touched")

	_, err := ReadTask(&buf)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

// A version mismatch is the one read failure ProcessTask can still answer
// cleanly — it knows exactly how many bytes it consumed to find out.
func TestProcessTaskRefusesAWrongVersionInsteadOfHanging(t *testing.T) {
	dispatcher, target := net.Pipe()
	defer dispatcher.Close()
	defer target.Close()

	go func() {
		_, _ = ProcessTask(target, func(*Task) error { return nil })
	}()

	// Hand-write a header with the wrong version, bypassing WriteTask. The
	// body is never read — ReadTask rejects before getting that far — so
	// this Write stays pending until the deferred Close unblocks it.
	go func() {
		_, _ = dispatcher.Write(append(header(taskWireVersion+1, 2), []byte("{}")...))
	}()

	err := ReadVerdict(dispatcher)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("ReadVerdict error = %v, want *RefusedError", err)
	}
	if !strings.Contains(refused.Reason, "version") {
		t.Errorf("refusal reason = %q, want it to mention version", refused.Reason)
	}
}
