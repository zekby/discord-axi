package app

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReserveSlotSpacesConsecutiveRequests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pace")
	const interval = 200 * time.Millisecond

	if wait := reserveSlot(path, interval); wait != 0 {
		t.Fatalf("first request should go out immediately, waited %s", wait)
	}
	second := reserveSlot(path, interval)
	if second < interval {
		t.Fatalf("second request waits %s, want at least %s", second, interval)
	}
	third := reserveSlot(path, interval)
	if third < second+interval {
		t.Fatalf("third request waits %s, want at least %s", third, second+interval)
	}
}

// The burst an agent produces is the case that matters: every process must get
// its own slot rather than all of them reading the same timestamp.
func TestReserveSlotQueuesConcurrentCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pace")
	const interval = 100 * time.Millisecond
	const callers = 8

	waits := make([]time.Duration, callers)
	var group sync.WaitGroup
	for i := range waits {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			waits[index] = reserveSlot(path, interval)
		}(i)
	}
	group.Wait()

	longest := time.Duration(0)
	for _, wait := range waits {
		if wait > longest {
			longest = wait
		}
	}
	if want := time.Duration(callers-1) * interval; longest < want {
		t.Fatalf("last of %d concurrent callers waits %s, want at least %s", callers, longest, want)
	}
}

func TestPacingCanBeDisabled(t *testing.T) {
	t.Setenv(MinIntervalEnvVar, "0")
	if got := minInterval(); got != 0 {
		t.Fatalf("interval = %s, want 0", got)
	}

	t.Setenv(MinIntervalEnvVar, "not a number")
	if got := minInterval(); got != defaultMinInterval {
		t.Fatalf("interval = %s, want the default %s for unparseable input", got, defaultMinInterval)
	}
}

func TestReserveSlotIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pace")
	for i := 0; i < 40; i++ {
		if wait := reserveSlot(path, time.Second); wait > maxPacerWait {
			t.Fatalf("wait %s exceeds the %s cap", wait, maxPacerWait)
		}
	}
}
