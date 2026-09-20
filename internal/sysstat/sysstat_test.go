package sysstat

import "testing"

func TestClampKeepsValuesInRange(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-0.5, 0}, // counter wrap can go negative
		{0, 0},
		{0.42, 0.42},
		{1, 1},
		{2.5, 1}, // an oversubscribed load average exceeds 1
	}
	for _, c := range cases {
		if got := clamp(c.in); got != c.want {
			t.Errorf("clamp(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// Whatever the OS, both readings must land in 0..1 and never panic — the
// dashboard renders them straight into a bar with no further validation.
// A machine that can't report stats returns 0 rather than failing.
func TestUsageReadingsAreInRange(t *testing.T) {
	// CPU is sampled as a delta, so the first call only establishes a
	// baseline. Call twice, the way a polling caller does.
	CPUUsage()

	for _, tc := range []struct {
		name string
		fn   func() float64
	}{
		{"CPUUsage", CPUUsage},
		{"MemUsage", MemUsage},
	} {
		got := tc.fn()
		if got < 0 || got > 1 {
			t.Errorf("%s() = %v, outside 0..1", tc.name, got)
		}
	}
}
