package tab

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eordano/recgo/internal/audio"
)

// ReadCommands drives a recording from stdin: "m"/"mark" flags a moment and
// "sysaudio on|off" toggles the system-audio mix without stopping. Each
// toggle is echoed on stderr and lands in the document as a Note line, so a
// reader knows why what you heard drops out.
func ReadCommands(r io.Reader, mic *Mic, mark func() float64, note func(string) float64) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "m" || line == "mark":
			fmt.Fprintf(os.Stderr, "marked at %.1fs\n", mark()/1000)
		case line == "sysaudio on" || line == "sysaudio off":
			on := line == "sysaudio on"
			if mic == nil {
				fmt.Fprintln(os.Stderr, "system audio: no audio is being captured")
				continue
			}
			if err := mic.SetSystemAudio(on); err != nil {
				fmt.Fprintf(os.Stderr, "system audio: %v\n", err)
				continue
			}
			state := "off"
			if on {
				state = "on"
			}
			note("system audio " + state)
			fmt.Fprintf(os.Stderr, "system audio: %s\n", state)
		}
	}
}

// ResolveMonitor turns the -system-audio flag into a capture source: empty
// keeps the microphone alone, "default" follows the default output.
func ResolveMonitor(flag string) (string, error) {
	switch flag {
	case "":
		return "", nil
	case "default", "auto":
		return audio.DefaultMonitor()
	}
	return flag, nil
}
