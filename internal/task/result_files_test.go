package task

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/edgegrid/edgegrid/internal/blob"
)

// writes files into a fresh folder and lists them the way the executor's row would
func resultDir(t *testing.T, contents map[string]string) (string, []ResultFile) {
	t.Helper()
	dir := t.TempDir()
	var files []ResultFile
	for _, name := range []string{"model.pt", "optimizer.pt", "metrics.json", "train.log", "confusion.png"} {
		body, ok := contents[name]
		if !ok {
			continue
		}
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := blob.BuildManifest(path, resultMediaType, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, ResultFile{Name: name, Size: m.Size, SHA256: m.SHA256})
	}
	return dir, files
}

var fiveFiles = map[string]string{
	"model.pt":      "weights",
	"optimizer.pt":  "momentum",
	"metrics.json":  `{"loss":0.021}`,
	"train.log":     "epoch 1\nepoch 2\n",
	"confusion.png": "png bytes",
}

// runs ServeResult for a done task with files on one end of a pipe, and returns the asker's end
func serveDone(t *testing.T, dir string, files []ResultFile) (client interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}, errc chan error) {
	t.Helper()
	client, server := pipe(t)
	errc = make(chan error, 1)
	go func() {
		errc <- ServeResult(server, func(string) (ResultResponse, string) {
			return ResultResponse{Status: StatusDone, Output: []byte("epoch 10 loss 0.021\n"), Files: files}, dir
		}, nil)
		server.Close()
	}()
	return client, errc
}

func TestFetchesOnlyTheWantedFiles(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	client, errc := serveDone(t, srcDir, files)

	resp, err := AskResult(client, uuid.NewString())
	if err != nil || len(resp.Files) != 5 {
		t.Fatalf("resp %+v, err %v", resp, err)
	}
	dest := t.TempDir()
	want := []string{"model.pt", "metrics.json", "confusion.png"}
	if err := FetchResultFiles(client, resp.Files, want, dest, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("serve: %v", err)
	}

	entries, _ := os.ReadDir(dest)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, ",") != "confusion.png,metrics.json,model.pt" {
		t.Errorf("saved %v, want only the 3 wanted files and no .part", got)
	}
	for _, name := range want {
		b, _ := os.ReadFile(filepath.Join(dest, name))
		if string(b) != fiveFiles[name] {
			t.Errorf("%s = %q, want %q", name, b, fiveFiles[name])
		}
	}
}

func TestEmptyWantSendsNothing(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	client, errc := serveDone(t, srcDir, files)

	resp, _ := AskResult(client, uuid.NewString())
	dest := t.TempDir()
	if err := FetchResultFiles(client, resp.Files, nil, dest, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("serve: %v", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("%d file(s) saved, want none", len(entries))
	}
}

func TestAskerThatHangsUpAfterTheAnswerIsFine(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	client, errc := serveDone(t, srcDir, files)

	if _, err := AskResult(client, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	client.Close() // an asker from before files, or one that wants nothing
	if err := <-errc; err != nil {
		t.Errorf("serve = %v, want nil", err)
	}
}

func TestServeRefusesANameThatIsNotListed(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	client, errc := serveDone(t, srcDir, files)

	AskResult(client, uuid.NewString())
	// a hand-made want, since FetchResultFiles won't send this
	writeResultFrame(client, FileRequest{Want: []string{"../../tsnet/tailscaled.state"}})
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "not one of this task's files") {
		t.Errorf("serve = %v, want a refusal", err)
	}
}

func TestFileChangedAfterTheRunIsRefused(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	os.WriteFile(filepath.Join(srcDir, "model.pt"), []byte("different weights"), 0o600)
	client, errc := serveDone(t, srcDir, files)

	resp, _ := AskResult(client, uuid.NewString())
	dest := t.TempDir()
	err := FetchResultFiles(client, resp.Files, []string{"model.pt"}, dest, nil)
	if err == nil || !strings.Contains(err.Error(), "doesn't match the listed file") {
		t.Errorf("fetch = %v, want a mismatch", err)
	}
	if serveErr := <-errc; serveErr == nil {
		t.Error("serve didn't hear the refusal")
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Errorf("%d file(s) left in dest, want none", len(entries))
	}
}

func TestServeRefusesALinkPutThereAfterTheRun(t *testing.T) {
	srcDir, files := resultDir(t, fiveFiles)
	os.Remove(filepath.Join(srcDir, "train.log"))
	if err := os.Symlink("/etc/hostname", filepath.Join(srcDir, "train.log")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	client, errc := serveDone(t, srcDir, files)

	resp, _ := AskResult(client, uuid.NewString())
	if err := FetchResultFiles(client, resp.Files, []string{"train.log"}, t.TempDir(), nil); err == nil {
		t.Error("fetch succeeded through a link")
	}
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "not a plain file") {
		t.Errorf("serve = %v, want not a plain file", err)
	}
}

func TestFetchRejectsAnUnsafeNameBeforeAsking(t *testing.T) {
	files := []ResultFile{{Name: "../evil.sh", Size: 1, SHA256: "aa"}}
	client, _ := pipe(t) // nothing should be written to it
	if err := FetchResultFiles(client, files, []string{"../evil.sh"}, t.TempDir(), nil); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("err = %v, want unsafe name", err)
	}
}

func TestCheckWant(t *testing.T) {
	listed := []ResultFile{{Name: "model.pt"}, {Name: "metrics.json"}}
	cases := map[string]struct {
		want []string
		ok   bool
	}{
		"any order":   {[]string{"metrics.json", "model.pt"}, true},
		"empty":       {nil, true},
		"twice":       {[]string{"model.pt", "model.pt"}, false},
		"not listed":  {[]string{"secret.txt"}, false},
		"path escape": {[]string{"../../tsnet/tailscaled.state"}, false},
	}
	for name, c := range cases {
		if _, err := checkWant(listed, c.want); (err == nil) != c.ok {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

// a want with no ask before it is read as the ask: no task ID, so lost and no files
func TestWantAsTheFirstMessageGetsNoFiles(t *testing.T) {
	client, server := pipe(t)
	go ServeResult(server, func(string) (ResultResponse, string) {
		t.Error("lookup called for a message with no task ID")
		return ResultResponse{}, ""
	}, nil)

	if err := writeResultFrame(client, FileRequest{Want: []string{"model.pt"}}); err != nil {
		t.Fatal(err)
	}
	var resp ResultResponse
	if err := readResultFrame(client, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusLost || resp.Error != NoRecord || len(resp.Files) != 0 {
		t.Errorf("resp = %+v, want lost with no record and no files", resp)
	}
}
