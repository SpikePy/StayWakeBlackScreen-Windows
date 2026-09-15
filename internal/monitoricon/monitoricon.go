// Package monitoricon defines the flat monitor glyph (frame, screen
// interior, stand/base) shared by the runtime tray icon
// (internal/tray) and the generated .exe file icon (tools/genicon), so
// both always draw the exact same shape. It has no OS dependency - just
// the glyph's geometry - so it builds on any platform.
package monitoricon

// GridSize is the logical resolution the glyph is authored at; other
// render sizes sample this grid via nearest-neighbor scaling (see
// AtScaled), which stays crisp because every edge in the glyph is an
// integer-multiple-friendly axis-aligned rectangle.
const GridSize = 32

// Part identifies which part of the monitor silhouette a grid cell
// belongs to, so the screen interior can be colored independently of
// the frame around it and the stand/base below it.
type Part int

const (
	PartNone Part = iota
	PartFrame
	PartScreenInterior
	PartStand
)

// At reports which part of the monitor silhouette (x, y) on a
// GridSize x GridSize logical grid belongs to: a screen (frame plus
// interior) on a small stand and base.
func At(x, y int) Part {
	switch {
	case x >= 5 && x <= 26 && y >= 6 && y <= 18:
		return PartScreenInterior
	case x >= 3 && x <= 28 && y >= 4 && y <= 20:
		return PartFrame
	case x >= 14 && x <= 17 && y >= 21 && y <= 24:
		return PartStand
	case x >= 9 && x <= 22 && y >= 25 && y <= 26:
		return PartStand
	default:
		return PartNone
	}
}

// AtScaled maps pixel (x, y) on a size x size canvas down to the
// GridSize logical grid via nearest-neighbor scaling and reports its
// part.
func AtScaled(x, y, size int) Part {
	return At(x*GridSize/size, y*GridSize/size)
}
