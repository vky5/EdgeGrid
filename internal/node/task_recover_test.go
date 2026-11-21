package node

import (
	"testing"
	"time"
)

func TestCheckWaitDoublesUpToADay(t *testing.T) {
	cases := map[int]time.Duration{
		-1:  10 * time.Minute,
		0:   10 * time.Minute,
		1:   20 * time.Minute,
		2:   40 * time.Minute,
		3:   80 * time.Minute,
		4:   160 * time.Minute,
		5:   320 * time.Minute,
		6:   640 * time.Minute,
		7:   21*time.Hour + 20*time.Minute,
		8:   24 * time.Hour,
		9:   24 * time.Hour,
		500: 24 * time.Hour, // a task checked daily for over a year
	}
	for attempts, want := range cases {
		if got := checkWait(attempts); got != want {
			t.Errorf("checkWait(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestShouldGiveUpAfterSevenDays(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	cases := []struct {
		name    string
		started time.Time
		want    bool
	}{
		{"just sent", now, false},
		{"one minute short of 7 days", now.Add(-7*day + time.Minute), false},
		{"exactly 7 days", now.Add(-7 * day), true},
		{"8 days", now.Add(-8 * day), true},
		{"start time ahead of the clock", now.Add(time.Hour), false},
	}
	for _, c := range cases {
		if got := shouldGiveUp(c.started, now); got != c.want {
			t.Errorf("%s: shouldGiveUp = %v, want %v", c.name, got, c.want)
		}
	}
}
