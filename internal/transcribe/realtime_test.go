package transcribe

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func fakeFFmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestStartEmitsErrWhenFFmpegDies(t *testing.T) {
	fakeFFmpeg(t, "#!/bin/sh\necho 'boom: no such device' >&2\nexit 1\n")

	s := New(context.Background(), "0", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatal("updates channel closed before error state")
			}
			if st.Err == nil {
				continue
			}
			msg := st.Err.Error()
			if !strings.Contains(msg, "ffmpeg exited") {
				t.Errorf("error %q does not mention ffmpeg exit", msg)
			}
			if !strings.Contains(msg, "boom: no such device") {
				t.Errorf("error %q does not carry stderr tail", msg)
			}
			return
		case <-deadline:
			t.Fatal("no State with Err on Updates() within 2s of ffmpeg death")
		}
	}
}

func TestStopAfterFFmpegDeathDoesNotHang(t *testing.T) {
	fakeFFmpeg(t, "#!/bin/sh\nexit 1\n")

	s := New(context.Background(), "0", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-s.procDone:
	case <-time.After(2 * time.Second):
		t.Fatal("ffmpeg monitor did not finish within 2s")
	}

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung after ffmpeg already exited")
	}
}

func TestStartArgv(t *testing.T) {
	dir := fakeFFmpeg(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_FFMPEG_ARGV\"\nexit 1\n")
	argvFile := filepath.Join(dir, "argv")
	t.Setenv("FAKE_FFMPEG_ARGV", argvFile)

	s := New(context.Background(), "3", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	select {
	case <-s.procDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fake ffmpeg did not exit within 2s")
	}

	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake ffmpeg did not record argv: %v", err)
	}
	argv := strings.Join(strings.Split(strings.TrimSpace(string(data)), "\n"), " ")

	if runtime.GOOS == "darwin" {
		if !strings.Contains(argv, "-f avfoundation -i :3") {
			t.Errorf("darwin argv %q missing avfoundation input", argv)
		}
		if strings.Contains(argv, "pulse") {
			t.Errorf("darwin argv %q must not use pulse", argv)
		}
	} else {
		if !strings.Contains(argv, "-f pulse -i 3") {
			t.Errorf("linux argv %q missing pulse input", argv)
		}
	}
	if !strings.Contains(argv, "-ac 1 -ar 16000 -f s16le -loglevel error -") {
		t.Errorf("argv %q missing output args", argv)
	}
}

func TestFeedSessionTranscribesPushedPCM(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("bad multipart: %v", err)
		}
		mu.Lock()
		prompts = append(prompts, r.FormValue("prompt"))
		mu.Unlock()
		w.Write([]byte(`{"text":"hello there"}`))
	}))
	defer srv.Close()

	s := NewFeed(context.Background(), Config{Endpoint: srv.URL})
	s.interval = 50 * time.Millisecond
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	s.Feed(make([]byte, sampleRate*bytesPerSample))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatal("updates channel closed before a pass")
			}
			if st.Err != nil {
				t.Fatal(st.Err)
			}
			if st.PassText == "" {
				continue
			}
			if st.PassText != "hello there" || st.Locked != "hello there" {
				t.Errorf("pass %q locked %q", st.PassText, st.Locked)
			}
			if st.PassStartSample != 0 || st.PassEndSample != sampleRate {
				t.Errorf("pass window %d..%d, want 0..%d",
					st.PassStartSample, st.PassEndSample, sampleRate)
			}
			return
		case <-deadline:
			t.Fatal("no transcription pass within 3s of feeding PCM")
		}
	}
}

func TestFeedSessionLocalBackendTranscribesAndThreadsPrompt(t *testing.T) {
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "whisper-cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"" + argvLog + "\"\nprintf '@@\\n' >> \"" + argvLog + "\"\necho ' hello local '\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	s := NewFeed(context.Background(), Config{LocalBin: bin, LocalModel: "/models/ggml-test.bin"})
	s.interval = 50 * time.Millisecond
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	s.Feed(make([]byte, sampleRate*bytesPerSample))

	sawFirst := false
	deadline := time.After(3 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatal("updates channel closed before a pass")
			}
			if st.Err != nil {
				t.Fatal(st.Err)
			}
			if st.PassText == "" {
				continue
			}
			if st.PassText != "hello local" {
				t.Errorf("pass %q, want %q", st.PassText, "hello local")
			}
			if !sawFirst {
				sawFirst = true
				s.Feed(make([]byte, sampleRate*bytesPerSample))
				continue
			}
			raw, err := os.ReadFile(argvLog)
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(raw)), "@@")
			if len(calls) < 2 {
				t.Fatalf("expected 2 whisper-cli calls, got %d", len(calls))
			}
			if !strings.Contains(calls[0], "-m\n/models/ggml-test.bin") {
				t.Errorf("first call missing model arg:\n%s", calls[0])
			}
			if strings.Contains(calls[0], "--prompt") {
				t.Errorf("first call should have no prompt:\n%s", calls[0])
			}
			if !strings.Contains(calls[1], "--prompt\nhello local") {
				t.Errorf("second call does not thread locked text as prompt:\n%s", calls[1])
			}
			return
		case <-deadline:
			t.Fatal("no local transcription pass within 3s")
		}
	}
}

