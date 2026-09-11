//go:build windows

// Package blackout wraps the raw Win32 APIs this tool needs: blocking
// sleep/display-off, a system-wide low-level keyboard/mouse input block,
// per-monitor black overlay windows, DPI awareness, and idle detection.
// It has no dependency on any GUI toolkit - just user32/kernel32/gdi32/
// shcore via syscall, the same primitives the original PowerShell version
// reached via Add-Type/P-Invoke.
package blackout

import (
	"syscall"

	"golang.org/x/sys/windows"
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")
	modShcore   = windows.NewLazySystemDLL("shcore.dll")

	procGetModuleHandleW        = modKernel32.NewProc("GetModuleHandleW")
	procSetThreadExecutionState = modKernel32.NewProc("SetThreadExecutionState")
	procGetTickCount            = modKernel32.NewProc("GetTickCount")

	procRegisterClassExW              = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW               = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW                = modUser32.NewProc("DefWindowProcW")
	procShowWindow                    = modUser32.NewProc("ShowWindow")
	procDestroyWindow                 = modUser32.NewProc("DestroyWindow")
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
	procSetForegroundWindow           = modUser32.NewProc("SetForegroundWindow")
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

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
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

// iptr converts a (possibly negative, e.g. a monitor left of the primary)
// int32 coordinate to the uintptr a syscall arg slot expects, going via
// int64 so the sign is preserved correctly regardless of pointer width.
func iptr(v int32) uintptr { return uintptr(int64(v)) }

func utf16Ptr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		// Only fails on an embedded NUL byte, which none of our fixed
		// strings contain.
		panic(err)
	}
	return p
}

func getModuleHandle() syscall.Handle {
	r, _, _ := procGetModuleHandleW.Call(0)
	return syscall.Handle(r)
}
