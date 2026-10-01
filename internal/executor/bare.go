package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// maxOutput caps what a run can return — output is a small result, not bulk
// data, and an unbounded buffer lets one chatty process exhaust memory.
const maxOutput = 1 << 20

// Bare runs the entrypoint as a plain process, with this node's own privileges.
type Bare struct{}

func (Bare) Name() RuntimeName { return RuntimeBare }

func (Bare) Run(ctx context.Context, workDir string, entrypoint []string, input []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, entrypoint[0], entrypoint[1:]...)
	cmd.Dir = workDir
	cmd.Stdin = bytes.NewReader(input)

	var stdout, stderr cappedBuffer
	stdout.limit, stderr.limit = maxOutput, 4<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	// Without WaitDelay, a grandchild still holding the pipes open keeps Wait
	// blocked even after ctx cancellation kills the direct child.
	cmd.WaitDelay = 5 * time.Second

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("bare: %v: %w (stderr: %q)", entrypoint, err, stderr.String())
	}
	if stdout.overflow {
		return nil, fmt.Errorf("bare: %v: output exceeded %d bytes", entrypoint, maxOutput)
	}
	return stdout.Bytes(), nil
}

// cappedBuffer keeps the first limit bytes and drops the rest, still reporting
// success so the process isn't killed by a write error mid-run. buf is a named
// field, not embedded: embedding would promote bytes.Buffer.ReadFrom, which
// io.Copy prefers over Write — bypassing the cap entirely.
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
