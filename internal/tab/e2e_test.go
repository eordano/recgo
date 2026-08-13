package tab

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const fixturePage = `<!doctype html>
<meta charset="utf-8">
<title>recgo-tab fixture</title>
<style>
  body { font: 16px system-ui, sans-serif; margin: 40px; background: #fff; color: #111; }
  button { font-size: 18px; padding: 14px 22px; margin: 8px 8px 8px 0; border-radius: 8px;
           border: 1px solid #888; background: #f4f4f4; cursor: pointer; }
  #status { margin-top: 24px; padding: 16px; border: 2px solid #ccc; border-radius: 8px;
            min-height: 48px; font-family: ui-monospace, monospace; }
</style>
<h1>Document editor</h1>
<button data-testid="save">Save</button>
<button data-testid="load">Load</button>
<button data-testid="inert">Does nothing visible</button>
<div id="status">ready</div>
<script>
  const status = document.getElementById('status');
  document.querySelector('[data-testid=save]').addEventListener('click', async () => {
    // Deliberately broken: the server 500s with HTML, so res.json() rejects, the
    // catch yields undefined, and reading .id off it throws.
    const res = await fetch('/api/documents/4821', { method: 'POST' });
    const body = await res.json().catch(() => undefined);
    status.textContent = 'saving…';
    console.error('save failed', res.status);
    status.textContent = 'id ' + body.id;
  });
  document.querySelector('[data-testid=load]').addEventListener('click', async () => {
    const res = await fetch('/api/documents/4821');
    status.textContent = 'loaded ' + (await res.json()).title;
  });
  document.querySelector('[data-testid=inert]').addEventListener('click', () => {
    console.log('inert clicked');   // no DOM mutation: exercises the static-page path
  });
  const ws = new WebSocket('ws://' + location.host + '/hmr');
  ws.addEventListener('message', (ev) => {
    const m = JSON.parse(ev.data);
    if (m.type === 'update') document.title = 'updated ' + m.updates[0].path;
  });
</script>`

