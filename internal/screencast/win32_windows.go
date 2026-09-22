//go:build windows

package screencast

import (
	"fmt"
	"image"
	"sync"
	"syscall"
	"unsafe"
)

// Win32 bindings over the standard library's lazy DLL loader: no cgo, no
// golang.org/x/sys, so recgo-desktop.exe cross-compiles from Linux.

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shcore   = syscall.NewLazyDLL("shcore.dll")

	pGetDC                         = user32.NewProc("GetDC")
	pReleaseDC                     = user32.NewProc("ReleaseDC")
	pEnumDisplayMonitors           = user32.NewProc("EnumDisplayMonitors")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	pSetWindowsHookExW             = user32.NewProc("SetWindowsHookExW")
	pUnhookWindowsHookEx           = user32.NewProc("UnhookWindowsHookEx")
	pCallNextHookEx                = user32.NewProc("CallNextHookEx")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pPeekMessageW                  = user32.NewProc("PeekMessageW")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostThreadMessageW            = user32.NewProc("PostThreadMessageW")
	pGetForegroundWindow           = user32.NewProc("GetForegroundWindow")
	pGetWindowTextW                = user32.NewProc("GetWindowTextW")
	pGetClassNameW                 = user32.NewProc("GetClassNameW")
	pEnumWindows                   = user32.NewProc("EnumWindows")
	pIsWindowVisible               = user32.NewProc("IsWindowVisible")
	pGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pGetWindowThreadProcessId      = user32.NewProc("GetWindowThreadProcessId")
	pWindowFromPoint               = user32.NewProc("WindowFromPoint")
	pGetAncestor                   = user32.NewProc("GetAncestor")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pClientToScreen                = user32.NewProc("ClientToScreen")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")

	pGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	pGetCurrentProcessId = kernel32.NewProc("GetCurrentProcessId")

	pGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000

	monitorInfoPrimary = 1
	mdtEffectiveDPI    = 0

	gaRoot         = 2
	gwlExStyle     = -20
	wsExToolWindow = 0x00000080
)

type rect struct{ Left, Top, Right, Bottom int32 }

func (r rect) w() int { return int(r.Right - r.Left) }
func (r rect) h() int { return int(r.Bottom - r.Top) }

type point struct{ X, Y int32 }

type monitorInfoEx struct {
	Size    uint32
	Monitor rect
	Work    rect
	Flags   uint32
	Device  [32]uint16
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [3]uint32
}

type monitor struct {
	handle  uintptr
	bounds  rect
	primary bool
	name    string
	dpi     int
}

func i32(v int32) uintptr { return uintptr(uint32(v)) }

var dpiOnce sync.Once

// ensureDPIAware makes every coordinate this process sees -- hook points,
// monitor bounds, GetDC pixels -- physical, so click positions line up
// with the screenshots on a 125% display. Per-monitor v2 is Windows 10
// 1703+; the system-wide fallback is enough for one display.
func ensureDPIAware() {
	dpiOnce.Do(func() {
		const perMonitorAwareV2 = ^uintptr(3) // DPI_AWARENESS_CONTEXT(-4)
		if r, _, _ := pSetProcessDpiAwarenessContext.Call(perMonitorAwareV2); r == 0 {
			pSetProcessDPIAware.Call()
		}
	})
}

var (
	enumMonitorsCB   uintptr
	enumMonitorsOnce sync.Once
	enumMu           sync.Mutex
	enumMonitorsOut  []monitor
)

func monitorCallback(hmon, hdc, lprc, lparam uintptr) uintptr {
	mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if r, _, _ := pGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return 1
	}
	m := monitor{handle: hmon, bounds: mi.Monitor, primary: mi.Flags&monitorInfoPrimary != 0,
		name: syscall.UTF16ToString(mi.Device[:])}
	var dx, dy uint32
	if r, _, _ := pGetDpiForMonitor.Call(hmon, mdtEffectiveDPI,
		uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		m.dpi = int(dx)
	}
	enumMonitorsOut = append(enumMonitorsOut, m)
	return 1
}

// monitors lists the attached displays in EnumDisplayMonitors order, which
// is also how -screen numbers them.
func monitors() []monitor {
	ensureDPIAware()
	enumMonitorsOnce.Do(func() { enumMonitorsCB = syscall.NewCallback(monitorCallback) })
	enumMu.Lock()
	defer enumMu.Unlock()
	enumMonitorsOut = nil
	pEnumDisplayMonitors.Call(0, 0, enumMonitorsCB, 0)
	out := make([]monitor, len(enumMonitorsOut))
	copy(out, enumMonitorsOut)
	return out
}

func (m monitor) info() DisplayInfo {
	pw, ph := m.bounds.w(), m.bounds.h()
	return DisplayInfo{
		W: logicalSize(pw, m.dpi), H: logicalSize(ph, m.dpi),
		PixelW: pw, PixelH: ph, Main: m.primary, Name: m.name,
	}
}

func virtualScreen() rect {
	x, _, _ := pGetSystemMetrics.Call(smXVirtualScreen)
	y, _, _ := pGetSystemMetrics.Call(smYVirtualScreen)
	w, _, _ := pGetSystemMetrics.Call(smCXVirtualScreen)
	h, _, _ := pGetSystemMetrics.Call(smCYVirtualScreen)
	return rect{Left: int32(x), Top: int32(y), Right: int32(x) + int32(w), Bottom: int32(y) + int32(h)}
}

// captureRect copies a screen rectangle through GDI: a compatible bitmap
// BitBlt'd from the screen DC (CAPTUREBLT includes layered windows), read
// back as a top-down 32-bit DIB.
func captureRect(r rect) (image.Image, error) {
	ensureDPIAware()
	w, h := r.w(), r.h()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("empty capture rectangle %+v", r)
	}
	screen, _, err := pGetDC.Call(0)
	if screen == 0 {
		return nil, fmt.Errorf("GetDC: %v", err)
	}
	defer pReleaseDC.Call(0, screen)

	mem, _, err := pCreateCompatibleDC.Call(screen)
	if mem == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC: %v", err)
	}
	defer pDeleteDC.Call(mem)

	bmp, _, err := pCreateCompatibleBitmap.Call(screen, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("CreateCompatibleBitmap: %v", err)
	}
	defer pDeleteObject.Call(bmp)

	old, _, _ := pSelectObject.Call(mem, bmp)
	defer pSelectObject.Call(mem, old)

	if ok, _, err := pBitBlt.Call(mem, 0, 0, uintptr(w), uintptr(h),
		screen, i32(r.Left), i32(r.Top), srcCopy|captureBlt); ok == 0 {
		return nil, fmt.Errorf("BitBlt: %v", err)
	}

	bi := bitmapInfo{Header: bitmapInfoHeader{
		Size:  uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32,
	}}
	buf := make([]byte, w*h*4)
	lines, _, err := pGetDIBits.Call(mem, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	if lines == 0 {
		return nil, fmt.Errorf("GetDIBits: %v", err)
	}
	return bgraToRGBA(w, h, buf)
}

func windowText(hwnd uintptr) string {
	var buf [512]uint16
	n, _, _ := pGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func windowClass(hwnd uintptr) string {
	var buf [256]uint16
	n, _, _ := pGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

func windowRect(hwnd uintptr) rect {
	var r rect
	pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return r
}

func windowPID(hwnd uintptr) uint32 {
	var pid uint32
	pGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}
