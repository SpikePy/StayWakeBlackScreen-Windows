//go:build windows

// Command staywakeblackscreenidle is a background idle guard - unlike
// staywakeblackscreen, it does NOT block input or show the black screen
// immediately. It just prevents the PC from sleeping/locking and keeps
// running in the background. Only after idle_minutes (default 3, set via
// config.yaml in %LOCALAPPDATA%\StayWakeBlackScreen, or overridden with
// -idle-minutes) of no real keyboard/mouse activity does it show the
// black screen and block all input, exactly like staywakeblackscreen.
// Pressing Escape then dismisses the black screen and restores input, but
// the program itself keeps running - the idle countdown simply restarts,
// and it will black out again after another idle_minutes of inactivity,
// repeating indefinitely.
//
// A tray icon (black screen = guarding, light grey screen = disabled) lets
// the user pause/resume without stopping the process: left-click toggles
// it, right-click opens an Enable/Disable/Exit menu.
//
// This program does not exit on its own otherwise. To stop it: the tray
// menu's Exit, Task Manager, taskkill, or the installer (which does this
// automatically when updating).
//
// While blacked out: ALL keyboard and mouse input is blocked system-wide.
// Only Escape ends the black screen. Ctrl+Alt+Del always remains available
// as a hard escape hatch, since Windows never lets any hook suppress it.
//
// Logging is OFF by default. Pass -enable-logging to write diagnostics to
// StayWakeBlackScreenIdle.log next to the exe, for troubleshooting only.
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

	"stay-wake-black-screen/internal/blackout"
	"stay-wake-black-screen/internal/config"
	"stay-wake-black-screen/internal/singleinstance"
	"stay-wake-black-screen/internal/tray"
)

const (
	menuIDEnable  = 1
	menuIDDisable = 2
	menuIDExit    = 3
)

