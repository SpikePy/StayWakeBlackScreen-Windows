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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"windows-stay-wake-black-screen/internal/blackout"
	"windows-stay-wake-black-screen/internal/singleinstance"
)

func main() {
	// Win32 hooks and the message queue are bound to the OS thread that
	// installs/creates them; the Go runtime must never migrate this
	// goroutine to a different one mid-run.
	runtime.LockOSThread()

	heartbeatSeconds := flag.Int("heartbeat-seconds", 5, "seconds between Caps Lock activity heartbeats")
	enableLogging := flag.Bool("enable-logging", false, "write diagnostics to StayWakeBlackScreen.log next to the exe")
	flag.Parse()

	logPath := ""
	if exe, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(exe), "StayWakeBlackScreen.log")
	}
	logf := func(format string, args ...any) {
		if !*enableLogging || logPath == "" {
			return
		}
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintf(f, "%s  %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
	}

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

	var (
		overlayWindows []uintptr
		heartbeatTimer uintptr
		escapeTimer    uintptr
		cursorHidden   bool
		inputBlocked   bool
		exitReason     = "message loop returned without any handler logging a reason (unexpected)"
	)

	cleanup := func() {
		// Restore input FIRST, before anything else, so the user regains
		// control of their keyboard/mouse as soon as possible no matter
		// what else below might fail.
		if inputBlocked {
			blackout.RemoveInputBlockHooks()
		}
		if cursorHidden {
			blackout.ShowCursorAgain()
		}
		blackout.StopTimer(heartbeatTimer)
		blackout.StopTimer(escapeTimer)
		for _, h := range overlayWindows {
			blackout.DestroyOverlayWindow(h)
		}
		blackout.RestoreExecutionState()
		if blackout.IsCapsLockOn() {
			blackout.ToggleCapsLock()
		}
		logf("Cleanup done. Log at: %s", logPath)
	}
	defer cleanup()
	defer func() {
		if r := recover(); r != nil {
			logf("PANIC: %v", r)
		}
	}()

	blackout.EnableDPIAwareness()
	blackout.BlockSleep()

	monitors, err := blackout.Monitors()
	if err != nil || len(monitors) == 0 {
		logf("EXCEPTION enumerating monitors: %v", err)
		return
	}
	for _, m := range monitors {
		hwnd, err := blackout.CreateOverlayWindow(m)
		if err != nil {
			logf("EXCEPTION creating overlay window for %+v: %v", m, err)
			return
		}
		overlayWindows = append(overlayWindows, hwnd)
		logf("Created overlay window for bounds=%+v", m)
	}
	for _, h := range overlayWindows {
		blackout.ShowOverlayWindow(h)
	}
	if len(overlayWindows) > 0 {
		blackout.FocusWindow(overlayWindows[0])
	}
	blackout.HideCursor()
	cursorHidden = true

	// Blocks ALL keyboard/mouse input system-wide at the OS level -
	// nothing reaches any window, including this one's. Escape is
	// detected inside the hook itself and picked up by the escape-watch
	// timer below.
	if err := blackout.InstallInputBlockHooks(); err != nil {
		logf("EXCEPTION installing input hooks: %v", err)
		return
	}
	inputBlocked = true

	heartbeatMs := uint32(*heartbeatSeconds)
	if heartbeatMs < 1 {
		heartbeatMs = 1
	}
	heartbeatTimer, err = blackout.StartTimer(heartbeatMs * 1000)
	if err != nil {
		logf("EXCEPTION starting heartbeat timer: %v", err)
		return
	}
	escapeTimer, err = blackout.StartTimer(50)
	if err != nil {
		logf("EXCEPTION starting escape-watch timer: %v", err)
		return
	}

	logf("Entering message loop")
	for {
		m, ok := blackout.GetMessage()
		if !ok {
			break
		}
		if m.Hwnd == 0 && m.Message == blackout.WMTimer {
			switch m.WParam {
			case heartbeatTimer:
				blackout.ToggleCapsLock()
				time.Sleep(150 * time.Millisecond)
				blackout.ToggleCapsLock()
			case escapeTimer:
				if blackout.TakeEscapeRequested() {
					exitReason = "Escape pressed"
					blackout.PostQuitMessage()
				}
			}
			continue
		}
		blackout.Dispatch(m)
	}
	logf("Message loop returned. Reason: %s", exitReason)
}
