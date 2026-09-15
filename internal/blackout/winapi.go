//go:build windows

// Package blackout wraps the raw Win32 APIs this tool needs: blocking
// sleep/display-off, a system-wide low-level keyboard/mouse input block,
// per-monitor black overlay windows, DPI awareness, and idle detection.
// It has no dependency on any GUI toolkit - just user32/kernel32/gdi32/
// shcore via syscall, the same primitives the original PowerShell version
// reached via Add-Type/P-Invoke.
package blackout

import (
	"golang.org/x/sys/windows"

	"windows-stay-wake-black-screen/internal/win32"
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modShcore   = windows.NewLazySystemDLL("shcore.dll")

	procSetThreadExecutionState = modKernel32.NewProc("SetThreadExecutionState")
	procGetTickCount            = modKernel32.NewProc("GetTickCount")

	procShowWindow                    = modUser32.NewProc("ShowWindow")
	procGetMessageW                   = modUser32.NewProc("GetMessageW")
	procTranslateMessage              = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW              = modUser32.NewProc("DispatchMessageW")
	procPostQuitMessage               = modUser32.NewProc("PostQuitMessage")
	procSetTimer                      = modUser32.NewProc("SetTimer")
	procKillTimer                     = modUser32.NewProc("KillTimer")
	procSetWindowsHookExW             = modUser32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx           = modUser32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx                = modUser32.NewProc("CallNextHookEx")
	procGetKeyState                   = modUser32.NewProc("GetKeyState")
	procKeybdEvent                    = modUser32.NewProc("keybd_event")
	procShowCursor                    = modUser32.NewProc("ShowCursor")
	procEnumDisplayMonitors           = modUser32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW               = modUser32.NewProc("GetMonitorInfoW")
	procSetProcessDpiAwarenessContext = modUser32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = modUser32.NewProc("SetProcessDPIAware")
	procGetLastInputInfo              = modUser32.NewProc("GetLastInputInfo")

	procCreateSolidBrush = modGdi32.NewProc("CreateSolidBrush")

	procSetProcessDpiAwareness = modShcore.NewProc("SetProcessDpiAwareness")
)

const (
	wsPopup = 0x80000000

	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	swShow = 5

	whKeyboardLL = 13
	whMouseLL    = 14

	wmKeydown    = 0x0100
	wmSyskeydown = 0x0104

	// WMTimer is exposed so callers can recognize WM_TIMER messages (posted
	// with Hwnd 0 for timers created via StartTimer) in their message loop.
	WMTimer = 0x0113

	// WMEscapePressed arrives (with Hwnd 0) in the message loop of the
	// thread running a blackout Session whenever Escape is pressed.
	WMEscapePressed = win32.WMEscapePressed

	vkEscape  = 0x1B
	vkCapital = 0x14

	keyeventfKeyup = 0x0002

	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002

	// Arbitrary marker stamped on our own synthetic Caps Lock keystrokes
	// (via keybd_event's dwExtraInfo) so the input-block hook can recognize
	// and pass through only these specific events - otherwise the hook
	// would swallow them like everything else, and the toggle/LED would
	// never actually update.
	ownInjectedMarker = 0x53504143 // 'CAPS' ASCII

	// (DPI_AWARENESS_CONTEXT)-4, i.e. PER_MONITOR_AWARE_V2. Written this
	// way (rather than a fixed-width literal) so it's correct regardless
	// of uintptr's width.
	dpiAwarenessContextPerMonitorAwareV2 = ^uintptr(3)
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type point struct{ X, Y int32 }

type rawMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type monitorInfo struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
}

type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}
