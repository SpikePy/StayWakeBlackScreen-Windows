//go:build windows

package main

import (
	"fmt"

	"windows-stay-wake-black-screen/internal/blackout"
	"windows-stay-wake-black-screen/internal/config"
	"windows-stay-wake-black-screen/internal/tray"
)

const (
	menuIDEnable    = 1
	menuIDDisable   = 2
	menuIDConfigure = 3
	menuIDExit      = 4
)

// guard is the idle guard's state and behaviour. All its methods run on
// the main thread: either directly from run's message loop, or from the
// tray window's click callbacks, which that same loop dispatches.
//
// Nothing polls. While enabled and not blacked out, a single one-shot
// timer is armed for the moment the idle threshold would be reached;
// while blacked out, the input hook posts Escape straight to the loop.
type guard struct {
	logf        func(format string, args ...any)
	heartbeatMs uint32

	enabled   bool
	idle      *blackout.IdleTracker
	idleTimer uintptr           // 0 while disabled or blacked out
	session   *blackout.Session // non-nil only while blacked out

	trayHwnd uintptr
	trayIcon uintptr
}

func newGuard(logf func(format string, args ...any), idleThresholdMs int32, heartbeatMs uint32, enabled bool) *guard {
	return &guard{
		logf:        logf,
		heartbeatMs: heartbeatMs,
		enabled:     enabled,
		idle:        blackout.NewIdleTracker(idleThresholdMs, blackout.GetTickCount()),
	}
}

// start creates the tray icon and, if enabled, starts the idle countdown.
func (g *guard) start() error {
	var err error
	if g.trayHwnd, err = tray.NewWindow(g.toggle, g.showMenu); err != nil {
		return fmt.Errorf("creating tray window: %w", err)
	}
	g.applyTrayIcon()
	if g.enabled {
		g.idle.Reset(blackout.GetTickCount())
		return g.checkIdle()
	}
	return nil
}

// run pumps messages until Exit is chosen from the tray menu (or the
// thread otherwise receives WM_QUIT). It returns an error only if the
// idle guard can no longer work.
func (g *guard) run() error {
	for {
		m, ok := blackout.GetMessage()
		if !ok {
			return nil
		}
		if m.Hwnd != 0 {
			blackout.Dispatch(m)
			continue
		}
		switch m.Message {
		case blackout.WMTimer:
			if err := g.handleTimer(m.WParam); err != nil {
				return err
			}
		case blackout.WMEscapePressed:
			if err := g.handleEscape(); err != nil {
				return err
			}
		default:
			blackout.Dispatch(m)
		}
	}
}

func (g *guard) handleTimer(id uintptr) error {
	if id != 0 && id == g.idleTimer {
		return g.checkIdle()
	}
	g.session.HandleTimer(id)
	return nil
}

// handleEscape ends the blackout, if one is running, and restarts the
// idle countdown from now.
func (g *guard) handleEscape() error {
	if g.session == nil {
		return nil // a stray press that was queued just as the blackout ended
	}
	g.endBlackout()
	g.idle.Reset(blackout.GetTickCount())
	return g.checkIdle()
}

// checkIdle blacks out if the idle threshold has been reached, and
// otherwise arms the idle timer for exactly the time remaining. Input in
// the meantime only moves the deadline later, so the timer can never fire
// too late; if it fires before a moved deadline, checkIdle re-arms it for
// the rest.
func (g *guard) checkIdle() error {
	g.stopIdleTimer()
	remaining := g.idle.Remaining(blackout.GetTickCount(), blackout.GetLastInputTick())
	if remaining <= 0 {
		return g.enterBlackout()
	}
	var err error
	if g.idleTimer, err = blackout.StartTimer(uint32(remaining)); err != nil {
		return fmt.Errorf("starting idle timer: %w", err)
	}
	return nil
}

func (g *guard) stopIdleTimer() {
	blackout.StopTimer(g.idleTimer)
	g.idleTimer = 0
}

func (g *guard) enterBlackout() error {
	g.logf("Idle timeout reached - entering blackout")
	var err error
	if g.session, err = blackout.Start(g.heartbeatMs, g.logf); err != nil {
		return fmt.Errorf("entering blackout: %w", err)
	}
	return nil
}

func (g *guard) endBlackout() {
	if g.session == nil {
		return
	}
	g.logf("Exiting blackout, resuming idle watch")
	g.session.End()
	g.session = nil
}

// toggle is the tray icon's left-click action.
func (g *guard) toggle() { g.setEnabled(!g.enabled) }

func (g *guard) setEnabled(v bool) {
	if g.enabled == v {
		return
	}
	g.enabled = v
	if v {
		g.logf("Enabled via tray")
		blackout.BlockSleep()
		// Idle time that built up while disabled mustn't count.
		g.idle.Reset(blackout.GetTickCount())
		if err := g.checkIdle(); err != nil {
			g.logf("EXCEPTION %v", err)
			blackout.PostQuitMessage()
		}
	} else {
		g.logf("Disabled via tray")
		g.endBlackout()
		g.stopIdleTimer()
		blackout.RestoreExecutionState()
	}
	g.applyTrayIcon()
}

// showMenu is the tray icon's right-click action.
func (g *guard) showMenu() {
	id := tray.ShowMenu(g.trayHwnd, []tray.MenuItem{
		{ID: menuIDEnable, Label: "Enable", Checked: g.enabled},
		{ID: menuIDDisable, Label: "Disable", Checked: !g.enabled},
		{},
		{ID: menuIDConfigure, Label: "Configure"},
		{},
		{ID: menuIDExit, Label: "Exit"},
	})
	switch id {
	case menuIDEnable:
		g.setEnabled(true)
	case menuIDDisable:
		g.setEnabled(false)
	case menuIDConfigure:
		g.openConfigFile()
	case menuIDExit:
		blackout.PostQuitMessage()
	}
}

func (g *guard) openConfigFile() {
	path, err := config.Path()
	if err != nil {
		g.logf("EXCEPTION resolving config.yaml path: %v", err)
		return
	}
	if err := blackout.OpenFile(path); err != nil {
		g.logf("EXCEPTION opening config.yaml: %v", err)
	}
}

// applyTrayIcon shows (or updates) the tray icon and tooltip for the
// current enabled state.
func (g *guard) applyTrayIcon() {
	build, state := tray.EnabledIcon, "guarding"
	if !g.enabled {
		build, state = tray.DisabledIcon, "disabled"
	}
	newIcon, err := build()
	if err != nil {
		g.logf("EXCEPTION building tray icon: %v", err)
		return
	}
	tooltip := fmt.Sprintf("StayWakeBlackScreenIdle %s - %s", version, state)
	if err := tray.SetIcon(g.trayHwnd, newIcon, tooltip); err != nil {
		g.logf("EXCEPTION showing tray icon: %v", err)
	}
	old := g.trayIcon
	g.trayIcon = newIcon
	tray.DestroyIconHandle(old)
}

// cleanup undoes everything the guard set up; it runs as the program
// exits, whatever state it's in.
func (g *guard) cleanup() {
	g.session.End()
	g.stopIdleTimer()
	blackout.RestoreExecutionState()
	if blackout.IsCapsLockOn() {
		blackout.ToggleCapsLock()
	}
	tray.RemoveIcon(g.trayHwnd)
	tray.DestroyIconHandle(g.trayIcon)
	tray.DestroyWindow(g.trayHwnd)
}
