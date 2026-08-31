package tab

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func packageImports(t *testing.T, dir string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	out := map[string][]string{}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			for _, imp := range file.Imports {
				out[filepath.Base(name)] = append(out[filepath.Base(name)],
					strings.Trim(imp.Path.Value, `"`))
			}
		}
	}
	return out
}

// recgo-tab may now push a finished session to an operator-configured host
// (--sync-target), so the old rule -- that it must not import the uploader at
// all -- has become "it must not upstream from the library half, and the CLI
// must say so". The library keeps the absolute ban: tab.* is what runs while a
// recording is live, and nothing there may ship bytes anywhere. The CLI's
// upstream is bounded by two things the tests below pin: it is inert unless a
// target was configured (the binary ships none -- see internal/config), and
// the help text announces it in the same shouted form as every other egress.
func TestTabLibraryDoesNotImportUploader(t *testing.T) {
	for file, imports := range packageImports(t, ".") {
		for _, imp := range imports {
			if strings.Contains(imp, "internal/upload") {
				t.Errorf("./%s imports %s — the tab library must not upstream recordings", file, imp)
			}
		}
	}
}

// buildCLI builds one of the recorders and returns its `-h` output, which is
// where every egress this tool can perform has to be announced.
func helpText(t *testing.T, cli string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), cli)
	build := exec.Command("go", "build", "-o", bin, "../../cmd/"+cli)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", cli, err, out)
	}
	out, _ := exec.Command(bin, "-h").CombinedOutput()
	return string(out)
}

// recgo-browser is the default recorder, recgo-tab its pinned-to-one-tab
// sibling, and recgo-desktop the same recorder pointed at the screen instead
// of a browser; they share defaults and flags, so every promise below has to
// hold for all three.
var sessionCLIs = []string{"recgo-browser", "recgo-tab", "recgo-desktop"}

func TestTabSyncIsAnnouncedInHelp(t *testing.T) {
	// The uploader is only reachable from main.go, where the flag that turns
	// it on is defined and documented.
	for _, cli := range sessionCLIs {
		for file, imports := range packageImports(t, "../../cmd/"+cli) {
			for _, imp := range imports {
				if strings.Contains(imp, "internal/upload") && file != "main.go" {
					t.Errorf("cmd/%s/%s imports %s -- session upstream is wired in main.go only",
						cli, file, imp)
				}
			}
		}

		text := helpText(t, cli)
		if !strings.Contains(text, "PUSHES THE WHOLE SESSION") {
			t.Errorf("%s help text does not warn that --sync-target upstreams the session:\n%s",
				cli, text)
		}
		if !strings.Contains(text, "--no-sync") && !strings.Contains(text, "-no-sync") {
			t.Errorf("%s help text does not offer a way to keep the session local", cli)
		}
	}
}

func TestNetworkEgressIsConfinedToTheSTTBackend(t *testing.T) {
	allowed := map[string]bool{
		"stt.go":    true,
		"cdp.go":    true,
		"title.go":  true,
		"portal.go": true,
	}
	// The websocket client is egress too: cdp.go dials localhost Chrome,
	// portal.go dials the operator-named portal (opt-in, announced). A
	// third importer means a new place bytes can leave from.
	websocketAllowed := map[string]bool{
		"cdp.go":    true,
		"portal.go": true,
	}

	for file, imports := range packageImports(t, ".") {
		for _, imp := range imports {
			if strings.Contains(imp, "gorilla/websocket") && !websocketAllowed[file] {
				t.Errorf("%s imports %s — websocket egress belongs in cdp.go and portal.go only", file, imp)
			}
			if imp != "net/http" && imp != "net" && !strings.Contains(imp, "internal/transcribe") {
				continue
			}
			if !allowed[file] {
				t.Errorf("%s imports %s — new network egress outside the opt-in STT and title backends", file, imp)
			}
		}
	}

	for _, cli := range []string{"recgo-tab", "recgo-browser", "recgo-desktop"} {
		for file, imports := range packageImports(t, "../../cmd/"+cli) {
			for _, imp := range imports {
				if imp == "net/http" || imp == "net" {
					t.Errorf("cmd/%s/%s imports %s -- egress belongs in the opt-in backends, not the CLI", cli, file, imp)
				}
				if strings.Contains(imp, "internal/transcribe") && file != "main.go" {
					t.Errorf("cmd/%s/%s imports %s -- live narration streaming is wired in main.go only", cli, file, imp)
				}
			}
		}
	}
}

