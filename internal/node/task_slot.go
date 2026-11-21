package node

import "sync"

// whether this node is running a task right now
// kept only in memory, since no task survives a restart
type TaskSlot struct {
	mu   sync.Mutex
	busy bool
}

// marks the slot busy if it's free; false means another task got it first
// e.g. a second offer is then refused with "this node is already running a task"
func (ts *TaskSlot) TryClaim() bool {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.busy {
		return false
	}
	ts.busy = true
	return true
}

// frees the slot for the next task
func (ts *TaskSlot) Release() {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.busy = false
}
