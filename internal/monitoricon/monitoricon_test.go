package monitoricon

import "testing"

func TestGlyphShape(t *testing.T) {
	tests := []struct {
		name string
		x, y int
		want Part
	}{
		{"top-left corner is empty", 0, 0, PartNone},
		{"middle of the screen", 16, 12, PartScreenInterior},
		{"top-left of the bezel", 3, 4, PartFrame},
		{"stand neck", 15, 22, PartStand},
		{"stand base", 10, 26, PartStand},
		{"below the base is empty", 16, 30, PartNone},
	}
	for _, tt := range tests {
		if got := At(tt.x, tt.y); got != tt.want {
			t.Errorf("%s: At(%d, %d) = %v, want %v", tt.name, tt.x, tt.y, got, tt.want)
		}
	}
}

func TestAtScaledMatchesAtAtNativeSize(t *testing.T) {
	for y := 0; y < GridSize; y++ {
		for x := 0; x < GridSize; x++ {
			if a, s := At(x, y), AtScaled(x, y, GridSize); a != s {
				t.Fatalf("(%d, %d): At = %v but AtScaled at GridSize = %v", x, y, a, s)
			}
		}
	}
}
