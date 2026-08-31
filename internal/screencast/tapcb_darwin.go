//go:build darwin && cgo

package screencast

import "C"

//export recgoClickGo
func recgoClickGo(x, y C.double, button C.int) {
	clickMu.Lock()
	fn := clickFn
	clickMu.Unlock()
	if fn != nil {
		fn(Click{X: float64(x), Y: float64(y), Button: int(button)})
	}
}