func TestDefaultBackendIsAuto(t *testing.T) {
	for _, cli := range sessionCLIs {
		text := helpText(t, cli)

		if !strings.Contains(text, `default "auto"`) {
			t.Errorf("%s --stt-backend does not default to auto:\n%s", cli, text)
		}
		if !strings.Contains(text, "nothing leaves this machine") {
			t.Errorf("%s help text does not state the local-first policy", cli)
		}
		if !strings.Contains(text, "UPLOADS THE AUDIO") {
			t.Errorf("%s help text does not warn that --stt-backend remote uploads audio", cli)
		}
		if !strings.Contains(text, "UPLOADS THE TRANSCRIPT") {
			t.Errorf("%s help text does not warn that --title-backend remote uploads the transcript", cli)
		}
		if !strings.Contains(text, "STREAMS NARRATION AUDIO") {
			t.Errorf("%s help text does not warn that --live with a remote backend streams narration while recording", cli)
		}
		if !strings.Contains(text, "EXPOSES THE OUTPUT ROOT") {
			t.Errorf("%s help text does not warn that --portal exposes the output root to the room", cli)
		}
		if !strings.Contains(text, "the room name is the only credential") {
			t.Errorf("%s help text does not state that the portal room name is the only credential", cli)
		}
	}
}

// recgo-desktop's own promises: video and screenshots never leave the
// machine (only audio ever goes to an STT backend, and only opt-in or under
// auto with no local model), and clicks are screenshotted by default with a
// way to turn that off.
func TestDesktopHelpStatesLocalVideoAndClickShots(t *testing.T) {
	text := helpText(t, "recgo-desktop")
	if !strings.Contains(text, "Video and screenshots stay on this machine") {
		t.Errorf("recgo-desktop help no longer states that video stays local:\n%s", text)
	}
	if !strings.Contains(text, "-click-shots") {
		t.Error("recgo-desktop has no -click-shots flag; per-click screenshots are unreachable or undisableable")
	}
	if !strings.Contains(text, "-focus-shots") {
		t.Error("recgo-desktop has no -focus-shots flag; focus/dialog screenshots are unreachable or undisableable")
	}
	if !strings.Contains(text, "Accessibility") {
		t.Error("recgo-desktop help does not say click capture needs the Accessibility grant on macOS")
	}
}

// recgo-browser is the default recorder, and what makes it the default is
// that it follows the whole browser without being asked. A regression here is
// silent: the session would still record, just not the tabs the person
// actually walked through.
func TestBrowserFollowsEveryTabByDefault(t *testing.T) {
	text := helpText(t, "recgo-browser")
	if !strings.Contains(text, "following you across tabs") {
		t.Errorf("recgo-browser no longer advertises following the browser:\n%s", text)
	}
	if !strings.Contains(text, "--match or --select pins the") {
		t.Error("help text does not say how to pin the recording to one tab")
	}
	for _, pinning := range []string{"-match", "-select"} {
		if !strings.Contains(text, pinning) {
			t.Errorf("recgo-browser has no %s flag; pinning to one tab is unreachable", pinning)
		}
	}
}

func TestTitleBackendFollowsSTTBackend(t *testing.T) {
	for _, cli := range sessionCLIs {
		text := helpText(t, cli)
		if !strings.Contains(text, "default: follow --stt-backend") {
			t.Errorf("%s --title-backend does not default to following --stt-backend:\n%s", cli, text)
		}
	}

	res := GenerateTitle(TitleOptions{}, &Transcript{OK: true,
		Segments: []Segment{{T: 0, EndT: 500, Text: "hello"}}}, nil)
	if res.Title != "" {
		t.Errorf("disabled title backend produced a title %q", res.Title)
	}
	if res.Slug == "" {
		t.Error("disabled title backend produced no fallback slug")
	}
}

func TestOutputPathIsOutsideTheArchiveSweep(t *testing.T) {
	for _, cli := range sessionCLIs {
		text := helpText(t, cli)
		if !strings.Contains(text, "walk-and-talk") {
			t.Errorf("%s default output path is not walk-and-talk:\n%s", cli, text)
		}
		if strings.Contains(text, "archive/recordings") {
			t.Errorf("%s default output path is inside the archive sweep", cli)
		}
	}
}
