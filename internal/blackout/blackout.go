//go:build windows

package blackout

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"windows-stay-wake-black-screen/internal/win32"
)

// Rect is a monitor's or window's bounds in physical (DPI-aware) pixels.
type Rect struct {
	Left, Top, Right, Bottom int32
}

// Msg is a decoded Win32 message from GetMessage.
type Msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
}

const overlayClassName = "StayWakeBlackoutWindow"

var (
	classOnce sync.Once
	classErr  error

	overlayWndProcCB = syscall.NewCallback(win32.DefWindowProc)
)

// EnableDPIAwareness must be called before any monitor bounds or window are
// touched, so Screen bounds come back as real physical pixels instead of
// scaled/virtualized values whenever display scaling isn't 100%. Tries the
// modern per-monitor-v2 API first, then falls back for older Windows.
func EnableDPIAwareness() {
	if procSetProcessDpiAwarenessContext.Find() == nil {
		if r, _, _ := procSetProcessDpiAwarenessContext.Call(dpiAwarenessContextPerMonitorAwareV2); r != 0 {
			return
		}
	}
	if procSetProcessDpiAwareness.Find() == nil {
		const processPerMonitorDPIAware = 2
		if r, _, _ := procSetProcessDpiAwareness.Call(processPerMonitorDPIAware); r == 0 { // S_OK
			return
		}
	}
	if procSetProcessDPIAware.Find() == nil {
		procSetProcessDPIAware.Call()
	}
}

// BlockSleep tells Windows sleep and display-off must not happen, and that
// the display must stay on, for as long as this process keeps running (or
// until RestoreExecutionState is called). Combined with covering every
// screen with a real black window (instead of powering the monitor off),
// Windows never sees a display-off/idle transition that could lock the
// session.
func BlockSleep() {
	procSetThreadExecutionState.Call(uintptr(esContinuous | esSystemRequired | esDisplayRequired))
}

// RestoreExecutionState undoes BlockSleep.
func RestoreExecutionState() {
	procSetThreadExecutionState.Call(uintptr(esContinuous))
}

// OpenFile opens path with whatever application Windows has associated
// with its extension (e.g. the default YAML editor for a .yaml file),
// the same as double-clicking it in Explorer.
func OpenFile(path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encoding path: %w", err)
	}
	if err := windows.ShellExecute(0, win32.UTF16Ptr("open"), file, nil, nil, swShow); err != nil {
		return fmt.Errorf("ShellExecuteW: %w", err)
	}
	return nil
}

var (
	keyboardHookHandle uintptr
	mouseHookHandle    uintptr

	keyboardHookCB = syscall.NewCallback(keyboardHookProc)
	mouseHookCB    = syscall.NewCallback(mouseHookProc)
)

// installInputBlockHooks installs system-wide low-level keyboard and mouse
// hooks that swallow every event - nothing reaches any window, including
// this process's own. Only Escape is special-cased: it still isn't
// delivered anywhere, but it posts WMEscapePressed to this thread.
// Windows never lets any hook suppress Ctrl+Alt+Del, so that combination
// always remains a hard escape hatch regardless of anything going wrong.
func installInputBlockHooks() error {
	hMod := win32.ModuleHandle()

	kh, _, e1 := procSetWindowsHookExW.Call(uintptr(whKeyboardLL), keyboardHookCB, uintptr(hMod), 0)
	if kh == 0 {
		return fmt.Errorf("SetWindowsHookExW(WH_KEYBOARD_LL): %w", e1)
	}
	keyboardHookHandle = kh

	mh, _, e2 := procSetWindowsHookExW.Call(uintptr(whMouseLL), mouseHookCB, uintptr(hMod), 0)
	if mh == 0 {
		procUnhookWindowsHookEx.Call(kh)
		keyboardHookHandle = 0
		return fmt.Errorf("SetWindowsHookExW(WH_MOUSE_LL): %w", e2)
	}
	mouseHookHandle = mh
	return nil
}

// removeInputBlockHooks removes both hooks, if installed. Safe to call
// more than once.
func removeInputBlockHooks() {
	if keyboardHookHandle != 0 {
		procUnhookWindowsHookEx.Call(keyboardHookHandle)
		keyboardHookHandle = 0
	}
	if mouseHookHandle != 0 {
		procUnhookWindowsHookEx.Call(mouseHookHandle)
		mouseHookHandle = 0
	}
}

func keyboardHookProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode < 0 {
		r, _, _ := procCallNextHookEx.Call(keyboardHookHandle, uintptr(nCode), wParam, lParam)
		return r
	}
	data := (*kbdllhookstruct)(unsafe.Pointer(lParam))
	if data.DwExtraInfo == ownInjectedMarker {
		// Our own synthetic Caps Lock heartbeat - let it through so the
		// toggle state and LED actually update.
		r, _, _ := procCallNextHookEx.Call(keyboardHookHandle, uintptr(nCode), wParam, lParam)
		return r
	}
	if (wParam == wmKeydown || wParam == wmSyskeydown) && data.VkCode == vkEscape {
		// Low-level hooks run on the thread that installed them, so this
		// lands in that thread's own queue and wakes its message loop
		// right away - nothing has to poll for it.
		win32.PostMessage(0, WMEscapePressed, 0, 0)
	}
	// Swallow the key, Escape included: do not call CallNextHookEx, so
	// nothing - not even our own window - ever receives this input.
	return 1
}

func mouseHookProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode < 0 {
		r, _, _ := procCallNextHookEx.Call(mouseHookHandle, uintptr(nCode), wParam, lParam)
		return r
	}
	// Swallow every mouse event (move, click, wheel) the same way.
	return 1
}

// ToggleCapsLock presses and releases the (synthetic, marked) Caps Lock
// key once, flipping its state.
func ToggleCapsLock() {
	marker := uintptr(ownInjectedMarker)
	procKeybdEvent.Call(uintptr(vkCapital), 0x45, 0, marker)
	procKeybdEvent.Call(uintptr(vkCapital), 0x45, uintptr(keyeventfKeyup), marker)
}

// IsCapsLockOn reports the current Caps Lock toggle state.
func IsCapsLockOn() bool {
	r, _, _ := procGetKeyState.Call(uintptr(vkCapital))
	return int16(r)&1 != 0
}

// GetLastInputTick returns GetLastInputInfo's tick count of the last real
// keyboard/mouse activity, comparable against GetTickCount (same units,
// same ~49.7-day wraparound).
func GetLastInputTick() uint32 {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))
	procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	return lii.dwTime
}

// GetTickCount returns milliseconds since boot, wrapping to 0 roughly every
// 49.7 days. Comparisons between two values from this call should go
// through int32, matching Windows' own wraparound-safe subtraction idiom.
func GetTickCount() uint32 {
	r, _, _ := procGetTickCount.Call()
	return uint32(r)
}

var (
	monitorsMu     sync.Mutex
	monitorsResult []Rect
)

var monitorEnumCB = syscall.NewCallback(func(hMonitor, _hdcMonitor uintptr, _lprcMonitor uintptr, _dwData uintptr) uintptr {
	var mi monitorInfo
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi)))
	monitorsResult = append(monitorsResult, Rect{
		Left: mi.rcMonitor.Left, Top: mi.rcMonitor.Top,
		Right: mi.rcMonitor.Right, Bottom: mi.rcMonitor.Bottom,
	})
	return 1
})

// monitors returns the full bounds (not just the work area) of every
// display, so overlay windows cover the entire screen, taskbar included.
func monitors() ([]Rect, error) {
	monitorsMu.Lock()
	defer monitorsMu.Unlock()
	monitorsResult = nil
	r, _, e := procEnumDisplayMonitors.Call(0, 0, monitorEnumCB, 0)
	if r == 0 {
		return nil, e
	}
	out := make([]Rect, len(monitorsResult))
	copy(out, monitorsResult)
	return out, nil
}

func ensureClass() error {
	classOnce.Do(func() {
		brush, _, _ := procCreateSolidBrush.Call(0) // RGB(0,0,0) = black
		classErr = win32.RegisterClass(overlayClassName, overlayWndProcCB, syscall.Handle(brush))
	})
	return classErr
}

// createOverlayWindow creates (but does not show) a borderless, topmost,
// black, taskbar-hidden window covering r.
func createOverlayWindow(r Rect) (uintptr, error) {
	if err := ensureClass(); err != nil {
		return 0, err
	}
	return win32.CreateWindow(wsExTopmost|wsExToolWindow, wsPopup, overlayClassName, "",
		r.Left, r.Top, r.Right-r.Left, r.Bottom-r.Top)
}

// StartTimer creates a message-only timer (delivered as WM_TIMER with Hwnd
// 0) and returns its system-assigned id, to be passed to StopTimer and
// compared against Msg.WParam in the message loop.
func StartTimer(elapseMs uint32) (uintptr, error) {
	r, _, e := procSetTimer.Call(0, 0, uintptr(elapseMs), 0)
	if r == 0 {
		return 0, e
	}
	return r, nil
}

// StopTimer stops a timer created by StartTimer. Safe to call on a zero id.
func StopTimer(id uintptr) {
	if id != 0 {
		procKillTimer.Call(0, id)
	}
}

// GetMessage blocks for the next message, like Win32 GetMessage. The
// second return value is false on WM_QUIT or an error, at which point the
// caller's message loop should stop.
func GetMessage() (*Msg, bool) {
	var m rawMsg
	r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
	if int32(r) <= 0 {
		return nil, false
	}
	return &Msg{Hwnd: m.Hwnd, Message: m.Message, WParam: m.WParam, LParam: m.LParam}, true
}

// Dispatch runs TranslateMessage + DispatchMessage for a message obtained
// from GetMessage.
func Dispatch(m *Msg) {
	raw := rawMsg{Hwnd: m.Hwnd, Message: m.Message, WParam: m.WParam, LParam: m.LParam}
	procTranslateMessage.Call(uintptr(unsafe.Pointer(&raw)))
	procDispatchMessageW.Call(uintptr(unsafe.Pointer(&raw)))
}

// PostQuitMessage causes the next GetMessage in this thread to return
// (nil, false), ending the message loop.
func PostQuitMessage() { procPostQuitMessage.Call(0) }
