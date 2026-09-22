//go:build windows

package screencast

import "unsafe"

// WindowAt resolves the screen point to the root window under it.
func WindowAt(x, y float64) (WindowRect, bool) {
	ensureDPIAware()
	// POINT travels by value in one 64-bit register: x low, y high.
	pt := uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32
	hwnd, _, _ := pWindowFromPoint.Call(pt)
	if hwnd == 0 {
		return WindowRect{}, false
	}
	if root, _, _ := pGetAncestor.Call(hwnd, gaRoot); root != 0 {
		hwnd = root
	}
	var client rect
	if r, _, _ := pGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); r == 0 {
		return WindowRect{}, false
	}
	var origin point
	if r, _, _ := pClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin))); r == 0 {
		return WindowRect{}, false
	}
	return WindowRect{
		Title: windowText(hwnd), Class: windowClass(hwnd),
		Left: float64(origin.X), Top: float64(origin.Y),
		Width: float64(client.w()), Height: float64(client.h()),
	}, true
}
