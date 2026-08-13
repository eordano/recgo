package screencast

import (
	"encoding/binary"
	"image"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func frame(w, h, stride, format uint32, pixels []byte) (map[string]dbus.Variant, []byte) {
	return map[string]dbus.Variant{
		"width":  dbus.MakeVariant(w),
		"height": dbus.MakeVariant(h),
		"stride": dbus.MakeVariant(stride),
		"format": dbus.MakeVariant(format),
		"type":   dbus.MakeVariant("raw"),
	}, pixels
}

func bgra(r, g, b, a uint8) []byte {
	var px [4]byte
	binary.LittleEndian.PutUint32(px[:], uint32(a)<<24|uint32(r)<<16|uint32(g)<<8|uint32(b))
	return px[:]
}

func TestDecodeKWinFrameChannelOrder(t *testing.T) {
	pix := bgra(0x10, 0x20, 0x30, 0xff)
	results, data := frame(1, 1, 4, qImageARGB32, pix)

	img, err := decodeKWinFrame(data, results)
	if err != nil {
		t.Fatal(err)
	}
	r, g, b, a := img.At(0, 0).RGBA()
	if r>>8 != 0x10 || g>>8 != 0x20 || b>>8 != 0x30 || a>>8 != 0xff {
		t.Errorf("got rgba(%d,%d,%d,%d), want (16,32,48,255)", r>>8, g>>8, b>>8, a>>8)
	}
}

func TestDecodeKWinFrameRGB32IgnoresAlphaByte(t *testing.T) {
	pix := bgra(0x11, 0x22, 0x33, 0x00)
	results, data := frame(1, 1, 4, qImageRGB32, pix)

	img, err := decodeKWinFrame(data, results)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, a := img.At(0, 0).RGBA(); a>>8 != 0xff {
		t.Errorf("alpha = %d, want 255 — RGB32's top byte is padding", a>>8)
	}
}

func TestDecodeKWinFrameUnpremultiplies(t *testing.T) {
	pix := bgra(0x80, 0x80, 0x80, 0x80)
	results, data := frame(1, 1, 4, qImageARGB32Premultiplied, pix)

	img, err := decodeKWinFrame(data, results)
	if err != nil {
		t.Fatal(err)
	}
	px := img.(*image.NRGBA).NRGBAAt(0, 0)
	if px.R < 0xf0 || px.A != 0x80 {
		t.Errorf("stored rgba(%d,%d,%d,%d); premultiplication was not undone",
			px.R, px.G, px.B, px.A)
	}
}

func TestDecodeKWinFrameHonoursStridePadding(t *testing.T) {
	const w, h, stride = 2, 2, 12
	data := make([]byte, stride*h)
	copy(data[0:], bgra(1, 1, 1, 255))
	copy(data[4:], bgra(2, 2, 2, 255))
	copy(data[stride+0:], bgra(3, 3, 3, 255))
	copy(data[stride+4:], bgra(4, 4, 4, 255))
	results, _ := frame(w, h, stride, qImageARGB32, nil)

	img, err := decodeKWinFrame(data, results)
	if err != nil {
		t.Fatal(err)
	}
	if r, _, _, _ := img.At(0, 1).RGBA(); r>>8 != 3 {
		t.Errorf("second row starts at %d, want 3 — stride padding mishandled", r>>8)
	}
}

func TestDecodeKWinFrameRejectsBadInput(t *testing.T) {
	results, _ := frame(100, 100, 400, qImageARGB32, nil)
	if _, err := decodeKWinFrame(make([]byte, 16), results); err == nil ||
		!strings.Contains(err.Error(), "short frame") {
		t.Errorf("short buffer: %v", err)
	}

	results, _ = frame(0, 0, 0, qImageARGB32, nil)
	if _, err := decodeKWinFrame(nil, results); err == nil ||
		!strings.Contains(err.Error(), "no geometry") {
		t.Errorf("empty geometry: %v", err)
	}

	results, _ = frame(1, 1, 4, 99, nil)
	if _, err := decodeKWinFrame(make([]byte, 4), results); err == nil ||
		!strings.Contains(err.Error(), "unsupported QImage format") {
		t.Errorf("unknown format: %v", err)
	}
}

func TestDecodeKWinFrameDefaultsStrideAndFormat(t *testing.T) {
	results := map[string]dbus.Variant{
		"width":  dbus.MakeVariant(uint32(1)),
		"height": dbus.MakeVariant(uint32(1)),
	}
	img, err := decodeKWinFrame(bgra(9, 9, 9, 0), results)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, 1, 1) {
		t.Errorf("bounds = %v", img.Bounds())
	}
}
