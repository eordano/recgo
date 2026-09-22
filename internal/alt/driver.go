package alt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNoApp is returned while no instrumented app is connected.
var ErrNoApp = errors.New("no AltTester app is connected")

// DefaultTimeout bounds one command when the context carries no deadline.
const DefaultTimeout = 5 * time.Second

// AltError is the app's error envelope ({type, message}) as a Go error.
type AltError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Trace   string `json:"trace,omitempty"`
}

func (e *AltError) Error() string { return e.Type + ": " + e.Message }

// envelope is the app's response: data is a JSON-encoded string that has
// to be decoded again, error is {type, message} or null.
type envelope struct {
	MessageID      string    `json:"messageId"`
	CommandName    string    `json:"commandName"`
	DriverID       string    `json:"driverId"`
	IsNotification bool      `json:"isNotification"`
	Data           *string   `json:"data"`
	Error          *AltError `json:"error"`
}

// Object is AltTester's AltObject.
type Object struct {
	Name              string  `json:"name"`
	ID                int     `json:"id"`
	X                 int     `json:"x"`
	Y                 int     `json:"y"`
	Z                 int     `json:"z"`
	MobileY           int     `json:"mobileY"`
	Type              string  `json:"type"`
	Enabled           bool    `json:"enabled"`
	WorldX            float64 `json:"worldX"`
	WorldY            float64 `json:"worldY"`
	WorldZ            float64 `json:"worldZ"`
	IDCamera          int     `json:"idCamera"`
	TransformParentID int     `json:"transformParentId"`
	TransformID       int     `json:"transformId"`
}

// Component is one entry of getAllComponents.
type Component struct {
	ComponentName string `json:"componentName"`
	AssemblyName  string `json:"assemblyName"`
}

// Driver is recgo's own AltTester driver. It shares the app socket with the
// external-driver relay: every command carries this driver's driverId and
// responses are matched back by messageId, so the two never mix.
type Driver struct {
	id      string
	seq     uint64
	mu      sync.Mutex
	app     *appConn
	pending map[string]*call
}

type call struct {
	ch  chan envelope
	app *appConn // the socket the command went out on
}

func newDriver(id string) *Driver {
	return &Driver{id: id, pending: map[string]*call{}}
}

// ID is the driverId this driver tags its commands with.
func (d *Driver) ID() string { return d.id }

// Connected reports whether an app is attached.
func (d *Driver) Connected() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.app != nil
}

func (d *Driver) attach(a *appConn) {
	d.mu.Lock()
	old := d.app
	d.app = a
	d.mu.Unlock()
	if old != nil {
		d.detach(old)
	}
}

// detach fails every call still waiting on a; calls already out on a
// newer app are left alone.
func (d *Driver) detach(a *appConn) {
	d.mu.Lock()
	if d.app == a {
		d.app = nil
	}
	var stale []*call
	for mid, c := range d.pending {
		if c.app == a {
			stale = append(stale, c)
			delete(d.pending, mid)
		}
	}
	d.mu.Unlock()
	for _, c := range stale {
		select {
		case c.ch <- envelope{Error: &AltError{Type: "appDisconnected", Message: "the app disconnected"}}:
		default:
		}
	}
}

func (d *Driver) owns(env envelope) bool {
	if env.DriverID != "" && env.DriverID == d.id {
		return true
	}
	if env.MessageID == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.pending[env.MessageID]
	return ok
}

func (d *Driver) deliver(env envelope, text string) {
	d.mu.Lock()
	c := d.pending[env.MessageID]
	d.mu.Unlock()
	if c == nil {
		return
	}
	select {
	case c.ch <- env:
	default:
	}
}

// Cmd sends one command (commandName plus its fields) and returns the
// decoded data payload. getScreenshot answers twice, "Ok" then the image;
// Cmd returns the image.
func (d *Driver) Cmd(ctx context.Context, params map[string]any) (json.RawMessage, error) {
	want := 1
	if params["commandName"] == "getScreenshot" {
		want = 2
	}
	res, err := d.call(ctx, params, want)
	if err != nil {
		return nil, err
	}
	return res[len(res)-1], nil
}

func (d *Driver) call(ctx context.Context, params map[string]any, want int) ([]json.RawMessage, error) {
	name, _ := params["commandName"].(string)
	if name == "" {
		return nil, errors.New("alt: command without commandName")
	}
	mid := fmt.Sprintf("%d-%d", time.Now().UnixNano(), atomic.AddUint64(&d.seq, 1))
	msg := make(map[string]any, len(params)+3)
	for k, v := range params {
		msg[k] = v
	}
	msg["messageId"] = mid
	msg["driverId"] = d.id
	msg["isNotification"] = false
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}

	c := &call{ch: make(chan envelope, want)}
	d.mu.Lock()
	app := d.app
	if app != nil {
		c.app = app
		d.pending[mid] = c
	}
	d.mu.Unlock()
	if app == nil {
		return nil, ErrNoApp
	}
	defer func() {
		d.mu.Lock()
		delete(d.pending, mid)
		d.mu.Unlock()
	}()
	if err := app.send(string(body)); err != nil {
		return nil, fmt.Errorf("alt: send %s: %w", name, err)
	}

	if _, has := ctx.Deadline(); !has {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	var out []json.RawMessage
	for len(out) < want {
		select {
		case env := <-c.ch:
			if env.Error != nil {
				return nil, env.Error
			}
			out = append(out, decodeData(env.Data))
		case <-ctx.Done():
			return nil, fmt.Errorf("alt: %s: %w", name, ctx.Err())
		}
	}
	return out, nil
}

