//go:build !darwin

package tab

import (
	"os"
	"strings"
)

func osAndModel() (string, string) {
	name := ""
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				name = strings.Trim(v, `"`)
				break
			}
		}
	}
	model := ""
	if data, err := os.ReadFile("/sys/devices/virtual/dmi/id/product_name"); err == nil {
		model = strings.TrimSpace(string(data))
	}
	return name, model
}
