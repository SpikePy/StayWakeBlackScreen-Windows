//go:build windows

// Command staywakeblackscreen blacks out every screen and blocks ALL
// keyboard and mouse input system-wide (via low-level WH_KEYBOARD_LL/
// WH_MOUSE_LL hooks) the moment it runs - nothing reaches any window,
// including this one. Only pressing Escape ends the black screen and
// exits. Windows never lets any hook suppress Ctrl+Alt+Del, so that
// combination always remains available as a hard escape hatch regardless
// of anything going wrong here.
//
// This does NOT power off the monitor. Turning a display off via
// SC_MONITORPOWER makes Windows treat that as an idle/wake transition and
// can lock the session (especially with "require sign-in on wake" enabled)
// even though SetThreadExecutionState is blocking sleep. Instead, this
// covers every screen with a real black window and keeps the display
// explicitly "required" (on), so Windows never sees a display-off event
// and has no reason to lock.
//
// Logging is OFF by default (no file, no console output, no popups at
// all). Pass -enable-logging to write diagnostics to
// StayWakeBlackScreen.log next to the exe, for troubleshooting only.
//
// Built with -ldflags "-H=windowsgui" so it never shows a console window;
// everything below runs inside a top-level recover() that never lets a
// panic surface as a crash dialog - diagnostics go only to the log file.
package main

import (
	"flag"
	"runtime"

	"windows-stay-wake-black-screen/internal/applog"
	"windows-stay-wake-black-screen/internal/blackout"
	"windows-stay-wake-black-screen/internal/singleinstance"
)

func main() {
	// Win32 hooks, timers and the message queue are bound to the OS thread
	// that creates them; the Go runtime must never migrate this goroutine
	// to a different one mid-run.
	runtime.LockOSThread()

	heartbeatSeconds := flag.Int("heartbeat-seconds", 5, "seconds between Caps Lock activity heartbeats")
	enableLogging := flag.Bool("enable-logging", false, "write diagnostics to StayWakeBlackScreen.log next to the exe")
	flag.Parse()

	logf, logPath := applog.New("StayWakeBlackScreen.log", *enableLogging)

	release, alreadyRunning, err := singleinstance.Acquire(`StayWakeBlackScreen_SingleInstance`)
	if err != nil {
		logf("EXCEPTION acquiring single-instance mutex: %v", err)
		return
	}
	if alreadyRunning {
		logf("Another instance is already running - exiting.")
		return
	}
	defer release()

	var session *blackout.Session
	exitReason := "message loop returned without any handler logging a reason (unexpected)"

	defer func() {
		// Session.End restores input first, so the user regains control of
		// their keyboard and mouse as soon as possible no matter what else
		// might fail.
		session.End()
		blackout.RestoreExecutionState()
		logf("Cleanup done. Log at: %s", logPath)
	}()
	defer func() {
		if r := recover(); r != nil {
			logf("PANIC: %v", r)
		}
	}()

	blackout.EnableDPIAwareness()
	blackout.BlockSleep()

	// Blacks out every screen and blocks ALL keyboard/mouse input
	// system-wide at the OS level - nothing reaches any window, including
	// this one's. Escape presses arrive below as WMEscapePressed.
	session, err = blackout.Start(blackout.HeartbeatMs(*heartbeatSeconds), logf)
	if err != nil {
		logf("EXCEPTION %v", err)
		return
	}

	logf("Entering message loop")
	for {
		m, ok := blackout.GetMessage()
		if !ok {
			break
		}
		switch {
		case m.Hwnd == 0 && m.Message == blackout.WMTimer:
			session.HandleTimer(m.WParam)
		case m.Hwnd == 0 && m.Message == blackout.WMEscapePressed:
			exitReason = "Escape pressed"
			blackout.PostQuitMessage()
		default:
			blackout.Dispatch(m)
		}
	}
	logf("Message loop returned. Reason: %s", exitReason)
}
