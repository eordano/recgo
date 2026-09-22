package alt

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestAppPairsWithExternalDriverLikeTheRelay(t *testing.T) {
	var connected atomic.Int32
	s := newServer(t, Options{OnApp: func(AppInfo) { connected.Add(1) }})
	app := dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { _, ok := s.App(); return ok })
	info, _ := s.App()
	if info.Name != "__default__" || info.Platform != "mock" || info.AppID != "mock" {
		t.Fatalf("app info from the query string = %+v", info)
	}

	d := dialDriver(t, s, "__default__")
	reg := d.mustRead()
	if reg["commandName"] != "driverRegistered" || reg["isNotification"] != true {
		t.Fatalf("driver should hear driverRegistered first, got %v", reg)
	}
	notes := app.waitNotifications(1)
	if len(notes) != 1 || notes[0] != "DriverConnectedNotification" {
		t.Fatalf("app should hear DriverConnectedNotification, got %v", notes)
	}
	app.mu.Lock()
	drvID, _ := app.notes[0]["driverId"].(string)
	app.mu.Unlock()
	if drvID == "" {
		t.Fatal("DriverConnectedNotification must carry the driver's id")
	}

	// Frames go through verbatim: driverId null like altdrive.py sends.
	d.send(map[string]any{"commandName": "getServerVersion", "messageId": "m1", "driverId": nil, "isNotification": false})
	res := d.mustRead()
	if res["messageId"] != "m1" || res["data"] != `"2.3.0"` || res["error"] != nil {
		t.Fatalf("relayed response = %v", res)
	}

	d.ws.Close()
	notes = app.waitNotifications(2)
	if len(notes) != 2 || notes[1] != "DriverDisconnectedNotification" {
		t.Fatalf("app should hear the driver leave, got %v", notes)
	}
	waitFor(t, "OnApp", func() bool { return connected.Load() == 1 })
}

func TestInternalAndExternalDriversDoNotMix(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })
	d := dialDriver(t, s, "__default__")
	d.mustRead() // driverRegistered

	// recgo's driver and the external one ask at the same time; each gets
	// exactly its own answer back.
	done := make(chan error, 1)
	go func() {
		v, err := s.Driver().GetServerVersion(context.Background())
		if err == nil && v != "2.3.0" {
			err = errors.New("internal driver got " + v)
		}
		done <- err
	}()
	d.send(map[string]any{"commandName": "getCurrentScene", "messageId": "ext-1", "driverId": nil, "isNotification": false})
	res := d.mustRead()
	if res["messageId"] != "ext-1" || res["commandName"] != "getCurrentScene" {
		t.Fatalf("external driver received a frame that was not its own: %v", res)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// Nothing else may reach the external driver.
	d.ws.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if _, _, err := d.ws.ReadMessage(); err == nil {
		t.Fatal("the internal driver's response leaked to the external driver")
	}

	// The internal driver tags its command with its own driverId.
	cmds := app.seen("getServerVersion")
	if len(cmds) != 1 || cmds[0]["driverId"] != s.Driver().ID() {
		t.Fatalf("internal command driverId = %v, want %s", cmds, s.Driver().ID())
	}
}

func TestScreenshotDoubleAnswer(t *testing.T) {
	s := newServer(t, Options{})
	dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	shot, err := s.Driver().GetScreenshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(shot.Image) != string(fakePNG) || shot.W != 1 || shot.H != 1 {
		t.Fatalf("screenshot = %d bytes %dx%d", len(shot.Image), shot.W, shot.H)
	}
	// Cmd returns the image, not the "Ok" ack.
	raw, err := s.Driver().Cmd(context.Background(), map[string]any{
		"commandName": "getScreenshot", "size": map[string]any{"x": 0, "y": 0}, "quality": 100})
	if err != nil || len(raw) < 20 {
		t.Fatalf("Cmd getScreenshot = %s, %v", raw, err)
	}
	// And a plain command right after is still matched to its own answer.
	if v, err := s.Driver().GetServerVersion(context.Background()); err != nil || v != "2.3.0" {
		t.Fatalf("after screenshot: %q %v", v, err)
	}
}

func TestErrorEnvelopeBecomesAltError(t *testing.T) {
	s := newServer(t, Options{})
	dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	_, err := s.Driver().Cmd(context.Background(), map[string]any{
		"commandName": "findObject", "path": "//Nope", "cameraBy": 2, "cameraPath": "//", "enabled": true})
	var ae *AltError
	if !errors.As(err, &ae) || ae.Type != "objectNotFound" {
		t.Fatalf("err = %v, want objectNotFound", err)
	}
}

