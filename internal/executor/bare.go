package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// how much of a run's printed output is held in memory, the last 1 MiB; earlier output is dropped
// so one noisy process can't eat all the memory, and the end of a log, where the result is, survives
const maxOutput = 1 << 20

// how much of a run's error output is kept for the error message, the last 4 KiB
// the end is kept because that's where a crash says what went wrong, e.g. a Python traceback's last line
const maxErrOutput = 4 << 10

// runs the task as a plain process, with this node's own permissions
type Bare struct{}

func (Bare) Name() RuntimeName { return RuntimeBare }

func (Bare) Run(ctx context.Context, workDir string, entrypoint []string, input []byte, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, entrypoint[0], entrypoint[1:]...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = bytes.NewReader(input)

	stdout, stderr := &tailBuffer{limit: maxOutput}, &tailBuffer{limit: maxErrOutput}
	cmd.Stdout, cmd.Stderr = stdout, stderr

	// or a child process that keeps the output open would hang the run after it is cancelled
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bare: %v: %w (stderr: %q)", entrypoint, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// keeps only the last limit bytes written, dropping older ones, and never fails a write
// so a chatty process is never killed for printing too much
// it holds up to twice the limit and trims back now and then, so most writes don't copy anything
//
//	limit 10, writes "hello " then "world, bye\n"   → Bytes() = "orld, bye\n"
//	limit 10, writes "hi\n"                          → Bytes() = "hi\n"
type tailBuffer struct {
	buf   []byte
	limit int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= t.limit {
		t.buf = append(t.buf[:0], p[len(p)-t.limit:]...)
		return n, nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.limit {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.limit:]...)
	}
	return n, nil
}

func (t *tailBuffer) Bytes() []byte {
	if len(t.buf) > t.limit {
		return t.buf[len(t.buf)-t.limit:]
	}
	return t.buf
}

func (t *tailBuffer) String() string { return string(t.Bytes()) }

var _ Runtime = Bare{}
