package blob

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"report.pdf", "report.pdf"},
		{"../../.ssh/authorized_keys", "authorized_keys"},
		{`C:\Windows\System32\evil.dll`, "evil.dll"}, // backslashes split on Linux too
		{"/etc/passwd", "passwd"},
		{"a\x00b.txt", "ab.txt"},
		{"..", ""},
		{"...", ""},
		{"", ""},
		{".bashrc", "bashrc"}, // leading dot stripped: no hidden-file trick
		{`bad<>:"|?*name.txt`, "badname.txt"},
		{"nul.txt", "_nul.txt"},
		{"COM1", "_COM1"},
		{"movie (1).mkv", "movie (1).mkv"},
	}
	for _, c := range cases {
		if got := SafeName(c.in); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := SafeName(strings.Repeat("é", 200)) // 2 bytes each
	if len(long) > maxNameLen {
		t.Errorf("long name not capped: %d bytes", len(long))
	}
}

func TestValidateRejectsLyingManifests(t *testing.T) {
	good := func() *Manifest {
		return &Manifest{Size: 8, Chunks: []ChunkInfo{
			{Index: 0, Offset: 0, Length: 4},
			{Index: 1, Offset: 4, Length: 4},
		}}
	}
	if err := good().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	cases := map[string]func(*Manifest){
		"offset past Size": func(m *Manifest) { m.Chunks[1].Offset = 1 << 40 },
		"gap between":      func(m *Manifest) { m.Chunks[1].Offset = 6 },
		"sum != Size":      func(m *Manifest) { m.Size = 99 },
		"negative size":    func(m *Manifest) { m.Size = -1 },
		"wrong index":      func(m *Manifest) { m.Chunks[1].Index = 5 },
		"zero length":      func(m *Manifest) { m.Chunks[0].Length = 0 },
		"oversized chunk":  func(m *Manifest) { m.Chunks[0].Length = maxChunkSize + 1 },
		"duplicate index":  func(m *Manifest) { m.Chunks[1].Index = 0 },
	}
	for name, mutate := range cases {
		m := good()
		mutate(m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s: lying manifest was accepted", name)
		}
	}
}

func writeSource(t *testing.T, content string) (path string, m *Manifest) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := BuildManifest(path, "application/octet-stream", 4) // tiny chunks: several per file
	if err != nil {
		t.Fatal(err)
	}
	return path, m
}

// A whole transfer over an in-memory connection, verdict included.
func TestSendReceiveRoundTrip(t *testing.T) {
	content := "hello, chunked world!" // 21 bytes -> 6 chunks at size 4
	src, m := writeSource(t, content)
	dest := filepath.Join(t.TempDir(), "out.bin")

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	recvErr := make(chan error, 1)
	var got *Manifest
	go func() {
		var err error
		got, err = Receive(b, func(*Manifest) (string, error) { return dest, nil }, nil)
		recvErr <- err
	}()

	if err := Send(a, src, m, nil); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := <-recvErr; err != nil {
		t.Fatalf("Receive: %v", err)
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, []byte(content)) {
		t.Errorf("received %q, want %q", data, content)
	}
	if got.Name != "source.bin" {
		t.Errorf("Name = %q, want source.bin carried across", got.Name)
	}
}

// The point of the verdict: a refusal reaches the sender as a real error
// before any chunk moves, not as a broken pipe.
func TestRefusalReachesTheSender(t *testing.T) {
	src, m := writeSource(t, "some bytes to refuse")

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go Receive(b, func(*Manifest) (string, error) {
		return "", errors.New("this node is not accepting files from you")
	}, nil)

	err := Send(a, src, m, nil)
	var refused *RefusedError
	if !errors.As(err, &refused) {
		t.Fatalf("Send error = %v, want *RefusedError", err)
	}
	if !strings.Contains(refused.Reason, "not accepting") {
		t.Errorf("reason lost in transit: %q", refused.Reason)
	}
}

