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

func TestTabDoesNotImportUploader(t *testing.T) {
	for _, dir := range []string{".", "../../cmd/recgo-tab"} {
		for file, imports := range packageImports(t, dir) {
			for _, imp := range imports {
				if strings.Contains(imp, "internal/upload") {
					t.Errorf("%s/%s imports %s — recgo-tab must not upstream recordings", dir, file, imp)
				}
			}
		}
	}
}

func TestNetworkEgressIsConfinedToTheSTTBackend(t *testing.T) {
	allowed := map[string]bool{
		"stt.go":   true,
		"cdp.go":   true,
		"title.go": true,
	}

	for file, imports := range packageImports(t, ".") {
		for _, imp := range imports {
			if imp != "net/http" && imp != "net" {
				continue
			}
			if !allowed[file] {
				t.Errorf("%s imports %s — new network egress outside the opt-in STT and title backends", file, imp)
			}
		}
	}
}

func TestDefaultBackendIsAuto(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "recgo-tab")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/recgo-tab")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recgo-tab: %v\n%s", err, out)
	}

	help := exec.Command(bin, "-h")
	out, _ := help.CombinedOutput()
	text := string(out)

	if !strings.Contains(text, `default "auto"`) {
		t.Errorf("--stt-backend does not default to auto:\n%s", text)
	}
	if !strings.Contains(text, "nothing leaves this machine") {
		t.Error("help text does not state the local-first policy")
	}
	if !strings.Contains(text, "UPLOADS THE AUDIO") {
		t.Error("help text does not warn that --stt-backend remote uploads audio")
	}
	if !strings.Contains(text, "UPLOADS THE TRANSCRIPT") {
		t.Error("help text does not warn that --title-backend remote uploads the transcript")
	}
}

func TestTitleBackendFollowsSTTBackend(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "recgo-tab")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/recgo-tab")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recgo-tab: %v\n%s", err, out)
	}

	out, _ := exec.Command(bin, "-h").CombinedOutput()
	if !strings.Contains(string(out), "default: follow --stt-backend") {
		t.Errorf("--title-backend does not default to following --stt-backend:\n%s", out)
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
	bin := filepath.Join(t.TempDir(), "recgo-tab")
	build := exec.Command("go", "build", "-o", bin, "../../cmd/recgo-tab")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build recgo-tab: %v\n%s", err, out)
	}

	out, _ := exec.Command(bin, "-h").CombinedOutput()
	text := string(out)
	if !strings.Contains(text, "walk-and-talk") {
		t.Errorf("default output path is not walk-and-talk:\n%s", text)
	}
	if strings.Contains(text, "archive/recordings") {
		t.Error("default output path is inside the archive sweep")
	}
}
