package node

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
)

// Direction says which way a transfer is moving relative to this node.
type Direction string

const (
	Outbound Direction = "sending"
	Inbound  Direction = "receiving"
)

// Transfer is a snapshot of one in-flight or just-finished transfer, safe to
// hand to the TUI. It is a copy — the live state is guarded by the registry.
type Transfer struct {
	ID        uint64
	Direction Direction
	Peer      string // hostname for outbound, self-reported node ID for inbound
	Progress  blob.Progress
	Started   time.Time
	Done      bool
	Err       error
}

// transfer is the live entry. Progress fields are updated from the transfer
// goroutine on every chunk, which is why they're atomics rather than plain
// ints under the registry's lock — the callback must never block on a lock
// the UI might be holding.
type transfer struct {
	id        uint64
	direction Direction
	peer      string
	started   time.Time

	chunksDone  atomic.Int64
	chunksTotal atomic.Int64
	bytesDone   atomic.Int64
	bytesTotal  atomic.Int64
	verifying   atomic.Bool

	done atomic.Bool
	mu   sync.Mutex // guards err only
	err  error
}

func (t *transfer) snapshot() Transfer {
	t.mu.Lock()
	err := t.err
	t.mu.Unlock()
	return Transfer{
		ID:        t.id,
		Direction: t.direction,
		Peer:      t.peer,
		Progress: blob.Progress{
			ChunksDone:  int(t.chunksDone.Load()),
			ChunksTotal: int(t.chunksTotal.Load()),
			BytesDone:   t.bytesDone.Load(),
			BytesTotal:  t.bytesTotal.Load(),
			Verifying:   t.verifying.Load(),
		},
		Started: t.started,
		Done:    t.done.Load(),
		Err:     err,
	}
}

// progressFunc returns the callback handed to blob.Send/Receive. Stores
// only, no locks — it runs once per chunk on the transfer goroutine.
func (t *transfer) progressFunc() blob.ProgressFunc {
	return func(p blob.Progress) {
		t.chunksDone.Store(int64(p.ChunksDone))
		t.chunksTotal.Store(int64(p.ChunksTotal))
		t.bytesDone.Store(p.BytesDone)
		t.bytesTotal.Store(p.BytesTotal)
		t.verifying.Store(p.Verifying)
	}
}

// rateMBps is n bytes over d in MB/s, for log lines. Zero for a zero-length
// interval rather than +Inf.
func rateMBps(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / (1 << 20) / d.Seconds()
}

// recordTransferStart writes a starting row to the history database. h may
// be nil (no database). ok is false when there is no row to finish later.
func recordTransferStart(h *db.Store, dir db.Direction, peerID, peerHostname, name string, size int64) (id int64, ok bool) {
	if h == nil {
		return 0, false
	}
	id, err := h.RecordStart(dir, peerID, peerHostname, name, size)
	if err != nil {
		log.Printf("blob: could not record transfer start: %v", err)
		return 0, false
	}
	return id, true
}

// recordTransferFinish writes the outcome for a row recordTransferStart
// made. refused gets its own status, distinct from a generic failure.
func recordTransferFinish(h *db.Store, id int64, ok bool, err error, refused bool, sha256 string) {
	if !ok {
		return
	}
	status, errText := db.StatusDone, ""
	if err != nil {
		errText = err.Error()
		status = db.StatusFailed
		if refused {
			status = db.StatusRefused
		}
	}
	if err := h.RecordFinish(id, status, errText, sha256); err != nil {
		log.Printf("blob: could not record transfer finish: %v", err)
	}
}

// finishedLinger is how long a completed transfer stays listed, so a
// transfer that succeeds in under a poll interval is still seen.
const finishedLinger = 8 * time.Second

// registry holds every transfer this node is running in either direction.
// Inbound ones have no other home: nothing else on this node knows a peer
// decided to send us something.
type registry struct {
	mu     sync.Mutex
	nextID uint64
	items  []*transfer
}

func (r *registry) start(dir Direction, peer string) *transfer {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	t := &transfer{id: r.nextID, direction: dir, peer: peer, started: time.Now()}
	r.items = append(r.items, t)
	return t
}

// finish marks t done and schedules its removal, so the list drains itself
// without the TUI having to dismiss anything.
func (r *registry) finish(t *transfer, err error) {
	t.mu.Lock()
	t.err = err
	t.mu.Unlock()
	t.done.Store(true)

	time.AfterFunc(finishedLinger, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, item := range r.items {
			if item == t {
				r.items = append(r.items[:i], r.items[i+1:]...)
				return
			}
		}
	})
}

func (r *registry) snapshot() []Transfer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Transfer, 0, len(r.items))
	for _, t := range r.items {
		out = append(out, t.snapshot())
	}
	return out
}

// Transfers lists what this node is sending and receiving right now, plus
// anything that finished in the last few seconds. Safe to call from the UI
// goroutine.
func (a *Node) Transfers() []Transfer { return a.transfers.snapshot() }
