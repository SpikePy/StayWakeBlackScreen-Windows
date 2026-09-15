package blackout

import "testing"

func TestHeartbeatMs(t *testing.T) {
	tests := []struct {
		seconds int
		want    uint32
	}{
		{-1, 1000}, // used to wrap to ~49.7 days
		{0, 1000},
		{1, 1000},
		{5, 5000},
		{2147483, 2147483000},
		{2147484, 2147483000}, // capped at MaxTimerMs
		{1 << 40, 2147483000},
	}
	for _, tt := range tests {
		if got := HeartbeatMs(tt.seconds); got != tt.want {
			t.Errorf("HeartbeatMs(%d) = %d, want %d", tt.seconds, got, tt.want)
		}
	}
}

func TestPollMs(t *testing.T) {
	tests := []struct {
		ms   int
		want uint32
	}{
		{-1, 50}, // used to wrap to ~49.7 days
		{0, 50},
		{10, 50},
		{50, 50},
		{250, 250},
		{1 << 31, MaxTimerMs},
		{1 << 40, MaxTimerMs},
	}
	for _, tt := range tests {
		if got := PollMs(tt.ms); got != tt.want {
			t.Errorf("PollMs(%d) = %d, want %d", tt.ms, got, tt.want)
		}
	}
}

func TestIdleThresholdMs(t *testing.T) {
	tests := []struct {
		minutes int
		want    int32
	}{
		{-5, 60000},
		{0, 60000},
		{1, 60000},
		{3, 180000},
		{35791, 2147460000},
		{35792, 2147460000}, // used to overflow to a negative threshold
		{1 << 40, 2147460000},
	}
	for _, tt := range tests {
		got := IdleThresholdMs(tt.minutes)
		if got != tt.want {
			t.Errorf("IdleThresholdMs(%d) = %d, want %d", tt.minutes, got, tt.want)
		}
		if got <= 0 {
			t.Errorf("IdleThresholdMs(%d) = %d, must be positive or it blacks out immediately", tt.minutes, got)
		}
	}
}
