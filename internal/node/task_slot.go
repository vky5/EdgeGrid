package node

import "sync"

// TaskSlot is whether this node is currently running a task — in-memory
// only, since nothing survives a restart to be busy with anyway.
type TaskSlot struct {
	mu   sync.Mutex
	busy bool
}

// TryClaim atomically marks the slot busy if it was free. false means
// something else already claimed it first.
func (ts *TaskSlot) TryClaim() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.busy {
		return false
	}
	ts.busy = true
	return true
}

// Release frees the slot for the next task.
func (ts *TaskSlot) Release() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.busy = false
}
