package screencast

import (
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	kwinBus   = "org.kde.KWin.ScreenShot2"
	kwinPath  = "/org/kde/KWin/ScreenShot2"
	kwinIface = "org.kde.KWin.ScreenShot2"
)

const (
	qImageRGB32               = 4
	qImageARGB32              = 5
	qImageARGB32Premultiplied = 6
)

func KWinAvailable(conn *dbus.Conn) bool {
	var names []string
	if err := conn.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
		return false
	}
	for _, n := range names {
		if n == kwinBus {
			return true
		}
	}
	return false
}

func KWinShot(conn *dbus.Conn, method string, includeCursor bool, timeout time.Duration) (image.Image, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}
	defer r.Close()

	type readResult struct {
		data []byte
		err  error
	}
	done := make(chan readResult, 1)
	go func() {
		b, err := io.ReadAll(r)
		done <- readResult{b, err}
	}()

	opts := map[string]dbus.Variant{
		"include-cursor":    dbus.MakeVariant(includeCursor),
		"native-resolution": dbus.MakeVariant(true),
	}

	var results map[string]dbus.Variant
	call := conn.Object(kwinBus, kwinPath).Call(
		kwinIface+"."+method, 0, opts, dbus.UnixFD(w.Fd()))
	w.Close()

	if err := call.Store(&results); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}

	var res readResult
	select {
	case res = <-done:
	case <-time.After(timeout):
		return nil, fmt.Errorf("%s: timed out reading pixels", method)
	}
	if res.err != nil {
		return nil, fmt.Errorf("%s: read pixels: %w", method, res.err)
	}

	return decodeKWinFrame(res.data, results)
}

func decodeKWinFrame(data []byte, results map[string]dbus.Variant) (image.Image, error) {
	get := func(k string) (uint32, bool) {
		v, ok := results[k]
		if !ok {
			return 0, false
		}
		var n uint32
		if err := v.Store(&n); err != nil {
			return 0, false
		}
		return n, true
	}

	width, okW := get("width")
	height, okH := get("height")
	stride, okS := get("stride")
	format, okF := get("format")
	if !okW || !okH || width == 0 || height == 0 {
		return nil, fmt.Errorf("KWin reported no geometry (width=%d height=%d)", width, height)
	}
	if !okS || stride == 0 {
		stride = width * 4
	}
	if !okF {
		format = qImageRGB32
	}

	switch format {
	case qImageRGB32, qImageARGB32, qImageARGB32Premultiplied:
	default:
		return nil, fmt.Errorf("unsupported QImage format %d from KWin", format)
	}

	need := int(stride) * int(height)
	if len(data) < need {
		return nil, fmt.Errorf("short frame: got %d bytes, need %d (%dx%d stride %d)",
			len(data), need, width, height, stride)
	}

	img := image.NewNRGBA(image.Rect(0, 0, int(width), int(height)))
	for y := 0; y < int(height); y++ {
		row := data[y*int(stride):]
		out := img.Pix[y*img.Stride:]
		for x := 0; x < int(width); x++ {
			px := binary.LittleEndian.Uint32(row[x*4:])
			a := uint8(px >> 24)
			r := uint8(px >> 16)
			g := uint8(px >> 8)
			b := uint8(px)

			if format == qImageRGB32 {
				a = 0xff
			}
			if format == qImageARGB32Premultiplied && a != 0 && a != 0xff {
				r = uint8(min(255, int(r)*255/int(a)))
				g = uint8(min(255, int(g)*255/int(a)))
				b = uint8(min(255, int(b)*255/int(a)))
			}

			o := x * 4
			out[o], out[o+1], out[o+2], out[o+3] = r, g, b, a
		}
	}
	return img, nil
}
