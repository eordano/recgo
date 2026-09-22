package screencast

import (
	"image/color"
	"testing"
)

func TestBGRAToRGBA(t *testing.T) {
	// 2x1: pure blue then pure red, as GDI lays them out (B, G, R, x).
	src := []byte{0xff, 0, 0, 0, 0, 0, 0xff, 0}
	img, err := bgraToRGBA(2, 1, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := img.At(0, 0); got != (color.RGBA{0, 0, 0xff, 0xff}) {
		t.Errorf("pixel 0 = %+v, want opaque blue", got)
	}
	if got := img.At(1, 0); got != (color.RGBA{0xff, 0, 0, 0xff}) {
		t.Errorf("pixel 1 = %+v, want opaque red", got)
	}
	if _, err := bgraToRGBA(2, 1, src[:4]); err == nil {
		t.Error("short buffer accepted")
	}
	if _, err := bgraToRGBA(0, 0, nil); err == nil {
		t.Error("empty bitmap accepted")
	}
}

func TestLogicalSize(t *testing.T) {
	for _, c := range []struct{ px, dpi, want int }{
		{1920, 120, 1536}, {1200, 120, 960}, {2560, 96, 2560}, {3840, 144, 2560}, {1000, 0, 1000},
	} {
		if got := logicalSize(c.px, c.dpi); got != c.want {
			t.Errorf("logicalSize(%d, %d) = %d, want %d", c.px, c.dpi, got, c.want)
		}
	}
}
