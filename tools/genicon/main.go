// Command genicon renders the monitoricon glyph (the same shape used by
// the runtime tray icon) as a multi-resolution .ico file, for embedding
// as the .exe file icon of StayWakeBlackScreen.exe and
// StayWakeBlackScreenIdle.exe. It has no OS dependency and runs on any
// platform.
//
// Usage:
//
//	go run ./tools/genicon monitor.ico
//
// The resulting .ico is then embedded as a Windows resource with
// akavel/rsrc, once per cmd directory that should carry it:
//
//	go run github.com/akavel/rsrc@latest -ico monitor.ico -arch amd64 -o cmd/staywakeblackscreen/rsrc_windows_amd64.syso
//	go run github.com/akavel/rsrc@latest -ico monitor.ico -arch amd64 -o cmd/staywakeblackscreenidle/rsrc_windows_amd64.syso
//
// `go build` picks up a *_windows_amd64.syso file automatically, no
// other wiring needed.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"

	"windows-stay-wake-black-screen/internal/monitoricon"
)

// sizes are the frames baked into the .ico, covering everything from a
// taskbar-scale icon up to Explorer's "extra large" thumbnail view. Each
// evenly divides or multiplies GridSize, so nearest-neighbor scaling
// stays crisp - every edge in the glyph is an axis-aligned rectangle.
var sizes = []int{16, 24, 32, 48, 256}

func render(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	black := color.RGBA{0, 0, 0, 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			switch monitoricon.AtScaled(x, y, size) {
			case monitoricon.PartScreenInterior, monitoricon.PartFrame, monitoricon.PartStand:
				img.Set(x, y, black)
			}
		}
	}
	return img
}

// writeICO packs the given square images, each a distinct size, as a
// Vista+-style ICO with PNG-compressed frames.
func writeICO(w *os.File, imgs []*image.RGBA) error {
	type dirEntry struct {
		w, h   byte
		size   uint32
		offset uint32
	}

	var pngs [][]byte
	for _, img := range imgs {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return err
		}
		pngs = append(pngs, buf.Bytes())
	}

	offset := uint32(6 + 16*len(imgs))
	var entries []dirEntry
	for i, img := range imgs {
		side := img.Bounds().Dx()
		b := byte(side)
		if side >= 256 {
			b = 0 // 0 means 256 in the ICO directory entry format
		}
		entries = append(entries, dirEntry{b, b, uint32(len(pngs[i])), offset})
		offset += uint32(len(pngs[i]))
	}

	if err := binary.Write(w, binary.LittleEndian, uint16(0)); err != nil { // reserved
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(1)); err != nil { // type: icon
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint16(len(imgs))); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := w.Write([]byte{e.w, e.h, 0, 0}); err != nil {
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, uint16(1)); err != nil { // color planes
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, uint16(32)); err != nil { // bits per pixel
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, e.size); err != nil {
			return err
		}
		if err := binary.Write(w, binary.LittleEndian, e.offset); err != nil {
			return err
		}
	}
	for _, data := range pngs {
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: genicon <output.ico>")
		os.Exit(1)
	}

	var imgs []*image.RGBA
	for _, s := range sizes {
		imgs = append(imgs, render(s))
	}

	f, err := os.Create(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer f.Close()
	if err := writeICO(f, imgs); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
