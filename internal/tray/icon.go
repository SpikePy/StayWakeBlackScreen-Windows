//go:build windows

package tray

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"windows-stay-wake-black-screen/internal/monitoricon"
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

// iconSize matches monitoricon.GridSize, so the glyph maps 1:1 with no
// scaling needed.
const iconSize = monitoricon.GridSize

// pixel is BGRA order (what a 32bpp Windows DIB section expects).
type pixel struct{ B, G, R, A byte }

// buildMonitorIcon renders a simple flat monitor glyph as an alpha-blended
// HICON: a black frame, screen interior, and stand/base for enabled=true
// (guard active). For enabled=false (guard disabled), the exact same
// glyph is rendered grey instead of black and a diagonal red strike is
// drawn across it - the conventional "disabled" cue - so it stays
// clearly recognizable and visible against both light and dark taskbars.
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

	black := pixel{B: 0, G: 0, R: 0, A: 255}
	for y := 0; y < iconSize; y++ {
		for x := 0; x < iconSize; x++ {
			switch monitoricon.At(x, y) {
			case monitoricon.PartScreenInterior, monitoricon.PartFrame, monitoricon.PartStand:
				pixels[y*iconSize+x] = black
			}
		}
	}

	if !enabled {
		grey := pixel{B: 140, G: 140, R: 140, A: 255}
		for i := range pixels {
			if pixels[i].A != 0 {
				pixels[i] = grey
			}
		}
		// Diagonal "disabled" strike, top-left to bottom-right, spanning
		// the whole canvas (including the transparent background) so
		// it's unambiguous at tray size regardless of glyph shape.
		strikeRed := pixel{B: 30, G: 30, R: 200, A: 255}
		for y := 0; y < iconSize; y++ {
			for x := 0; x < iconSize; x++ {
				if d := x - y; d >= -2 && d <= 2 {
					pixels[y*iconSize+x] = strikeRed
				}
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

// EnabledIcon returns a monitor icon HICON with a black screen (guard active).
func EnabledIcon() (uintptr, error) { return buildMonitorIcon(true) }

// DisabledIcon returns the same monitor icon as EnabledIcon, greyed out
// with a diagonal red strike across it (guard disabled).
func DisabledIcon() (uintptr, error) { return buildMonitorIcon(false) }

// DestroyIconHandle frees an HICON returned by EnabledIcon/DisabledIcon.
// Safe to call on a zero handle.
func DestroyIconHandle(hIcon uintptr) {
	if hIcon != 0 {
		procDestroyIcon.Call(hIcon)
	}
}
