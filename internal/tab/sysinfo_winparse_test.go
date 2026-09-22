package tab

import "testing"

func TestParseWindowsVer(t *testing.T) {
	if got := parseWindowsVer("\r\nMicrosoft Windows [Version 10.0.26100.6584]\r\n"); got != "Windows 10.0.26100.6584" {
		t.Errorf("parseWindowsVer = %q", got)
	}
	if got := parseWindowsVer(""); got != "" {
		t.Errorf("empty ver = %q", got)
	}
}

func TestParseRegValue(t *testing.T) {
	out := "\r\nHKEY_LOCAL_MACHINE\\HARDWARE\\DESCRIPTION\\System\\BIOS\r\n    SystemProductName    REG_SZ    ROG Zephyrus G16 GU605MV_GU605MV\r\n\r\n"
	if got := parseRegValue(out); got != "ROG Zephyrus G16 GU605MV_GU605MV" {
		t.Errorf("parseRegValue = %q", got)
	}
	if got := parseRegValue("ERROR: The system was unable to find the specified registry key or value."); got != "" {
		t.Errorf("error output parsed as %q", got)
	}
}
