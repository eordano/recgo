package android

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A real subprocess and TCP helper exercise the recorder without an attached device.
func TestADBHelperProcess(t *testing.T) {
	if os.Getenv("RECGO_FAKE_ADB") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	args = args[1:]
	if len(args) < 3 || args[0] != "-s" {
		os.Exit(3)
	}
	args = args[2:]
	switch strings.Join(args, " ") {
	case "get-state":
		fmt.Println("device")
	case "shell am get-current-user":
		fmt.Println("0")
	case "shell cmd package list packages -U --user 0 com.example.app":
		fmt.Println("package:com.example.app uid:10101")
	case "forward tcp:0 localabstract:recgo_android":
		fmt.Println(os.Getenv("RECGO_FAKE_PORT"))
	case "forward --remove tcp:" + os.Getenv("RECGO_FAKE_PORT"):
		if err := os.WriteFile(os.Getenv("RECGO_FAKE_CLEANUP"), []byte("removed"), 0600); err != nil {
			os.Exit(4)
		}
	default:
		os.Exit(5)
	}
	os.Exit(0)
}

func TestRecordLifecycle(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprint("disconnect=", disconnect), func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			root := t.TempDir()
			cleanup := filepath.Join(root, "forward-removed")
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(root, "adb")
			if err = os.WriteFile(script, []byte("#!/bin/sh\nexec \"$RECGO_TEST_BINARY\" -test.run=TestADBHelperProcess -- \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("RECGO_TEST_BINARY", binary)
			t.Setenv("RECGO_FAKE_ADB", "1")
			t.Setenv("RECGO_FAKE_PORT", port)
			t.Setenv("RECGO_FAKE_CLEANUP", cleanup)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				reader := bufio.NewReader(conn)
				if _, err = reader.ReadString('\n'); err != nil {
					return
				}
				encoder := json.NewEncoder(conn)
				_ = encoder.Encode(Event{Kind: "hello", Version: 1, ElapsedMs: 1000})
				_ = encoder.Encode(Event{Kind: "click", ElapsedMs: 1001, Package: "com.other.app", Node: &Node{Text: "excluded"}})
				_ = encoder.Encode(Event{Kind: "click", ElapsedMs: 1002, Package: "com.example.app", Node: &Node{ResourceID: "com.example.app:id/button", Text: "secret", Redacted: true}})
				if disconnect {
					return
				}
				for {
					if _, err = reader.ReadString('\n'); err != nil {
						return
					}
					if encoder.Encode(Event{Kind: "heartbeat", ElapsedMs: 1003}) != nil {
						return
					}
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var stdout, stderr bytes.Buffer
			err = Run(ctx, Options{Serial: "emulator-1234", Package: "com.example.app", ADB: script, Out: filepath.Join(root, "sessions"), Duration: 250 * time.Millisecond}, &stdout, &stderr)
			if disconnect && err == nil {
				t.Fatal("disconnect reported success")
			}
			if !disconnect && err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(cleanup); err != nil {
				t.Fatal("forward leaked:", err)
			}
			dirs, err := filepath.Glob(filepath.Join(root, "sessions", "*", "session.json"))
			if err != nil || len(dirs) != 1 {
				t.Fatal(dirs, err)
			}
			raw, err := os.ReadFile(dirs[0])
			if err != nil {
				t.Fatal(err)
			}
			var session Session
			if err = json.Unmarshal(raw, &session); err != nil {
				t.Fatal(err)
			}
			if len(session.Events) != 1 || session.Events[0].Node.Text != "" {
				t.Fatalf("filter/redaction failed: %s", raw)
			}
			if !strings.Contains(stdout.String(), "SESSION.md") {
				t.Fatal(stdout.String())
			}
			if disconnect && len(session.Warnings) == 0 {
				t.Fatal("partial session has no warning")
			}
		})
	}
}

func TestValidate(t *testing.T) {
	for _, serial := range []string{"emulator-5580", "56181FDCH0052N", "10.0.0.5:5555", "[::1]:5555"} {
		if _, err := (Options{Serial: serial, Package: "com.example.app"}).validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, o := range []Options{
		{Serial: "", Package: "com.example.app"},
		{Serial: "-s", Package: "com.example.app"},
		{Serial: "emu; echo bad", Package: "com.example.app"},
		{Serial: "emulator-5580", Package: "com.example.app;id"},
		{Serial: "emulator-5580", Package: "com.example.*"},
		{Serial: "emulator-5580", Package: "com.example.app", ExtraPackages: "*"},
		{Serial: "emulator-5580", Package: "com.example.app", Transcribe: true},
		{Serial: "emulator-5580", Package: "com.example.app", Duration: -time.Second},
	} {
		if _, err := o.validate(); err == nil {
			t.Errorf("accepted invalid options: %+v", o)
		}
	}
}

func TestParseUIDExactPackage(t *testing.T) {
	raw := "package:com.example.app.debug uid:10101\npackage:com.example.app uid:10102\n"
	uid, err := parseUID(raw, "com.example.app")
	if err != nil || uid != "10102" {
		t.Fatalf("%q %v", uid, err)
	}
	if _, err := parseUID(raw, "com.example"); err == nil {
		t.Fatal("matched partial package")
	}
}

func TestRedactAndUnknown(t *testing.T) {
	e := Event{Node: &Node{Text: "synthetic-secret", Description: "synthetic-secret", Redacted: true}}
	e.sanitize()
	b, _ := json.Marshal(e)
	if strings.Contains(string(b), "synthetic-secret") {
		t.Fatal("redacted value leaked")
	}
	e = Event{TargetStatus: "guessed"}
	e.sanitize()
	if e.TargetStatus != "unknown" || !strings.Contains(label(e), "unknown") {
		t.Fatal(e)
	}
}

func TestReportUntrustedTextAndShotTiming(t *testing.T) {
	s := Session{Events: []Event{{Kind: "click", T: 100, Node: &Node{Text: "[evil](https://example.com)\n# injected <script>"}, Screenshot: &Shot{File: "0001.png", StartMs: 350, EndMs: 500}}}}
	got := Report(s, false)
	for _, forbidden := range []string{"\n# injected", "<script>", "[evil]"} {
		if strings.Contains(got, forbidden) {
			t.Fatal(got)
		}
	}
	for _, want := range []string{"250 ms after event", "not at the tap or before it", "not raw touch coordinates"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s", want)
		}
	}
}

func TestReportAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	if err := writeReport(dir, "SESSION.md", "test"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "SESSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if _, err := os.Stat(filepath.Join(dir, "SESSION.md.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary file left: %v", err)
	}
}

func TestStopChildAfterAlreadyExited(t *testing.T) {
	c, err := startChild(exec.Command("true"), filepath.Join(t.TempDir(), "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	<-c.done
	done := make(chan struct{})
	go func() { c.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stop hangs on reaped process")
	}
}

func TestADBArgumentsNeverShell(t *testing.T) {
	d := device{"adb", "emulator-5580"}
	cmd := d.command(context.Background(), "install", "-r", "/tmp/helper file.apk")
	if got := cmd.Args; len(got) != 6 || got[1] != "-s" || got[2] != "emulator-5580" || got[5] != "/tmp/helper file.apk" {
		t.Fatal(got)
	}
}
