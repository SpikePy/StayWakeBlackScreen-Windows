//go:build windows

package blackout

import (
	"errors"
	"fmt"

	"windows-stay-wake-black-screen/internal/win32"
)

// Session is one active blackout: a black overlay window on every
// monitor, the cursor hidden, all keyboard and mouse input blocked, and
// the Caps Lock heartbeat running. Escape presses arrive in the starting
// thread's message loop as WMEscapePressed.
//
// A Session and its message loop must stay on the OS thread that started
// it (see runtime.LockOSThread): its hooks and timers belong to that
// thread.
type Session struct {
	windows      []uintptr
	cursorHidden bool
	hooked       bool
	heartbeat    *heartbeat
}

// Start blacks out every screen and blocks all input. heartbeatMs is the
// interval between Caps Lock pulses (see HeartbeatMs), and logf receives
// diagnostics. If Start fails partway, the returned Session holds
// whatever it had already set up, so callers must still call End.
func Start(heartbeatMs uint32, logf func(format string, args ...any)) (*Session, error) {
	s := &Session{}

	mons, err := monitors()
	if err != nil {
		return s, fmt.Errorf("enumerating monitors: %w", err)
	}
	if len(mons) == 0 {
		return s, errors.New("enumerating monitors: none found")
	}
	for _, m := range mons {
		hwnd, err := createOverlayWindow(m)
		if err != nil {
			return s, fmt.Errorf("creating overlay window for %+v: %w", m, err)
		}
		s.windows = append(s.windows, hwnd)
		logf("Created overlay window for bounds=%+v", m)
	}
	for _, h := range s.windows {
		procShowWindow.Call(h, swShow)
	}
	win32.SetForegroundWindow(s.windows[0])
	procShowCursor.Call(0)
	s.cursorHidden = true

	if err := installInputBlockHooks(); err != nil {
		return s, fmt.Errorf("installing input hooks: %w", err)
	}
	s.hooked = true

	if s.heartbeat, err = startHeartbeat(heartbeatMs); err != nil {
		return s, fmt.Errorf("starting heartbeat timer: %w", err)
	}
	return s, nil
}

// HandleTimer processes a WM_TIMER message's id if it belongs to s. Safe
// to call on a nil Session and with any id.
func (s *Session) HandleTimer(id uintptr) {
	if s != nil {
		s.heartbeat.handleTimer(id)
	}
}

// End undoes Start, restoring input first so the user regains control of
// their keyboard and mouse even if anything after that goes wrong. It
// also switches Caps Lock off if it's on. Safe to call on a nil or
// partially started Session, and more than once.
func (s *Session) End() {
	if s == nil {
		return
	}
	if s.hooked {
		removeInputBlockHooks()
		s.hooked = false
	}
	if s.cursorHidden {
		procShowCursor.Call(1)
		s.cursorHidden = false
	}
	s.heartbeat.stop()
	s.heartbeat = nil
	for _, h := range s.windows {
		win32.DestroyWindow(h)
	}
	s.windows = nil
	if IsCapsLockOn() {
		ToggleCapsLock()
	}
}

// pulseMs is how long Caps Lock stays flipped during one heartbeat pulse.
const pulseMs = 150

// heartbeat periodically pulses Caps Lock - flip it, then flip it back
// pulseMs later - as an activity signal while the screen is blacked out.
// The gap is timed with a second timer rather than a sleep so the thread
// keeps pumping messages: the input hooks run on this same thread, and
// Windows silently removes a low-level hook that doesn't respond in time.
type heartbeat struct {
	every   uintptr // periodic timer
	restore uintptr // one-shot timer ending the pulse in flight; 0 if none
}

func startHeartbeat(intervalMs uint32) (*heartbeat, error) {
	every, err := StartTimer(intervalMs)
	if err != nil {
		return nil, err
	}
	return &heartbeat{every: every}, nil
}

func (h *heartbeat) handleTimer(id uintptr) {
	switch {
	case h == nil || id == 0:
	case id == h.every && h.restore == 0:
		ToggleCapsLock()
		var err error
		if h.restore, err = StartTimer(pulseMs); err != nil {
			ToggleCapsLock() // can't time the pulse, so end it now rather than leave Caps Lock flipped
		}
	case id == h.restore:
		h.endPulse()
	}
}

// endPulse flips Caps Lock back, finishing the pulse in flight, if any.
func (h *heartbeat) endPulse() {
	if h.restore != 0 {
		StopTimer(h.restore)
		h.restore = 0
		ToggleCapsLock()
	}
}

// stop stops the heartbeat, finishing any pulse in flight so Caps Lock is
// left as it was found. Safe to call on nil.
func (h *heartbeat) stop() {
	if h == nil {
		return
	}
	StopTimer(h.every)
	h.every = 0
	h.endPulse()
}