// A hostile manifest must be refused before Truncate or WriteAt can act on
// its lies — no file may be created.
func TestReceiveRefusesLyingManifestBeforeTouchingDisk(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "out.bin")

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	go Receive(b, func(*Manifest) (string, error) { return dest, nil }, nil)

	hostile := &Manifest{Size: 4, Chunks: []ChunkInfo{{Index: 0, Offset: 1 << 40, Length: 4}}}
	if err := WriteManifest(a, hostile); err != nil {
		t.Fatal(err)
	}
	var refused *RefusedError
	if err := ReadVerdict(a); !errors.As(err, &refused) {
		t.Fatalf("verdict = %v, want a refusal", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("a file was created for a manifest that should have been refused")
	}
}

func TestSendAndReceiveRejectCorruptedChunk(t *testing.T) {
	src, m := writeSource(t, "twelve bytes")
	dest := filepath.Join(t.TempDir(), "out.bin")

	// Tamper with the manifest's promised hash for chunk 1: the chunk that
	// actually arrives no longer matches what was "promised".
	m.Chunks[1].SHA256 = strings.Repeat("0", 64)

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	recvErr := make(chan error, 1)
	go func() {
		_, err := Receive(b, func(*Manifest) (string, error) { return dest, nil }, nil)
		recvErr <- err
	}()

	go Send(a, src, m, nil)

	err := <-recvErr
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Errorf("Receive error = %v, want a hash mismatch", err)
	}
}

// The sender never checks the ACL — it can't, the ACL lives in the
// receiver's profile. What protects the receiver is that it stops reading
// after a refusal, so a sender that ignores the verdict and pushes chunks
// anyway is writing into a connection nobody is draining.
func TestSenderIgnoringARefusalGainsNothing(t *testing.T) {
	_, m := writeSource(t, "these bytes must never land")

	a, b := net.Pipe()
	defer a.Close()

	recvErr := make(chan error, 1)
	go func() {
		_, err := Receive(b, func(*Manifest) (string, error) {
			return "", errors.New("this node is not accepting files from you")
		}, nil)
		b.Close() // what node.handlePeer's deferred conn.Close() does
		recvErr <- err
	}()

	if err := WriteManifest(a, m); err != nil {
		t.Fatal(err)
	}
	var refused *RefusedError
	if err := ReadVerdict(a); !errors.As(err, &refused) {
		t.Fatalf("verdict = %v, want a refusal", err)
	}

	// A hostile sender ignores that and streams a chunk regardless.
	if err := WriteChunk(a, 0, []byte("these bytes must never land")); err == nil {
		t.Error("a chunk was accepted by a receiver that had refused the transfer")
	}

	if err := <-recvErr; err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("Receive returned %v, want a refusal", err)
	}
}

// Once every chunk is in, the receiver still flushes and re-hashes the whole
// file. Progress has to say so, or the UI shows 100% and looks hung.
func TestReceiveReportsVerifyingAfterTheLastChunk(t *testing.T) {
	src, m := writeSource(t, "twelve bytes")
	dest := filepath.Join(t.TempDir(), "out.bin")

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	var events []Progress
	recvErr := make(chan error, 1)
	go func() {
		_, err := Receive(b, func(*Manifest) (string, error) { return dest, nil },
			func(p Progress) { events = append(events, p) })
		recvErr <- err
	}()

	if err := Send(a, src, m, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-recvErr; err != nil {
		t.Fatal(err)
	}

	last := events[len(events)-1]
	if !last.Verifying {
		t.Errorf("last progress event = %+v, want Verifying set", last)
	}
	if last.BytesDone != last.BytesTotal {
		t.Errorf("verifying reported before all bytes arrived: %+v", last)
	}
	for _, e := range events[:len(events)-1] {
		if e.Verifying {
			t.Errorf("Verifying set before the last chunk: %+v", e)
		}
	}
}
