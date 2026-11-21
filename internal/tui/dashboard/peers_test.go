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

// with no Tailscale client the tab shows an error instead of crashing
// main.go passes nil when Node.LocalClient fails, rather than not starting the dashboard
func TestPeersModelWithNilClientDoesNotPanic(t *testing.T) {
	m := newPeersModel(nil, nil, nil)
	m = m.WithSize(80, 20)
	view := stripANSI(m.View())
	if !strings.Contains(view, "PEERS") {
		t.Errorf("expected the Peers pane title, got: %q", view)
	}
}
