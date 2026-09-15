package blackout

import "math"

// MaxTimerMs is USER_TIMER_MAXIMUM, the longest interval StartTimer
// accepts (~24.8 days).
const MaxTimerMs = 0x7FFFFFFF

// The helpers below turn user-supplied values (flags or config.yaml) into
// safe ones. They clamp before converting to a fixed-width int: a negative
// input would otherwise wrap to a huge value and effectively disable the
// feature, and an oversized one would overflow.

// HeartbeatMs converts a heartbeat interval in seconds to a StartTimer
// interval, clamped to [1s, MaxTimerMs].
func HeartbeatMs(seconds int) uint32 {
	return uint32(min(max(seconds, 1), MaxTimerMs/1000) * 1000)
}

// IdleThresholdMs converts an idle timeout in minutes to milliseconds,
// clamped to at least 1 minute and at most what fits in int32 (~24.8
// days). Idle time is measured as an int32 tick difference, so a larger
// threshold would wrap negative and trigger immediately.
func IdleThresholdMs(minutes int) int32 {
	return int32(min(max(minutes, 1), math.MaxInt32/60000) * 60000)
}
