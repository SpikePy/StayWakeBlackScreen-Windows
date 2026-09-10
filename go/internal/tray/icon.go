//go:build windows

package tray

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modGdi32 = windows.NewLazySystemDLL("gdi32.dll")

	procCreateDIBSection   = modGdi32.NewProc("CreateDIBSection")
	procCreateBitmap       = modGdi32.NewProc("CreateBitmap")
	procDeleteObject       = modGdi32.NewProc("DeleteObject")
	procCreateIconIndirect = modUser32.NewProc("CreateIconIndirect")
	procDestroyIcon        = modUser32.NewProc("DestroyIcon")
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type iconInfo struct {
	fIcon    int32
	xHotspot uint32
	yHotspot uint32
	hbmMask  uintptr
	hbmColor uintptr
}

const iconSize = 32

// pixel is BGRA order (what a 32bpp Windows DIB section expects).
type pixel struct{ B, G, R, A byte }

// monitorGlyph reports whether (x, y) on a 32x32 canvas is part of the
// monitor silhouette: a screen rectangle on a small stand and base.
func monitorGlyph(x, y int) bool {
	switch {
	case x >= 3 && x <= 28 && y >= 4 && y <= 20:
		return true // screen
	case x >= 14 && x <= 17 && y >= 21 && y <= 24:
		return true // stand
	case x >= 9 && x <= 22 && y >= 25 && y <= 26:
		return true // base
	default:
		return false
	}
}

// buildMonitorIcon renders a simple flat monitor glyph as an alpha-blended
// HICON: solid fillColor for enabled=true (black, "actively guarding"),
// or a light outline over transparency for enabled=false (white/hollow,
// "disabled") so it still reads against a light taskbar.
func buildMonitorIcon(enabled bool) (uintptr, error) {
	var bi bitmapInfoHeader
	bi.biSize = uint32(unsafe.Sizeof(bi))
	bi.biWidth = iconSize
	bi.biHeight = -iconSize // negative = top-down DIB, simpler indexing
	bi.biPlanes = 1
	bi.biBitCount = 32
	bi.biCompression = 0 // BI_RGB

	var bitsPtr uintptr
	hColor, _, e := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bitsPtr)), 0, 0)
	if hColor == 0 {
		return 0, fmt.Errorf("CreateDIBSection: %w", e)
	}
	pixels := unsafe.Slice((*pixel)(unsafe.Pointer(bitsPtr)), iconSize*iconSize)

	fill := pixel{B: 0, G: 0, R: 0, A: 255}          // black, active
	outline := pixel{B: 255, G: 255, R: 255, A: 255} // white, disabled
	if !enabled {
		fill = outline
	}
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			if monitorGlyph(x, y) {
				pixels[y*iconSize+x] = fill
			}
		}
	}

	// AND mask: all zero bits means "always use the color bitmap's own
	// alpha", the standard approach for a modern alpha-blended icon.
	maskBytes := make([]byte, (iconSize/8)*iconSize)
	hMask, _, e := procCreateBitmap.Call(iconSize, iconSize, 1, 1, uintptr(unsafe.Pointer(&maskBytes[0])))
	if hMask == 0 {
		procDeleteObject.Call(hColor)
		return 0, fmt.Errorf("CreateBitmap: %w", e)
	}

	ii := iconInfo{fIcon: 1, hbmMask: hMask, hbmColor: hColor}
	hIcon, _, e := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))

	// CreateIconIndirect copies the bitmap data internally; the source
	// bitmaps are ours to delete right away regardless of its outcome.
	procDeleteObject.Call(hColor)
	procDeleteObject.Call(hMask)

	if hIcon == 0 {
		return 0, fmt.Errorf("CreateIconIndirect: %w", e)
	}
	return hIcon, nil
}

// EnabledIcon returns a black monitor icon HICON (guard active).
func EnabledIcon() (uintptr, error) { return buildMonitorIcon(true) }

// DisabledIcon returns a white monitor icon HICON (guard disabled).
func DisabledIcon() (uintptr, error) { return buildMonitorIcon(false) }

// DestroyIconHandle frees an HICON returned by EnabledIcon/DisabledIcon.
// Safe to call on a zero handle.
func DestroyIconHandle(hIcon uintptr) {
	if hIcon != 0 {
		procDestroyIcon.Call(hIcon)
	}
}
