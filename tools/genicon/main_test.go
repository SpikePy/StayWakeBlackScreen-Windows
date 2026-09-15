package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func renderAll() []*image.RGBA {
	var imgs []*image.RGBA
	for _, s := range sizes {
		imgs = append(imgs, render(s))
	}
	return imgs
}

func TestEncodeICO(t *testing.T) {
	data, err := encodeICO(renderAll())
	if err != nil {
		t.Fatal(err)
	}

	r := bytes.NewReader(data)
	var dir iconDir
	if err := binary.Read(r, binary.LittleEndian, &dir); err != nil {
		t.Fatal(err)
	}
	if dir.Reserved != 0 || dir.Type != 1 || int(dir.Count) != len(sizes) {
		t.Fatalf("header = %+v, want Reserved 0, Type 1, Count %d", dir, len(sizes))
	}

	for _, size := range sizes {
		var e iconDirEntry
		if err := binary.Read(r, binary.LittleEndian, &e); err != nil {
			t.Fatal(err)
		}
		wantDim := byte(size)
		if size >= 256 {
			wantDim = 0
		}
		if e.Width != wantDim || e.Height != wantDim || e.Planes != 1 || e.BitCount != 32 {
			t.Errorf("%dpx entry = %+v", size, e)
		}
		end := int(e.ImageOffset) + int(e.BytesInRes)
		if end > len(data) {
			t.Fatalf("%dpx frame runs past the end of the file (%d > %d)", size, end, len(data))
		}
		frame, err := png.Decode(bytes.NewReader(data[e.ImageOffset:end]))
		if err != nil {
			t.Fatalf("%dpx frame is not a valid PNG: %v", size, err)
		}
		if b := frame.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%dpx frame decodes as %dx%d", size, b.Dx(), b.Dy())
		}
	}
}

// TestCommittedIconResourcesAreCurrent fails if the glyph changed but the
// committed rsrc_windows_amd64.syso files weren't regenerated (see the
// README), or if a program in cmd/ has no icon resource at all: rsrc
// stores each PNG frame verbatim, so every frame genicon renders today
// must appear in each .syso.
func TestCommittedIconResourcesAreCurrent(t *testing.T) {
	var frames [][]byte
	for _, img := range renderAll() {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, buf.Bytes())
	}

	cmdDirs, err := filepath.Glob("../../cmd/*")
	if err != nil || len(cmdDirs) == 0 {
		t.Fatalf("no cmd directories found: %v", err)
	}
	for _, dir := range cmdDirs {
		syso := filepath.Join(dir, "rsrc_windows_amd64.syso")
		data, err := os.ReadFile(syso)
		if err != nil {
			t.Errorf("%s has no icon resource: %v", dir, err)
			continue
		}
		for i, f := range frames {
			if !bytes.Contains(data, f) {
				t.Errorf("%s is stale: its %dpx frame doesn't match genicon's output; regenerate it (see README)", syso, sizes[i])
			}
		}
	}
}
