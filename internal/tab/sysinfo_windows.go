//go:build windows

package tab

func osAndModel() (string, string) {
	return parseWindowsVer(cmdLine("cmd", "/c", "ver")),
		parseRegValue(cmdLine("reg", "query", `HKLM\HARDWARE\DESCRIPTION\System\BIOS`, "/v", "SystemProductName"))
}
