package node

import (
	"sync"
	"testing"
)

func TestTaskSlotSecondClaimFailsUntilReleased(t *testing.T) {
	var ts TaskSlot
	if !ts.TryClaim() {
		t.Fatal("first claim on a free slot was refused")
	}
	if ts.TryClaim() {
		t.Fatal("second claim succeeded while the slot was still busy")
	}
	ts.Release()
	if !ts.TryClaim() {
		t.Fatal("claim after Release was refused")
	}
}

func TestTaskSlotReleaseOnAFreeSlotDoesNotPanic(t *testing.T) {
	var ts TaskSlot
	ts.Release() // never claimed — must be a harmless no-op
	if !ts.TryClaim() {
		t.Fatal("claim after a no-op Release was refused")
	}
}

// The real point of TaskSlot: under concurrent claims, exactly one wins.
// This is what originally caught the check-before-lock race.
func TestTaskSlotExactlyOneConcurrentClaimWins(t *testing.T) {
	var ts TaskSlot
	var wg sync.WaitGroup
	wins := make(chan bool, 100)

	for range 100 {
		wg.Go(func() {
			wins <- ts.TryClaim()
		})
	}
	wg.Wait()
	close(wins)

	count := 0
	for w := range wins {
		if w {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d goroutines won the claim, want exactly 1", count)
	}
}
