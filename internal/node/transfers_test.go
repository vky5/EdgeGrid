package node

import (
	"net"
	"testing"
	"time"
)

// a fake connection that only records deadlines, the one thing refreshDeadline touches
type fakeDeadlineConn struct {
	net.Conn
	last time.Time
}

func (f *fakeDeadlineConn) SetDeadline(t time.Time) error {
	f.last = t
	return nil
}

func TestRefreshDeadlineUsesIdleWindowWhenFarFromCeiling(t *testing.T) {
	c := &fakeDeadlineConn{}
	began := time.Now()
	refreshDeadline(c, began, time.Second, time.Hour)

	want := time.Now().Add(time.Second)
	if diff := c.last.Sub(want); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Errorf("deadline = %v, want ~%v", c.last, want)
	}
}

// near the overall limit, a refresh must not push the deadline past it
func TestRefreshDeadlineClampsToCeiling(t *testing.T) {
	c := &fakeDeadlineConn{}
	began := time.Now().Add(-59 * time.Second)
	refreshDeadline(c, began, time.Minute, time.Minute)

	want := began.Add(time.Minute)
	if diff := c.last.Sub(want); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Errorf("deadline = %v, want the ceiling ~%v", c.last, want)
	}
}
