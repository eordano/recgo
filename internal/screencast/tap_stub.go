//go:build (!darwin && !linux) || (darwin && !cgo)

package screencast

import (
	"fmt"
	"runtime"
)

func StartClickTap(onClick func(Click)) (func(), error) {
	if runtime.GOOS == "darwin" {
		return nil, fmt.Errorf("this build has no event tap (compiled without cgo)")
	}
	return nil, fmt.Errorf("click capture is not supported on %s yet — the portal screencast carries no input events", runtime.GOOS)
}

func DisplayList() []DisplayInfo { return nil }

func StartWindowWatch(onFocus func(string), onWindow func(string)) (func(), error) {
	if runtime.GOOS == "darwin" {
		return nil, fmt.Errorf("this build has no window watcher (compiled without cgo)")
	}
	return nil, fmt.Errorf("window watching is not supported on %s yet — the portal screencast carries no window list", runtime.GOOS)
}
