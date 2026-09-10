//go:build windows

// Package tray provides a minimal Shell_NotifyIcon-based system tray icon
// with a right-click popup menu, independent of any GUI toolkit.
package tray

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32  = windows.NewLazySystemDLL("user32.dll")
	modShell32 = windows.NewLazySystemDLL("shell32.dll")

	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenuEx    = modUser32.NewProc("TrackPopupMenuEx")
	procPostMessageW        = modUser32.NewProc("PostMessageW")

	procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")
)

const (
	wsOverlappedWindow = 0x00000000

	wmDestroy   = 0x0002
	wmNull      = 0x0000
	wmLButtonUp = 0x0202
	wmRButtonUp = 0x0205

	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfSeparator = 0x00000800
	mfChecked   = 0x00000008
	mfGrayed    = 0x00000001

	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100
	tpmNoAnimation = 0x4000
)

// CW_USEDEFAULT as a signed 32-bit value, converted to uintptr via int64 so
// the two's-complement bit pattern is preserved. Built from a variable
// (not a constant expression) so the conversion happens at runtime -
// converting the negative literal directly would trip uintptr's
// compile-time "constant overflows" check.
var cwUseDefaultI32 int32 = -2147483648
var cwUseDefault = uintptr(int64(cwUseDefaultI32))

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

type notifyIconDataW struct {
	cbSize            uint32
	hWnd              uintptr
	uID               uint32
	uFlags            uint32
	uCallbackMessage  uint32
	hIcon             uintptr
	szTip             [128]uint16
	dwState           uint32
	dwStateMask       uint32
	szInfo            [256]uint16
	uTimeoutOrVersion uint32
	szInfoTitle       [64]uint16
	dwInfoFlags       uint32
	guidItem          guid
	hBalloonIcon      uintptr
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

type point struct{ X, Y int32 }

const trayWindowClassName = "StayWakeTrayHiddenWindow"

var (
	mu           sync.Mutex
	onLeftClick  func()
	onRightClick func()

	wndProcCB = syscall.NewCallback(func(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
		if message == wmTrayCallback {
			switch lParam {
			case wmLButtonUp:
				mu.Lock()
				cb := onLeftClick
				mu.Unlock()
				if cb != nil {
					cb()
				}
				return 0
			case wmRButtonUp:
				mu.Lock()
				cb := onRightClick
				mu.Unlock()
				if cb != nil {
					cb()
				}
				return 0
			}
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
		return r
	})
)

func utf16Ptr(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// NewWindow creates a hidden window that owns the tray icon and any popup
// menu, and wires left/right click callbacks. It must be created on, and
// its messages pumped from, the same OS thread for the lifetime of the
// program (see runtime.LockOSThread in main).
func NewWindow(left, right func()) (uintptr, error) {
	mu.Lock()
	onLeftClick, onRightClick = left, right
	mu.Unlock()

	hInstance := getModuleHandle()

	wc := wndClassExW{
		lpfnWndProc:   wndProcCB,
		hInstance:     hInstance,
		lpszClassName: utf16Ptr(trayWindowClassName),
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return 0, fmt.Errorf("RegisterClassExW: %w", e)
	}

	hwnd, _, e := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr(trayWindowClassName))),
		uintptr(unsafe.Pointer(utf16Ptr("StayWakeBlackScreenIdle"))),
		uintptr(wsOverlappedWindow),
		cwUseDefault, cwUseDefault, cwUseDefault, cwUseDefault,
		0, 0, uintptr(hInstance), 0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW: %w", e)
	}
	// Deliberately never shown (no ShowWindow call) - it exists only to
	// own the notify icon and receive its callback messages.
	return hwnd, nil
}

func DestroyWindow(hwnd uintptr) {
	if hwnd != 0 {
		procDestroyWindow.Call(hwnd)
	}
}

func getModuleHandle() syscall.Handle {
	modKernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := modKernel32.NewProc("GetModuleHandleW")
	r, _, _ := proc.Call(0)
	return syscall.Handle(r)
}

func newNotifyIconData(hwnd, hIcon uintptr, tooltip string) notifyIconDataW {
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = nifMessage | nifIcon | nifTip
	nid.uCallbackMessage = wmTrayCallback
	nid.hIcon = hIcon
	tip := windows.StringToUTF16(tooltip)
	n := copy(nid.szTip[:], tip)
	if n == len(nid.szTip) {
		nid.szTip[len(nid.szTip)-1] = 0
	}
	return nid
}

// AddIcon adds the tray icon. hwnd must come from NewWindow.
func AddIcon(hwnd, hIcon uintptr, tooltip string) error {
	nid := newNotifyIconData(hwnd, hIcon, tooltip)
	if r, _, e := procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_ADD): %w", e)
	}
	return nil
}

// UpdateIcon changes the icon/tooltip of an already-added tray icon.
func UpdateIcon(hwnd, hIcon uintptr, tooltip string) error {
	nid := newNotifyIconData(hwnd, hIcon, tooltip)
	if r, _, e := procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return fmt.Errorf("Shell_NotifyIconW(NIM_MODIFY): %w", e)
	}
	return nil
}

// RemoveIcon removes the tray icon. Safe to call on a zero hwnd.
func RemoveIcon(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

// MenuItem is one entry in the popup menu shown by ShowMenu. ID 0 is
// reserved (means "nothing selected"); a zero-value MenuItem renders as a
// separator.
type MenuItem struct {
	ID       uint32
	Label    string
	Checked  bool
	Disabled bool
}

// ShowMenu displays a popup menu at the current cursor position, owned by
// hwnd (from NewWindow), and blocks until the user picks an item or
// dismisses it. Returns the selected item's ID, or 0 if none was chosen.
func ShowMenu(hwnd uintptr, items []MenuItem) uint32 {
	hMenu, _, _ := procCreatePopupMenu.Call()
	if hMenu == 0 {
		return 0
	}
	defer procDestroyMenu.Call(hMenu)

	for _, it := range items {
		if it.Label == "" {
			procAppendMenuW.Call(hMenu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if it.Checked {
			flags |= mfChecked
		}
		if it.Disabled {
			flags |= mfGrayed
		}
		procAppendMenuW.Call(hMenu, flags, uintptr(it.ID), uintptr(unsafe.Pointer(utf16Ptr(it.Label))))
	}

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

	// Required so the menu reliably closes when the user clicks away from
	// it (documented Win32 tray-icon idiom).
	procSetForegroundWindow.Call(hwnd)

	id, _, _ := procTrackPopupMenuEx.Call(
		hMenu,
		uintptr(tpmRightButton|tpmReturnCmd|tpmNoAnimation),
		uintptr(pt.X), uintptr(pt.Y),
		hwnd, 0,
	)

	procPostMessageW.Call(hwnd, wmNull, 0, 0)
	return uint32(id)
}