func main() {
	runtime.LockOSThread()

	cfg, cfgErr := config.Load()

	idleMinutes := flag.Int("idle-minutes", cfg.IdleMinutes, "minutes of inactivity before blacking out (overrides config.yaml)")
	heartbeatSeconds := flag.Int("heartbeat-seconds", cfg.HeartbeatSeconds, "seconds between Caps Lock activity heartbeats while blacked out (overrides config.yaml)")
	pollMs := flag.Int("poll-ms", cfg.PollMs, "milliseconds between idle/escape polls (overrides config.yaml)")
	startEnabled := flag.Bool("start-enabled", cfg.StartEnabled, "whether the idle guard is active on launch (overrides config.yaml)")
	enableLogging := flag.Bool("enable-logging", false, "write diagnostics to StayWakeBlackScreenIdle.log next to the exe")
	flag.Parse()

	logPath := ""
	if exe, err := os.Executable(); err == nil {
		logPath = filepath.Join(filepath.Dir(exe), "StayWakeBlackScreenIdle.log")
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
	if cfgErr != nil {
		logf("WARNING loading config.yaml (falling back to idle-minutes=%d): %v", config.DefaultIdleMinutes, cfgErr)
	}

	release, alreadyRunning, err := singleinstance.Acquire(`StayWakeBlackScreenIdle_SingleInstance`)
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
		heartbeatTimer uintptr // 0 when not blacked out
		fastTimer      uintptr
		cursorHidden   bool
		inputBlocked   bool
		blackedOut     bool
		enabled        = *startEnabled

		trayHwnd uintptr
		trayIcon uintptr
	)

	cleanup := func() {
		if inputBlocked {
			blackout.RemoveInputBlockHooks()
		}
		if cursorHidden {
			blackout.ShowCursorAgain()
		}
		blackout.StopTimer(heartbeatTimer)
		blackout.StopTimer(fastTimer)
		for _, h := range overlayWindows {
			blackout.DestroyOverlayWindow(h)
		}
		blackout.RestoreExecutionState()
		if blackout.IsCapsLockOn() {
			blackout.ToggleCapsLock()
		}
		tray.RemoveIcon(trayHwnd)
		tray.DestroyIconHandle(trayIcon)
		tray.DestroyWindow(trayHwnd)
		logf("Cleanup done. Log at: %s", logPath)
	}
	defer cleanup()
	defer func() {
		if r := recover(); r != nil {
			logf("PANIC: %v", r)
		}
	}()

	blackout.EnableDPIAwareness()
	// Block sleep AND tell Windows the display must stay on for the
	// entire lifetime of this program while enabled (not just during
	// blackout), so it never sees a display-off/idle transition that
	// could trigger a session lock.
	blackout.BlockSleep()
	if !enabled {
		logf("Starting disabled (start_enabled=false)")
		blackout.RestoreExecutionState()
	}

	enterBlackout := func() error {
		logf("Idle timeout reached - entering blackout")
		monitors, err := blackout.Monitors()
		if err != nil || len(monitors) == 0 {
			return fmt.Errorf("enumerating monitors: %w", err)
		}
		for _, m := range monitors {
			hwnd, err := blackout.CreateOverlayWindow(m)
			if err != nil {
				return fmt.Errorf("creating overlay window for %+v: %w", m, err)
			}
			overlayWindows = append(overlayWindows, hwnd)
		}
		for _, h := range overlayWindows {
			blackout.ShowOverlayWindow(h)
		}
		if len(overlayWindows) > 0 {
			blackout.FocusWindow(overlayWindows[0])
		}
		blackout.HideCursor()
		cursorHidden = true

		if err := blackout.InstallInputBlockHooks(); err != nil {
			return fmt.Errorf("installing input hooks: %w", err)
		}
		inputBlocked = true

		heartbeatMs := uint32(*heartbeatSeconds)
		if heartbeatMs < 1 {
			heartbeatMs = 1
		}
		heartbeatTimer, err = blackout.StartTimer(heartbeatMs * 1000)
		if err != nil {
			return fmt.Errorf("starting heartbeat timer: %w", err)
		}
		blackedOut = true
		return nil
	}

	var lastActivityTick int32

	exitBlackout := func() {
		logf("Exiting blackout, resuming idle watch")
		if inputBlocked {
			blackout.RemoveInputBlockHooks()
			inputBlocked = false
		}
		blackout.StopTimer(heartbeatTimer)
		heartbeatTimer = 0
		if blackout.IsCapsLockOn() {
			blackout.ToggleCapsLock()
		}
		if cursorHidden {
			blackout.ShowCursorAgain()
			cursorHidden = false
		}
		for _, h := range overlayWindows {
			blackout.DestroyOverlayWindow(h)
		}
		overlayWindows = nil
		// Restart the idle countdown fresh from now, rather than trusting
		// GetLastInputTick(): real input was swallowed by our own hooks
		// for the duration of the blackout, so the OS-reported value is
		// stale and would otherwise cause an immediate re-trigger.
		lastActivityTick = int32(blackout.GetTickCount())
		blackedOut = false
	}

	applyTrayIcon := func() {
		var (
			newIcon uintptr
			err     error
		)
		if enabled {
			newIcon, err = tray.EnabledIcon()
		} else {
			newIcon, err = tray.DisabledIcon()
		}
		if err != nil {
			logf("EXCEPTION building tray icon: %v", err)
			return
		}
		tooltip := "StayWakeBlackScreenIdle - guarding"
		if !enabled {
			tooltip = "StayWakeBlackScreenIdle - disabled"
		}
		if trayIcon == 0 {
			if err := tray.AddIcon(trayHwnd, newIcon, tooltip); err != nil {
				logf("EXCEPTION adding tray icon: %v", err)
			}
		} else {
			if err := tray.UpdateIcon(trayHwnd, newIcon, tooltip); err != nil {
				logf("EXCEPTION updating tray icon: %v", err)
			}
		}
		old := trayIcon
		trayIcon = newIcon
		tray.DestroyIconHandle(old)
	}

	setEnabled := func(v bool) {
		if enabled == v {
			return
		}
		enabled = v
		if enabled {
			logf("Enabled via tray")
			blackout.BlockSleep()
			// Avoid an immediate re-trigger from idle time that
			// accumulated while disabled.
			lastActivityTick = int32(blackout.GetTickCount())
		} else {
			logf("Disabled via tray")
			if blackedOut {
				exitBlackout()
			}
			blackout.RestoreExecutionState()
		}
		applyTrayIcon()
	}

	trayHwnd, err = tray.NewWindow(
		func() { setEnabled(!enabled) }, // left click: toggle
		func() { // right click: menu
			id := tray.ShowMenu(trayHwnd, []tray.MenuItem{
				{ID: menuIDEnable, Label: "Enable", Checked: enabled},
				{ID: menuIDDisable, Label: "Disable", Checked: !enabled},
				{},
				{ID: menuIDExit, Label: "Exit"},
			})
			switch id {
			case menuIDEnable:
				setEnabled(true)
			case menuIDDisable:
				setEnabled(false)
			case menuIDExit:
				blackout.PostQuitMessage()
			}
		},
	)
	if err != nil {
		logf("EXCEPTION creating tray window: %v", err)
		return
	}
	applyTrayIcon()

	idleThresholdMs := int32(*idleMinutes)
	if idleThresholdMs < 1 {
		idleThresholdMs = 1
	}
	idleThresholdMs *= 60000

	lastActivityTick = int32(blackout.GetTickCount())

	pollInterval := uint32(*pollMs)
	if pollInterval < 50 {
		pollInterval = 50
	}
	fastTimer, err = blackout.StartTimer(pollInterval)
	if err != nil {
		logf("EXCEPTION starting poll timer: %v", err)
		return
	}

	logf("Entering message loop (background idle guard, idleMinutes=%d)", *idleMinutes)
	for {
		m, ok := blackout.GetMessage()
		if !ok {
			break
		}
		if m.Hwnd != 0 || m.Message != blackout.WMTimer {
			blackout.Dispatch(m)
			continue
		}

		switch m.WParam {
		case fastTimer:
			if !enabled {
				continue
			}
			if blackedOut {
				if blackout.TakeEscapeRequested() {
					exitBlackout()
				}
				continue
			}
			osLast := int32(blackout.GetLastInputTick())
			if osLast-lastActivityTick > 0 {
				lastActivityTick = osLast
			}
			idleMs := int32(blackout.GetTickCount()) - lastActivityTick
			if idleMs >= idleThresholdMs {
				if err := enterBlackout(); err != nil {
					logf("EXCEPTION entering blackout: %v", err)
					return
				}
			}
		case heartbeatTimer:
			if blackedOut {
				blackout.ToggleCapsLock()
				time.Sleep(150 * time.Millisecond)
				blackout.ToggleCapsLock()
			}
		}
	}
	logf("Message loop returned (Exit or unexpected shutdown).")
}