func TestStopDuringEmitDoesNotPanic(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := NewFeed(context.Background(), Config{Endpoint: "http://127.0.0.1:0"})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				s.emit(State{PassText: "x"})
			}
		}()
		go func() {
			defer wg.Done()
			s.Stop()
		}()
		wg.Wait()
	}
}

func TestFirstPassFailureReachesUpdates(t *testing.T) {
	fails := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fails++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	s := NewFeed(context.Background(), Config{Endpoint: srv.URL})
	s.interval = 50 * time.Millisecond
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	s.Feed(make([]byte, sampleRate*bytesPerSample))

	deadline := time.After(3 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatal("updates closed before an error state")
			}
			if st.Err == nil {
				continue
			}
			if !strings.Contains(st.Err.Error(), "live transcription pass failed") {
				t.Errorf("error %q does not identify a pass failure", st.Err)
			}
			return
		case <-deadline:
			t.Fatal("no State with Err despite every pass failing")
		}
	}
}

func TestNoBackendConfiguredIsAnError(t *testing.T) {
	s := NewFeed(context.Background(), Config{})
	if err := s.StartFeed(); err == nil {
		t.Error("StartFeed with no backend should fail")
	}
	s2 := NewFeed(context.Background(), Config{LocalModel: "/models/ggml-test.bin"})
	if err := s2.StartFeed(); err != nil {
		t.Errorf("StartFeed with a local model should work: %v", err)
	}
	s2.Stop()
}

func TestFeedSessionThreadsLockedTextAsPrompt(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(32 << 20)
		mu.Lock()
		n := len(prompts)
		prompts = append(prompts, r.FormValue("prompt"))
		mu.Unlock()
		fmt.Fprintf(w, `{"text":"pass %d"}`, n)
	}))
	defer srv.Close()

	s := NewFeed(context.Background(), Config{Endpoint: srv.URL})
	s.interval = 50 * time.Millisecond
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	s.Feed(make([]byte, sampleRate*bytesPerSample))
	deadline := time.After(3 * time.Second)
	for {
		var st State
		var ok bool
		select {
		case st, ok = <-s.Updates():
		case <-deadline:
			t.Fatal("second pass never arrived")
		}
		if !ok {
			t.Fatal("updates channel closed early")
		}
		if st.PassText == "pass 0" {
			s.Feed(make([]byte, sampleRate*bytesPerSample))
		}
		if st.PassText == "pass 1" {
			if st.PassStartSample != sampleRate || st.PassEndSample != 2*sampleRate {
				t.Errorf("second window %d..%d", st.PassStartSample, st.PassEndSample)
			}
			mu.Lock()
			defer mu.Unlock()
			if prompts[0] != "" || prompts[1] != "pass 0" {
				t.Errorf("prompts = %q, want [\"\", \"pass 0\"]", prompts)
			}
			return
		}
	}
}

func TestPromptRejectingEndpointDegradesToPromptless(t *testing.T) {
	var mu sync.Mutex
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(32 << 20)
		mu.Lock()
		n := len(prompts)
		prompts = append(prompts, r.FormValue("prompt"))
		mu.Unlock()
		if r.FormValue("prompt") != "" {
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"message":"prompt is not supported by this build"}}`))
			return
		}
		fmt.Fprintf(w, `{"text":"pass %d"}`, n)
	}))
	defer srv.Close()

	s := NewFeed(context.Background(), Config{Endpoint: srv.URL})
	s.interval = 50 * time.Millisecond
	if err := s.StartFeed(); err != nil {
		t.Fatal(err)
	}
	defer s.Stop()

	s.Feed(make([]byte, sampleRate*bytesPerSample))
	deadline := time.After(3 * time.Second)
	for {
		var st State
		var ok bool
		select {
		case st, ok = <-s.Updates():
		case <-deadline:
			t.Fatal("second pass never arrived")
		}
		if !ok {
			t.Fatal("updates channel closed early")
		}
		if st.Err != nil {
			t.Fatalf("prompt rejection surfaced as a pass failure: %v", st.Err)
		}
		if st.PassText == "pass 0" {
			s.Feed(make([]byte, sampleRate*bytesPerSample))
		}
		// The second pass carries locked text, gets the 400, and must succeed
		// on the promptless retry (server counts the rejected request too).
		if st.PassText == "pass 2" {
			mu.Lock()
			defer mu.Unlock()
			if len(prompts) != 3 || prompts[0] != "" || prompts[1] != "pass 0" || prompts[2] != "" {
				t.Errorf("prompts = %q, want [\"\", \"pass 0\", \"\"]", prompts)
			}
			if !s.promptRejected.Load() {
				t.Error("promptRejected flag not latched")
			}
			return
		}
	}
}
