package task

import (
	"errors"
	"fmt"
	"io"
)

// decides whether to take one offered task
// nil accepts; an error refuses, and its text goes to the sender as the reason
// e.g. "this node is already running a task"
type AcceptFunc func(t *Task) error

// reads an offered task, asks accept whether to take it, and answers yes or no
// the receiving side of Offer
//
//	what arrives                  what the sender hears                                  returns
//	version 2 task                no: "task: unsupported version: peer sent 2, …"        nil, that error
//	broken JSON or short read     nothing, the connection just closes                    nil, read error
//	task with no kind             no: "task: kind is empty"                              nil, that error
//	accept says busy              no: "this node is already running a task"              nil, "task: refused: this node is already running a task"
//	accept says nil               yes                                                    the task
func ProcessTask(rw io.ReadWriter, accept AcceptFunc) (*Task, error) {
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
