package task

import (
	"errors"
	"fmt"
	"io"
)

// AcceptFunc is a receiver's policy for one offered task. Returning nil
// accepts; an error refuses, its text sent to the dispatcher as the reason.
type AcceptFunc func(t *Task) error

// ProcessTask reads an offered task, asks accept whether to take it, and
// answers with a verdict. It is the mirror image of Offer.
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
