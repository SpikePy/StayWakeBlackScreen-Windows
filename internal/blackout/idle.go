package blackout

// IdleTracker decides when the PC has been idle long enough to black out.
// It works on GetTickCount-style millisecond ticks and compares them as
// int32 differences, so it stays correct across the tick counter's
// ~49.7-day wraparound. It has no OS dependency, so it's tested anywhere.
type IdleTracker struct {
	thresholdMs  int32
	lastActivity uint32
}

// NewIdleTracker returns a tracker whose countdown starts at now.
// thresholdMs should come from IdleThresholdMs.
func NewIdleTracker(thresholdMs int32, now uint32) *IdleTracker {
	return &IdleTracker{thresholdMs: thresholdMs, lastActivity: now}
}

// Reset restarts the countdown from now, disregarding any earlier input.
// Needed after a blackout (the input hooks swallowed real input, so the
// OS's last-input tick is stale and would re-trigger at once) and when
// re-enabling (idle time that built up while disabled mustn't count).
func (t *IdleTracker) Reset(now uint32) { t.lastActivity = now }

// Remaining reports how many milliseconds are left until the idle
// threshold is reached, given the current tick and the OS's last-input
// tick. Zero or less means it has been reached.
func (t *IdleTracker) Remaining(now, lastInput uint32) int32 {
	if int32(lastInput-t.lastActivity) > 0 {
		t.lastActivity = lastInput
	}
	idle := max(int32(now-t.lastActivity), 0)
	return t.thresholdMs - idle
}
