package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bare(kind string, entrypoint ...string) Definition {
	return Definition{Kind: kind, Runtime: RuntimeBare, Entrypoint: entrypoint}
}

func newTestExecutor(t *testing.T, defs ...Definition) (*Executor, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "runs")
	e, err := New(defs, []Runtime{Bare{}}, root)
	if err != nil {
		t.Fatal(err)
	}
	return e, root
}

func TestRunReturnsStdout(t *testing.T) {
	e, _ := newTestExecutor(t, bare("hello", "echo", "hello world"))

	out, err := e.Run(context.Background(), "hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hello world\n" {
		t.Errorf("out = %q, want %q", out, "hello world\n")
	}
}

func TestRunFeedsInputOnStdin(t *testing.T) {
	e, _ := newTestExecutor(t, bare("echo", "cat"))

	out, err := e.Run(context.Background(), "echo", []byte(`{"x":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"x":1}` {
		t.Errorf("out = %q, want the input back", out)
	}
}

func TestRunReportsAFailingProcessWithItsStderr(t *testing.T) {
	e, _ := newTestExecutor(t, bare("fail", "sh", "-c", "echo boom >&2; exit 3"))

	_, err := e.Run(context.Background(), "fail", nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want a failure that includes stderr", err)
	}
}

func TestRunIsKilledWhenTheContextIsCancelled(t *testing.T) {
	e, _ := newTestExecutor(t, bare("sleep", "sleep", "30"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := e.Run(ctx, "sleep", nil)
	if err == nil {
		t.Fatal("a cancelled run reported success")
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("run took %v after cancellation, want it killed", took)
	}
}

func TestRunRejectsOutputOverTheCap(t *testing.T) {
	e, _ := newTestExecutor(t, bare("chatty", "head", "-c", "2000000", "/dev/zero"))

	_, err := e.Run(context.Background(), "chatty", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("err = %v, want an output-cap error", err)
	}
}

func TestRunCleansUpItsWorkDir(t *testing.T) {
	e, root := newTestExecutor(t, bare("touch", "touch", "leftover"))

	if _, err := e.Run(context.Background(), "touch", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d run dir(s) left behind", len(entries))
	}
}

func TestSupports(t *testing.T) {
	e, _ := newTestExecutor(t,
		bare("hello", "echo"),
		Definition{Kind: "needs-model", Runtime: RuntimeBare, Entrypoint: []string{"true"}, Artifacts: []Artifact{{URL: "x"}}},
		Definition{Kind: "in-container", Runtime: "container", Entrypoint: []string{"true"}},
	)

	cases := map[string]bool{
		"hello":        true,
		"needs-model":  false, // artifact fetching isn't built
		"in-container": false, // runtime not installed
		"missing":      false,
	}
	for kind, want := range cases {
		if got := e.Supports(kind); got != want {
			t.Errorf("Supports(%q) = %v, want %v", kind, got, want)
		}
	}
	if _, err := e.Run(context.Background(), "needs-model", nil); err == nil {
		t.Error("Run accepted a kind Supports rejects")
	}
}

func TestNewRejectsBadDefinitions(t *testing.T) {
	cases := map[string][]Definition{
		"empty kind":     {bare("", "true")},
		"no runtime":     {{Kind: "x", Entrypoint: []string{"true"}}},
		"no entrypoint":  {{Kind: "x", Runtime: RuntimeBare}},
		"duplicate kind": {bare("x", "true"), bare("x", "true")},
	}
	for name, defs := range cases {
		if _, err := New(defs, []Runtime{Bare{}}, t.TempDir()); err == nil {
			t.Errorf("New accepted a registry with %s", name)
		}
	}
}

func TestLoadDefinitions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	body := `[{"kind":"hello","runtime":"bare","entrypoint":["echo","hello world"]}]`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	defs, err := LoadDefinitions(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) != 1 || defs[0].Kind != "hello" || defs[0].Runtime != RuntimeBare || len(defs[0].Entrypoint) != 2 {
		t.Errorf("defs = %+v", defs)
	}

	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDefinitions(path); err == nil {
		t.Error("garbled registry was accepted")
	}
}
