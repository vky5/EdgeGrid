package dashboard

import (
	"strings"
	"testing"
	"time"
)

func TestTimeAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		t    time.Time
		want string
	}{
		{time.Time{}, "never"},
		{now.Add(-10 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-3 * time.Hour), "3h ago"},
		{now.Add(-48 * time.Hour), "2d ago"},
	}
	for _, c := range cases {
		if got := timeAgo(c.t); got != c.want {
			t.Errorf("timeAgo(%v) = %q, want %q", c.t, got, c.want)
		}
	}
}

// A nil local.Client (tsnet's local API unavailable) must render an error
// state, not panic — cmd/edgegrid/main.go passes nil here when
// Node.LocalClient fails, rather than refusing to start the dashboard.
func TestPeersModelWithNilClientDoesNotPanic(t *testing.T) {
	m := newPeersModel(nil, nil)
	m = m.WithSize(80, 20)
	view := stripANSI(m.View())
	if !strings.Contains(view, "PEERS") {
		t.Errorf("expected the Peers pane title, got: %q", view)
	}
}
