package node

import (
	"net"
	"testing"
	"time"
)

// fakeDeadlineConn only records SetDeadline calls — refreshDeadline never
// touches Read/Write/Close, so nothing else needs implementing.
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

// Near the ceiling, refreshing must not push the deadline past it — that's
// the whole point of having a ceiling at all.
func TestRefreshDeadlineClampsToCeiling(t *testing.T) {
	c := &fakeDeadlineConn{}
	began := time.Now().Add(-59 * time.Second)
	refreshDeadline(c, began, time.Minute, time.Minute)

	want := began.Add(time.Minute)
	if diff := c.last.Sub(want); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Errorf("deadline = %v, want the ceiling ~%v", c.last, want)
	}
}
