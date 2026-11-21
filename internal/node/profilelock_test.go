package node

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// a second EdgeGrid on the same profile is refused, and told who holds it
func TestSecondLockOnTheSameProfileIsRefused(t *testing.T) {
	dir := t.TempDir()
	first, err := lockProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.release()

	_, err = lockProfile(dir)
	if !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("second lock: %v, want ErrProfileInUse", err)
	}
	if want := "pid " + strconv.Itoa(os.Getpid()); !strings.Contains(err.Error(), want) {
		t.Errorf("message %q doesn't name the holder (%s)", err, want)
	}
}

// e.g. test-cluster and 2nd-cluster running side by side on one machine
func TestDifferentProfilesLockIndependently(t *testing.T) {
	a, err := lockProfile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer a.release()
	b, err := lockProfile(t.TempDir())
	if err != nil {
		t.Fatalf("second profile refused: %v", err)
	}
	b.release()
}

// after the first EdgeGrid closes, the profile can be used again; release twice is fine
func TestReleasedLockCanBeTakenAgain(t *testing.T) {
	dir := t.TempDir()
	l, err := lockProfile(dir)
	if err != nil {
		t.Fatal(err)
	}
	l.release()
	l.release()
	again, err := lockProfile(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again.release()
}

// a holder killed outright (kill -9, so no Close) still frees the profile
func TestKilledHolderFreesTheLock(t *testing.T) {
	dir := t.TempDir()
	holder := exec.Command(os.Args[0], "-test.run=^TestLockHolderProcess$")
	holder.Env = append(os.Environ(), "EDGEGRID_LOCK_HOLDER_DIR="+dir)
	out, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		holder.Process.Kill()
		t.Fatalf("holder said %q, want locked", line)
	}

	if _, err := lockProfile(dir); !errors.Is(err, ErrProfileInUse) {
		t.Fatalf("while held by another process: %v, want ErrProfileInUse", err)
	}
	holder.Process.Kill()
	holder.Wait()

	l, err := lockProfile(dir)
	if err != nil {
		t.Fatalf("after the holder was killed: %v", err)
	}
	l.release()
}

// not a real test: the other process for TestKilledHolderFreesTheLock, which takes the lock and waits
func TestLockHolderProcess(t *testing.T) {
	dir := os.Getenv("EDGEGRID_LOCK_HOLDER_DIR")
	if dir == "" {
		t.Skip("only runs as the helper process")
	}
	if _, err := lockProfile(dir); err != nil {
		os.Stdout.WriteString("error: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Stdout.WriteString("locked\n")
	select {}
}
