package screencast

import "testing"

func TestWindowDesc(t *testing.T) {
	for _, c := range []struct{ caption, class, want string }{
		{"~ — Konsole", "konsole", "~ — Konsole"},
		{"Settings", "org.kde.systemsettings", "org.kde.systemsettings: Settings"},
		{"", "firefox", "firefox"},
		{"", "", "(untitled)"},
		{"Downloads", "CabinetWClass", "CabinetWClass: Downloads"},
		{"recgo - Chromium", "Chrome_WidgetWin_1", "Chrome_WidgetWin_1: recgo - Chromium"},
	} {
		if got := windowDesc(c.caption, c.class); got != c.want {
			t.Errorf("windowDesc(%q,%q) = %q want %q", c.caption, c.class, got, c.want)
		}
	}
}
