//go:build darwin

package tab

func osAndModel() (string, string) {
	os := ""
	if v := cmdLine("sw_vers", "-productVersion"); v != "" {
		os = "macOS " + v
		if b := cmdLine("sw_vers", "-buildVersion"); b != "" {
			os += " (" + b + ")"
		}
	}
	return os, cmdLine("sysctl", "-n", "hw.model")
}
