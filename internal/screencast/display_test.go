package screencast

import "testing"

func TestFindDisplayMatchesExactlyOne(t *testing.T) {
	displays := []DisplayInfo{
		{W: 1440, H: 900, PixelW: 2880, PixelH: 1800, Main: true, Name: "eDP-1"},
		{W: 2560, H: 1440, PixelW: 2560, PixelH: 1440, Name: "DP-1"},
		{W: 2560, H: 1440, PixelW: 2560, PixelH: 1440, Name: "DP-2"},
	}
	if d := FindDisplay(displays, 2880, 1800); d == nil || d.Name != "eDP-1" {
		t.Errorf("pixel size match = %+v, want eDP-1", d)
	}
	if d := FindDisplay(displays, 1440, 900); d == nil || d.Name != "eDP-1" {
		t.Errorf("point size match = %+v, want eDP-1", d)
	}
	if d := FindDisplay(displays, 2560, 1440); d != nil {
		t.Errorf("two identical displays matched %+v, want nil (ambiguous)", d)
	}
	if d := FindDisplay(displays, 1, 1); d != nil {
		t.Errorf("no display matched %+v, want nil", d)
	}
	if got := displays[0].Size(); got != "2880x1800" {
		t.Errorf("Size() = %q, want pixel size", got)
	}
}
