package tab

import (
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
)

// Display is one attached display as recorded in the session: bounds in
// points, the pixel scale, and whether it is the main display.
type Display struct {
	W     int     `json:"w"`
	H     int     `json:"h"`
	Scale float64 `json:"scale,omitempty"`
	Main  bool    `json:"main,omitempty"`
}

// SystemInfo describes the machine a session was recorded on. It goes into
// SESSION.md and session.json so a session read elsewhere still says where
// and on what it happened. Everything here is collected locally; nothing is
// uploaded beyond the session itself.
type SystemInfo struct {
	Hostname string    `json:"hostname,omitempty"`
	User     string    `json:"user,omitempty"`
	OS       string    `json:"os,omitempty"`
	Kernel   string    `json:"kernel,omitempty"`
	Arch     string    `json:"arch,omitempty"`
	Model    string    `json:"model,omitempty"`
	Displays []Display `json:"displays,omitempty"`
}

// CollectSystemInfo gathers host facts best-effort: a field that cannot be
// read is left empty rather than failing the recording. Displays are the
// caller's to fill — only the desktop recorder can see them.
func CollectSystemInfo() *SystemInfo {
	s := &SystemInfo{Arch: runtime.GOARCH}
	s.Hostname, _ = os.Hostname()
	if u, err := user.Current(); err == nil {
		s.User = u.Username
	} else {
		s.User = os.Getenv("USER")
	}
	if out := cmdLine("uname", "-sr"); out != "" {
		s.Kernel = out
	}
	s.OS, s.Model = osAndModel()
	return s
}

func cmdLine(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