// decodeData turns the envelope's JSON-encoded string into the JSON it
// carries; the SDK always double-encodes, but a bare value is tolerated.
func decodeData(data *string) json.RawMessage {
	if data == nil {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(*data)) {
		return json.RawMessage(*data)
	}
	b, _ := json.Marshal(*data)
	return b
}

func (d *Driver) cmdInto(ctx context.Context, params map[string]any, v any) error {
	raw, err := d.Cmd(ctx, params)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("alt: %s: decode %q: %w", params["commandName"], clip(string(raw), 120), err)
	}
	return nil
}

func (d *Driver) GetServerVersion(ctx context.Context) (string, error) {
	var v string
	err := d.cmdInto(ctx, map[string]any{"commandName": "getServerVersion"}, &v)
	return v, err
}

// GetApplicationScreenSize is Screen.width x Screen.height in the app.
func (d *Driver) GetApplicationScreenSize(ctx context.Context) (w, h float64, err error) {
	var v struct{ X, Y float64 }
	err = d.cmdInto(ctx, map[string]any{"commandName": "getApplicationScreenSize"}, &v)
	return v.X, v.Y, err
}

// FindObjectAtCoordinates is the UGUI raycast at Unity screen coordinates;
// nil, nil when nothing is hit.
func (d *Driver) FindObjectAtCoordinates(ctx context.Context, x, y float64) (*Object, error) {
	var v *Object
	err := d.cmdInto(ctx, map[string]any{
		"commandName": "findObjectAtCoordinates",
		"coordinates": map[string]any{"x": x, "y": y},
	}, &v)
	return v, err
}

// GetAllLoadedObjects is every enabled object in every loaded scene (plus
// DontDestroyOnLoad), the way altdrive.py's `objects` asks for it. Scene
// entries come back too, as objects with no transformId.
func (d *Driver) GetAllLoadedObjects(ctx context.Context) ([]Object, error) {
	var v []Object
	err := d.cmdInto(ctx, map[string]any{
		"commandName": "getAllLoadedScenesAndObjects",
		"path":        "//*",
		"cameraBy":    2, // By.NAME
		"cameraPath":  "//*",
		"enabled":     true,
	}, &v)
	return v, err
}

func (d *Driver) GetText(ctx context.Context, obj *Object) (string, error) {
	var v string
	err := d.cmdInto(ctx, map[string]any{"commandName": "getText", "altObject": obj}, &v)
	return v, err
}

func (d *Driver) GetAllComponents(ctx context.Context, id int) ([]Component, error) {
	var v []Component
	err := d.cmdInto(ctx, map[string]any{"commandName": "getAllComponents", "altObjectId": id}, &v)
	return v, err
}

// CallStaticMethod mirrors the SDK's AltCallStaticMethod: a
// callComponentMethodForObject with altObject null. params are each
// parameter's JSON encoding, paramTypes their .NET type names.
func (d *Driver) CallStaticMethod(ctx context.Context, typeName, method, assembly string, params, paramTypes []string) (json.RawMessage, error) {
	if params == nil {
		params = []string{}
	}
	if paramTypes == nil {
		paramTypes = []string{}
	}
	return d.Cmd(ctx, map[string]any{
		"commandName":      "callComponentMethodForObject",
		"altObject":        nil,
		"component":        typeName,
		"method":           method,
		"parameters":       params,
		"typeOfParameters": paramTypes,
		"assembly":         assembly,
	})
}

// Screenshot is the app's compressed screenshot (JPEG or PNG bytes) and
// its texture size.
type Screenshot struct {
	Image []byte
	W, H  int
}

func (d *Driver) GetScreenshot(ctx context.Context) (*Screenshot, error) {
	res, err := d.call(ctx, map[string]any{
		"commandName": "getScreenshot", "size": map[string]any{"x": 0, "y": 0}, "quality": 100,
	}, 2)
	if err != nil {
		return nil, err
	}
	var ack string
	if json.Unmarshal(res[0], &ack) != nil || ack != "Ok" {
		return nil, fmt.Errorf("alt: getScreenshot: expected an Ok ack, got %s", clip(string(res[0]), 80))
	}
	// textureSize is an AltVector3, whose floats Newtonsoft writes as
	// 1920.0: an int field would refuse every real screenshot.
	var v struct {
		CompressedImage string                 `json:"compressedImage"`
		TextureSize     struct{ X, Y float64 } `json:"textureSize"`
	}
	if err := json.Unmarshal(res[1], &v); err != nil {
		return nil, fmt.Errorf("alt: getScreenshot: %w", err)
	}
	img, err := base64.StdEncoding.DecodeString(v.CompressedImage)
	if err != nil {
		return nil, fmt.Errorf("alt: getScreenshot: %w", err)
	}
	return &Screenshot{Image: img, W: int(v.TextureSize.X), H: int(v.TextureSize.Y)}, nil
}

// JSONNumber is a float the way the SDK's JsonConvert serializes a
// System.Single parameter for callComponentMethodForObject.
func JSONNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
