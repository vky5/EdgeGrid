package node

import (
	"errors"
	"strings"
	"testing"
)

// withFreeSpace fakes the free-space lookup, restoring the real one afterwards.
func withFreeSpace(t *testing.T, free uint64) {
	t.Helper()
	old := freeBytesFunc
	freeBytesFunc = func(string) (uint64, error) { return free, nil }
	t.Cleanup(func() { freeBytesFunc = old })
}

func TestCheckAcceptRefusesAFileThatWontFit(t *testing.T) {
	n := nodeWithDir(t)
	_ = n.SetTrust("friend", "friend", true)

	withFreeSpace(t, 1<<30)                                     // 1 GiB free
	err := n.checkAccept("friend", "friend", manifestOf(2<<30)) // a 2 GiB file
	if err == nil {
		t.Fatal("a 2 GiB file was accepted with 1 GiB free")
	}
	// The sender sees this text, so it has to carry the numbers.
	for _, want := range []string{"not enough free space", "2.0 GiB", "1.0 GiB"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q is missing %q", err, want)
		}
	}
}

// Free space equal to file plus headroom is enough; one byte less isn't.
func TestCheckAcceptSpaceBoundary(t *testing.T) {
	n := nodeWithDir(t)
	_ = n.SetTrust("friend", "friend", true)
	const size = 1 << 30

	withFreeSpace(t, size+spaceHeadroom)
	if err := n.checkAccept("friend", "friend", manifestOf(size)); err != nil {
		t.Errorf("a file that exactly fits was refused: %v", err)
	}

	withFreeSpace(t, size+spaceHeadroom-1)
	if err := n.checkAccept("friend", "friend", manifestOf(size)); err == nil {
		t.Error("a file one byte short of fitting was accepted")
	}
}

// A failed lookup must not refuse every transfer.
func TestCheckSpaceAllowsTheTransferWhenFreeSpaceCantBeRead(t *testing.T) {
	n := nodeWithDir(t)
	_ = n.SetTrust("friend", "friend", true)

	old := freeBytesFunc
	freeBytesFunc = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	t.Cleanup(func() { freeBytesFunc = old })

	if err := n.checkAccept("friend", "friend", manifestOf(1<<30)); err != nil {
		t.Errorf("a failed free-space lookup refused the transfer: %v", err)
	}
}

// The real implementation for this OS, not the stub.
func TestFreeBytesReadsTheRealFilesystem(t *testing.T) {
	free, err := freeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("freeBytes: %v", err)
	}
	if free == 0 {
		t.Error("freeBytes reported 0 for a directory we just created")
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		n    uint64
		want string
	}{
		{512, "512 B"},
		{1 << 10, "1.0 KiB"},
		{256 << 20, "256.0 MiB"},
		{3 << 30, "3.0 GiB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.n); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
