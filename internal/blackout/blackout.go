//go:build windows

package blackout

import (
	"fmt"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
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

const windowClassName = "StayWakeBlackoutWindow"

var (
	classOnce sync.Once
	classErr  error
	hInstance syscall.Handle

	wndProcCB = syscall.NewCallback(func(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return r
	})
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
	if err := windows.ShellExecute(0, utf16Ptr("open"), file, nil, nil, swShow); err != nil {
		return fmt.Errorf("ShellExecuteW: %w", err)
	}
	return nil
}

var escapeRequested atomic.Bool

// InstallInputBlockHooks installs system-wide low-level keyboard and mouse
// hooks that swallow every event - nothing reaches any window, including
// this process's own. Only Escape is special-cased: it still isn't
// delivered anywhere, but it sets a flag pollable via TakeEscapeRequested.
// Windows never lets any hook suppress Ctrl+Alt+Del, so that combination
// always remains a hard escape hatch regardless of anything going wrong.
func InstallInputBlockHooks() error {
	escapeRequested.Store(false)
	hMod := getModuleHandle()

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

// RemoveInputBlockHooks removes both hooks, if installed. Safe to call more
// than once.
func RemoveInputBlockHooks() {
	if keyboardHookHandle != 0 {
		procUnhookWindowsHookEx.Call(keyboardHookHandle)
		keyboardHookHandle = 0
	}
	if mouseHookHandle != 0 {
		procUnhookWindowsHookEx.Call(mouseHookHandle)
		mouseHookHandle = 0
	}
}

// TakeEscapeRequested reports whether Escape was pressed since the hooks
// were installed or since the last call to TakeEscapeRequested, and clears
// the flag.
func TakeEscapeRequested() bool {
	return escapeRequested.Swap(false)
}

var (
	keyboardHookHandle uintptr
	mouseHookHandle    uintptr

	keyboardHookCB = syscall.NewCallback(keyboardHookProc)
	mouseHookCB    = syscall.NewCallback(mouseHookProc)
)

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
	if wParam == wmKeydown || wParam == wmSyskeydown {
		if data.VkCode == vkEscape {
			escapeRequested.Store(true)
		}
	}
	// Swallow every other key: do not call CallNextHookEx, so nothing -
	// not even our own window - ever receives this input.
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

// PulseCapsLock toggles Caps Lock and, 150ms later, back again: one
// activity heartbeat that leaves the Caps Lock state as it found it.
func PulseCapsLock() {
	ToggleCapsLock()
	time.Sleep(150 * time.Millisecond)
	ToggleCapsLock()
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

// Monitors returns the full bounds (not just the work area) of every
// display, matching .NET's Screen.AllScreens / Screen.Bounds - so overlay
// windows cover the entire screen, not just the area outside the taskbar.
func Monitors() ([]Rect, error) {
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
		hInstance = getModuleHandle()
		brush, _, _ := procCreateSolidBrush.Call(0) // RGB(0,0,0) = black
		wc := wndClassExW{
			lpfnWndProc:   wndProcCB,
			hInstance:     hInstance,
			hbrBackground: syscall.Handle(brush),
			lpszClassName: utf16Ptr(windowClassName),
		}
		wc.cbSize = uint32(unsafe.Sizeof(wc))
		r, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		if r == 0 {
			classErr = e
		}
	})
	return classErr
}

// CreateOverlayWindow creates (but does not show) a borderless, topmost,
// black, cursor-less, taskbar-hidden window covering r.
func CreateOverlayWindow(r Rect) (uintptr, error) {
	if err := ensureClass(); err != nil {
		return 0, err
	}
	hwnd, _, e := procCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow),
		uintptr(unsafe.Pointer(utf16Ptr(windowClassName))),
		uintptr(unsafe.Pointer(utf16Ptr(""))),
		uintptr(wsPopup),
		iptr(r.Left), iptr(r.Top), iptr(r.Right-r.Left), iptr(r.Bottom-r.Top),
		0, 0, uintptr(hInstance), 0,
	)
	if hwnd == 0 {
		return 0, e
	}
	return hwnd, nil
}

// ShowOverlayWindow shows a window created by CreateOverlayWindow.
func ShowOverlayWindow(hwnd uintptr) { procShowWindow.Call(hwnd, swShow) }

// DestroyOverlayWindow destroys a window created by CreateOverlayWindow.
// Safe to call on a zero handle.
func DestroyOverlayWindow(hwnd uintptr) {
	if hwnd != 0 {
		procDestroyWindow.Call(hwnd)
	}
}

// FocusWindow brings hwnd to the foreground.
func FocusWindow(hwnd uintptr) { procSetForegroundWindow.Call(hwnd) }

// HideCursor hides the system cursor (one-shot, mirrors Cursor.Hide()).
func HideCursor() { procShowCursor.Call(0) }

// ShowCursorAgain restores the system cursor hidden by HideCursor.
func ShowCursorAgain() { procShowCursor.Call(1) }

// MaxTimerMs is USER_TIMER_MAXIMUM, the longest interval StartTimer
// accepts (~24.8 days). Callers deriving an interval from user input
// should clamp to it before converting to uint32.
const MaxTimerMs = 0x7FFFFFFF

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
