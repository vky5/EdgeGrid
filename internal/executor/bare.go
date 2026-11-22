package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// the most output a run may print, 1 MiB; more is an error
// output is a small result, so one noisy process can't eat all the memory
const maxOutput = 1 << 20

// runs the task as a plain process, with this node's own permissions
type Bare struct{}

func (Bare) Name() RuntimeName { return RuntimeBare }

func (Bare) Run(ctx context.Context, workDir string, entrypoint []string, input []byte, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, entrypoint[0], entrypoint[1:]...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = maxOutput, 4<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	// or a child process that keeps the output open would hang the run after it is cancelled
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bare: %v: %w (stderr: %q)", entrypoint, err, stderr.String())
	}
	if stdout.overflow {
		return nil, fmt.Errorf("bare: %v: output exceeded %d bytes", entrypoint, maxOutput)
	}
	return stdout.Bytes(), nil
}

// keeps the first limit bytes and quietly drops the rest, so the process isn't killed by a write error
// buf isn't embedded, or io.Copy would use bytes.Buffer.ReadFrom and write past the limit
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room < len(p) {
		c.overflow = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) Bytes() []byte  { return c.buf.Bytes() }
func (c *cappedBuffer) String() string { return c.buf.String() }

var _ Runtime = Bare{}