func TestNoAppAndTimeout(t *testing.T) {
	s := newServer(t, Options{})
	if _, err := s.Driver().GetServerVersion(context.Background()); !errors.Is(err, ErrNoApp) {
		t.Fatalf("without an app: %v", err)
	}
	app := dialApp(t, s, "__default__", false)
	app.hang = true
	waitFor(t, "app", func() bool { return s.Driver().Connected() })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := s.Driver().GetServerVersion(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hung app: %v", err)
	}
}

func TestAppLossCloses4002AndPendingCalls(t *testing.T) {
	var lost atomic.Int32
	s := newServer(t, Options{OnAppLost: func(AppInfo) { lost.Add(1) }})
	app := dialApp(t, s, "__default__", false)
	app.hang = true
	waitFor(t, "app", func() bool { return s.Driver().Connected() })
	d := dialDriver(t, s, "__default__")
	d.mustRead()

	done := make(chan error, 1)
	go func() {
		_, err := s.Driver().GetServerVersion(context.Background())
		done <- err
	}()
	waitFor(t, "pending call", func() bool { return len(app.seen("getServerVersion")) == 1 })
	app.close()

	_, err := d.read()
	var ce *websocket.CloseError
	if !errors.As(err, &ce) || ce.Code != 4002 {
		t.Fatalf("external driver should be closed with 4002, got %v", err)
	}
	if err := <-done; err == nil {
		t.Fatal("a call pending on the lost app must fail")
	}
	waitFor(t, "OnAppLost", func() bool { return lost.Load() == 1 })
	if s.Driver().Connected() {
		t.Fatal("driver still attached to a dead app")
	}

	// A new app takes over and a new driver pairs with it.
	dialApp(t, s, "__default__", false)
	waitFor(t, "second app", func() bool { return s.Driver().Connected() })
	d2 := dialDriver(t, s, "__default__")
	if reg := d2.mustRead(); reg["commandName"] != "driverRegistered" {
		t.Fatalf("second driver: %v", reg)
	}
}

func TestSecondAppReplacesTheFirst(t *testing.T) {
	s := newServer(t, Options{})
	first := dialApp(t, s, "one", false)
	waitFor(t, "first app", func() bool { i, ok := s.App(); return ok && i.Name == "one" })
	dialApp(t, s, "two", false)
	waitFor(t, "second app", func() bool { i, ok := s.App(); return ok && i.Name == "two" })
	select {
	case <-first.done:
	case <-time.After(3 * time.Second):
		t.Fatal("the first app's socket should be closed")
	}
	if v, err := s.Driver().GetServerVersion(context.Background()); err != nil || v != "2.3.0" {
		t.Fatalf("driver on the second app: %q %v", v, err)
	}
}

func TestAppNameFilter(t *testing.T) {
	s := newServer(t, Options{AppName: "wanted"})
	other := dialApp(t, s, "other", false)
	select {
	case <-other.done:
	case <-time.After(3 * time.Second):
		t.Fatal("an app with another name should be refused")
	}
	if _, ok := s.App(); ok {
		t.Fatal("refused app must not be kept")
	}
	dialApp(t, s, "wanted", false)
	waitFor(t, "wanted app", func() bool { return s.Driver().Connected() })
}

func TestDriverWaitingBeforeTheAppIsPairedLater(t *testing.T) {
	s := newServer(t, Options{})
	d := dialDriver(t, s, "__default__")
	time.Sleep(100 * time.Millisecond)
	app := dialApp(t, s, "__default__", false)
	if reg := d.mustRead(); reg["commandName"] != "driverRegistered" {
		t.Fatalf("driver: %v", reg)
	}
	if notes := app.waitNotifications(1); len(notes) != 1 || notes[0] != "DriverConnectedNotification" {
		t.Fatalf("the app that arrived second is told about the waiting driver: %v", notes)
	}
}

func TestPortTakenIsRefusedWithAdvice(t *testing.T) {
	s := newServer(t, Options{})
	_, port, _ := splitHostPort(s.Addr())
	_, err := Listen(Options{Host: "127.0.0.1", Port: port})
	if err == nil {
		t.Fatal("second listener on the same port should fail")
	}
	for _, want := range []string{"--alt-port", "AltTester Desktop"} {
		if !contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
}