type fixture struct {
	server *httptest.Server
	mu     sync.Mutex
	socks  []*websocket.Conn
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, fixturePage)
	})

	mux.HandleFunc("/api/documents/4821", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "<html><body>500</body></html>")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":4821,"title":"Q3 planning"}`)
	})

	mux.HandleFunc("/hmr/fire", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.broadcast(string(body))
		w.WriteHeader(http.StatusOK)
	})

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux.HandleFunc("/hmr", func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"connected"}`))
		f.mu.Lock()
		f.socks = append(f.socks, conn)
		f.mu.Unlock()

		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(func() {
		f.mu.Lock()
		for _, c := range f.socks {
			c.Close()
		}
		f.mu.Unlock()
		f.server.Close()
	})
	return f
}

func (f *fixture) broadcast(payload string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.socks {
		c.WriteMessage(websocket.TextMessage, []byte(payload))
	}
}

func (f *fixture) fire(t *testing.T, payload string) {
	t.Helper()
	res, err := http.Post(f.server.URL+"/hmr/fire", "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	time.Sleep(300 * time.Millisecond)
}

func launchTestChromium(t *testing.T, bin string, port int) {
	t.Helper()
	profile := t.TempDir()
	cmd := exec.Command(bin,
		fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profile,
		"--no-first-run", "--no-default-browser-check",
		"--window-size=1280,800",
		"--headless=new", "--disable-gpu", "--no-sandbox",
		"about:blank")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch chromium: %v", err)
	}
	t.Cleanup(func() {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		cmd.Wait()
	})

	for i := 0; i < 100; i++ {
		if targets, err := ListTargets(port); err == nil {
			for _, tg := range targets {
				if tg.Type == "page" {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("chromium exposed no page target")
}

func clickSelector(t *testing.T, cdp *CDP, selector string) {
	t.Helper()
	var res struct {
		Result struct {
			Value *struct{ X, Y float64 } `json:"value"`
		} `json:"result"`
	}
	expr := fmt.Sprintf(`(() => { const e = document.querySelector(%q);
		if (!e) return null; const r = e.getBoundingClientRect();
		return {X: r.x + r.width/2, Y: r.y + r.height/2}; })()`, selector)
	if err := cdp.Send("Runtime.evaluate",
		map[string]any{"expression": expr, "returnByValue": true}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Result.Value == nil {
		t.Fatalf("selector not found: %s", selector)
	}

	base := map[string]any{
		"x": res.Result.Value.X, "y": res.Result.Value.Y,
		"button": "left", "clickCount": 1, "buttons": 1,
	}
	press := map[string]any{"type": "mousePressed"}
	release := map[string]any{"type": "mouseReleased"}
	for k, v := range base {
		press[k], release[k] = v, v
	}
	if err := cdp.Send("Input.dispatchMouseEvent", press, nil); err != nil {
		t.Fatal(err)
	}
	if err := cdp.Send("Input.dispatchMouseEvent", release, nil); err != nil {
		t.Fatal(err)
	}
}

func TestEndToEnd(t *testing.T) {
	chromium := os.Getenv("CHROMIUM")
	if chromium == "" {
		t.Skip("set CHROMIUM to run the end-to-end test")
	}

	fx := newFixture(t)
	const port = 9422
	launchTestChromium(t, chromium, port)

	cdp, target, err := Attach(port, "")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer cdp.Close()
	if target.Type != "page" {
		t.Fatalf("attached to a %s target", target.Type)
	}

	outDir := t.TempDir()
	clock := NewClock()
	rec := NewRecorder(cdp, clock, outDir)

	offset, errMs, err := rec.Start()
	if err != nil {
		t.Fatalf("recorder start: %v", err)
	}
	if offset == 0 {
		t.Error("browser clock offset was never measured")
	}
	if errMs > 50 {
		t.Errorf("calibration error %.1fms is implausibly high", errMs)
	}

	if err := cdp.Send("Page.navigate", map[string]any{"url": fx.server.URL + "/"}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)

	clickSelector(t, cdp, "[data-testid=save]")
	time.Sleep(1200 * time.Millisecond)
	clickSelector(t, cdp, "[data-testid=load]")
	time.Sleep(800 * time.Millisecond)

	fx.fire(t, `{"type":"update","updates":[{"type":"js-update","path":"/src/components/StatusBox.tsx","acceptedPath":"/src/components/StatusBox.tsx"}]}`)
	clickSelector(t, cdp, "[data-testid=inert]")
	time.Sleep(1000 * time.Millisecond)

	rec.Mark("something looked wrong here")
	fx.fire(t, `{"type":"full-reload","path":"/src/main.tsx"}`)
	time.Sleep(800 * time.Millisecond)

	events := rec.Stop()

	var clicks []Event
	for _, e := range events {
		if e.Kind == "click" {
			clicks = append(clicks, e)
		}
	}
	if len(clicks) != 3 {
		t.Fatalf("captured %d clicks, want 3", len(clicks))
	}
	wantSel := []string{`[data-testid="save"]`, `[data-testid="load"]`, `[data-testid="inert"]`}
	for i, c := range clicks {
		if c.Elem == nil || c.Elem.Selector != wantSel[i] {
			t.Errorf("click %d selector = %v, want %s", i+1, c.Elem, wantSel[i])
		}
	}

	for _, c := range clicks {
		if len(c.Shots) != 3 {
			t.Errorf("click %d has %d shots, want 3", c.Seq, len(c.Shots))
			continue
		}
		for _, s := range c.Shots {
			if s.File == "" {
				t.Errorf("click %d %s shot has no file", c.Seq, s.Label)
				continue
			}
			st, err := os.Stat(filepath.Join(outDir, s.File))
			if err != nil || st.Size() < 1000 {
				t.Errorf("click %d %s shot is missing or trivial", c.Seq, s.Label)
			}
			if s.OffsetMs <= 0 && !s.Definitive {
				t.Errorf("click %d %s shot should have been proven", c.Seq, s.Label)
			}
		}
		if c.FullShot == "" {
			t.Errorf("click %d has no full-resolution PNG", c.Seq)
		}
	}

	var hmrTypes []string
	var hmrFiles []string
	for _, e := range events {
		if e.Kind == "hmr" {
			hmrTypes = append(hmrTypes, e.Type)
			hmrFiles = append(hmrFiles, e.Files...)
		}
	}
	for _, want := range []string{"connected", "update", "full-reload"} {
		if !strings.Contains(strings.Join(hmrTypes, ","), want) {
			t.Errorf("missing HMR %q; saw %v", want, hmrTypes)
		}
	}
	if !strings.Contains(strings.Join(hmrFiles, ","), "StatusBox.tsx") {
		t.Errorf("HMR update lost its file path; saw %v", hmrFiles)
	}

	wsRaw, err := os.ReadFile(filepath.Join(outDir, "logs", "websocket-frames.jsonl"))
	if err != nil || len(strings.TrimSpace(string(wsRaw))) == 0 {
		t.Error("raw websocket frames were not archived")
	}

	var saw500, sawTypeError bool
	for _, e := range events {
		if e.T <= clicks[0].T || e.T > clicks[0].T+2000 {
			continue
		}
		if e.Kind == "network-error" && e.Status == 500 {
			saw500 = true
		}
		if e.Kind == "exception" && strings.Contains(e.Text, "TypeError") {
			sawTypeError = true
		}
	}
	if !saw500 {
		t.Error("save click was not followed by a 500")
	}
	if !sawTypeError {
		t.Error("save click was not followed by a TypeError")
	}

	marks := 0
	for _, e := range events {
		if e.Kind == "mark" {
			marks++
		}
	}
	if marks != 1 {
		t.Errorf("recorded %d marks, want 1", marks)
	}
	if clock.Report().CDPMonotonicToWallMs == nil {
		t.Error("CDP monotonic epoch was never learned")
	}

	slug := DeterministicSlug(events)
	if slug == "" || slug == "session" {
		t.Errorf("slug = %q, expected something derived from the session", slug)
	}
	session, err := Pack(outDir, events, clock, nil, Meta{
		Slug: slug, Title: "the save button explodes", StartedISO: time.Now().UTC().Format(time.RFC3339),
		DurationMs: clock.Now(), TargetURL: fx.server.URL, Tool: "recgo-tab-test",
		Cwd: "/tmp/project",
	}, PackOptions{JSON: true})
	if err != nil {
		t.Fatalf("pack: %v", err)
	}
	if session.Counts.Clicks != 3 {
		t.Errorf("session counts %d clicks", session.Counts.Clicks)
	}

	md, err := os.ReadFile(filepath.Join(outDir, "SESSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# Session: the save button explodes", "Folder: /tmp/project",
		`Click: `, `[data-testid="save"]`, "StatusBox.tsx", "Error:",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("SESSION.md missing %q", want)
		}
	}
	for _, unwanted := range []string{"## Clicks", "Screen state", "Hot module reloads"} {
		if strings.Contains(string(md), unwanted) {
			t.Errorf("SESSION.md still carries the old section %q", unwanted)
		}
	}
	if st, err := os.Stat(filepath.Join(outDir, "session.json")); err != nil || st.Size() < 100 {
		t.Error("session.json missing or too small under --json")
	}
	for _, f := range []string{"FEEDBACK.md", "AGENT.md", "clock.json"} {
		if _, err := os.Stat(filepath.Join(outDir, f)); err == nil {
			t.Errorf("%s should no longer be written", f)
		}
	}
}
