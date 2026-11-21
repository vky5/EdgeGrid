package node

import (
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/edgegrid/edgegrid/internal/blob"
	"github.com/edgegrid/edgegrid/internal/db"
)

// how long the manifest and the yes-or-no answer may take, 15s, before any chunk moves
const verdictTimeout = 15 * time.Second

// how long a transfer may go with no chunk moving, 60s
// it restarts on every chunk, so only a stalled transfer hits it, not a slow one
const idleTimeout = 60 * time.Second

// moves the connection's deadline one idle period ahead, but never past the overall limit
func refreshDeadline(conn net.Conn, began time.Time, idle, ceiling time.Duration) {
	next := time.Now().Add(idle)
	if ceilingAt := began.Add(ceiling); next.After(ceilingAt) {
		next = ceilingAt
	}
	conn.SetDeadline(next) //nolint:errcheck // best-effort: a failed refresh just leaves the current deadline standing
}

// which way a transfer is going, from this node's side
type Direction string

const (
	Outbound Direction = "sending"
	Inbound  Direction = "receiving"
)

// a copy of one running or just-finished transfer, for the TUI to show
type Transfer struct {
	ID        uint64
	Direction Direction
	Peer      string // hostname for outbound, self-reported node ID for inbound
	Progress  blob.Progress
	Started   time.Time
	Done      bool
	Err       error
}

// one transfer as it runs; progress is updated after every chunk
// atomics, not the lock, so a chunk never waits on a lock the UI is holding
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

// the progress callback given to blob.Send and blob.Receive
// it takes no locks, since it runs after every chunk
func (t *transfer) progressFunc() blob.ProgressFunc {
	return func(p blob.Progress) {
		t.chunksDone.Store(int64(p.ChunksDone))
		t.chunksTotal.Store(int64(p.ChunksTotal))
		t.bytesDone.Store(p.BytesDone)
		t.bytesTotal.Store(p.BytesTotal)
		t.verifying.Store(p.Verifying)
	}
}

// speed in MB/s for log lines, e.g. 10 MB in 2s gives 5; 0 if no time passed
func rateMBps(n int64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return float64(n) / (1 << 20) / d.Seconds()
}

// adds a row for a transfer that just began; ok is false if there is no database or the write failed
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

// saves how a transfer ended, e.g. done, failed or refused
// a refusal gets its own status, apart from other failures
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

// how long a finished transfer stays on screen, 8s
// so a transfer quicker than one TUI refresh is still seen
const finishedLinger = 8 * time.Second

// every transfer running on this node, both ways
// nothing else on this node knows about incoming ones
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

// marks a transfer done and removes it a few seconds later, so the TUI never has to clear it
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

// what this node is sending and receiving now, plus anything that just finished
// safe to call from the TUI
func (a *Node) Transfers() []Transfer { return a.transfers.snapshot() }
