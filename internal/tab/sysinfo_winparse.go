package tab

import "strings"

// parseWindowsVer turns `cmd /c ver` ("Microsoft Windows [Version
// 10.0.26100.6584]") into "Windows 10.0.26100.6584".
func parseWindowsVer(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	out = strings.TrimPrefix(out, "Microsoft ")
	out = strings.Replace(out, "[Version ", "", 1)
	return strings.TrimSuffix(out, "]")
}

// parseRegValue reads the data column of a one-value `reg query` result:
//
//	HKEY_LOCAL_MACHINE\HARDWARE\DESCRIPTION\System\BIOS
//	    SystemProductName    REG_SZ    ROG Zephyrus G16
func parseRegValue(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if strings.HasPrefix(f, "REG_") && i+1 < len(fields) {
				return strings.Join(fields[i+1:], " ")
			}
		}
	}
	return ""
}
