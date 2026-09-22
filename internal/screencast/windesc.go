package screencast

import "strings"

// windowDesc names a window for a Focus or Window line from its title and
// its class (KWin's resource class, a Win32 window class): the title alone
// when it already says what the app is, the class alone for an untitled
// window, both otherwise.
func windowDesc(caption, class string) string {
	caption = strings.TrimSpace(caption)
	class = strings.TrimSpace(class)
	switch {
	case caption == "" && class == "":
		return "(untitled)"
	case class == "" || strings.Contains(strings.ToLower(caption), strings.ToLower(class)):
		return caption
	case caption == "":
		return class
	}
	return class + ": " + caption
}
