package blackout

import (
	"math"
	"testing"
)

const minute = 60_000

func TestIdleTrackerCountsDownFromStart(t *testing.T) {
	tr := NewIdleTracker(3*minute, 1000)
	// Input from before the tracker started (e.g. before the program ran)
	// doesn't count.
	const oldInput = 500

	if got := tr.Remaining(1000, oldInput); got != 3*minute {
		t.Errorf("at start: Remaining = %d, want %d", got, 3*minute)
	}
	if got := tr.Remaining(1000+minute, oldInput); got != 2*minute {
		t.Errorf("after 1 minute: Remaining = %d, want %d", got, 2*minute)
	}
	if got := tr.Remaining(1000+3*minute, oldInput); got > 0 {
		t.Errorf("after 3 minutes: Remaining = %d, want <= 0", got)
	}
}

func TestIdleTrackerInputPushesDeadlineBack(t *testing.T) {
	tr := NewIdleTracker(3*minute, 0)
	// Input at 2 minutes means only 2 minutes of idle time at 4 minutes.
	if got := tr.Remaining(4*minute, 2*minute); got != minute {
		t.Errorf("Remaining = %d, want %d", got, minute)
	}
}

func TestIdleTrackerResetIgnoresStaleInput(t *testing.T) {
	tr := NewIdleTracker(3*minute, 0)
	tr.Reset(10 * minute)
	// After a blackout the OS's last-input tick predates the reset.
	if got := tr.Remaining(10*minute+1000, 5*minute); got != 3*minute-1000 {
		t.Errorf("Remaining = %d, want %d", got, 3*minute-1000)
	}
}

func TestIdleTrackerAcrossTickWraparound(t *testing.T) {
	start := uint32(math.MaxUint32 - 30_000) // 30s before the counter wraps
	tr := NewIdleTracker(minute, start)

	now := start + 45_000
	if now > start {
		t.Fatal("test setup: expected now to have wrapped past zero")
	}
	if got := tr.Remaining(now, start-5000); got != 15_000 {
		t.Errorf("45s after start, across the wrap: Remaining = %d, want 15000", got)
	}
	// Input just after the wrap is a small number but still newer.
	if got := tr.Remaining(now+10_000, now); got != minute-10_000 {
		t.Errorf("10s after post-wrap input: Remaining = %d, want %d", got, minute-10_000)
	}
}
