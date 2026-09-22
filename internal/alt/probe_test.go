package alt

import (
	"context"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func splitHostPort(addr string) (string, int, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}
	n, err := strconv.Atoi(p)
	return h, n, err
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestCallStaticMethodMirrorsTheSDK(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	r := NewResolver(s.Driver(), DefaultProbe)
	el, err := r.ElementAt(context.Background(), 812.5, 340)
	if err != nil {
		t.Fatal(err)
	}
	cmds := app.seen("callComponentMethodForObject")
	if len(cmds) != 1 {
		t.Fatalf("probe calls = %d", len(cmds))
	}
	c := cmds[0]
	if v, ok := c["altObject"]; !ok || v != nil {
		t.Errorf("altObject must be present and null, got %v", c["altObject"])
	}
	for k, want := range map[string]string{
		"component": "DCL.Input.AltTesterUiProbe", "method": "ElementAtScreenPoint", "assembly": "DCL.Input",
	} {
		if c[k] != want {
			t.Errorf("%s = %v, want %s", k, c[k], want)
		}
	}
	if js(c["parameters"]) != `["812.5","340"]` {
		t.Errorf("parameters = %s (each parameter is its JSON encoding)", js(c["parameters"]))
	}
	if js(c["typeOfParameters"]) != `["System.Single","System.Single"]` {
		t.Errorf("typeOfParameters = %s", js(c["typeOfParameters"]))
	}
	if c["isNotification"] != false || c["driverId"] != s.Driver().ID() || c["messageId"] == "" {
		t.Errorf("envelope fields: %v", c)
	}
	if el == nil || el.Selector != "/Canvas/Play" || el.Tag != "Button" || el.ID != "2" || el.Text != "Play" {
		t.Fatalf("ugui hit = %+v", el)
	}
}

func TestProbeHitUITK(t *testing.T) {
	s := newServer(t, Options{})
	dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	r := NewResolver(s.Driver(), Probe{})
	el, err := r.ElementAt(context.Background(), 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := "uitk:/root/backpack/card-3"
	if el == nil || el.Selector != want || el.Tag != "Button" || el.ID != "4242" ||
		el.Classes != "card muted" || el.Text != "Backpack" || el.TestID != "" {
		t.Fatalf("uitk hit = %+v", el)
	}
	if el, err := r.ElementAt(context.Background(), 0, 0); err != nil || el != nil {
		t.Fatalf("probe \"null\" should be no element, got %+v %v", el, err)
	}
	if _, missing := r.ProbeMissing(); missing {
		t.Fatal("probe answered; it must not be marked missing")
	}
}

func TestProbeMissingFallsBackForTheSession(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	r := NewResolver(s.Driver(), DefaultProbe)
	el, err := r.ElementAt(context.Background(), 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	// The selector is the transform path the probe would have given, built
	// from the object tree fetched lazily on this first fallback hit; the
	// tag stays the most specific UI component.
	if el == nil || el.Selector != "/Canvas/Play" || el.Tag != "Button" || el.ID != "2" || el.Text != "" {
		t.Fatalf("raycast fallback = %+v", el)
	}
	if reason, missing := r.ProbeMissing(); !missing || !contains(reason, "componentNotFound") {
		t.Fatalf("probe should be marked missing: %q %v", reason, missing)
	}

	el, err = r.ElementAt(context.Background(), 300, 100)
	if err != nil {
		t.Fatal(err)
	}
	if el == nil || el.Selector != "/Canvas/Title" || el.Tag != "TextMeshProUGUI" || el.Text != "Decentraland" {
		t.Fatalf("text object via fallback = %+v", el)
	}
	if n := len(app.seen("getAllLoadedScenesAndObjects")); n != 1 {
		t.Fatalf("object tree fetched %d times; known hits never refetch it", n)
	}
	if el, err := r.ElementAt(context.Background(), 10, 10); err != nil || el != nil {
		t.Fatalf("raycast miss = %+v %v", el, err)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 1 {
		t.Fatalf("the probe was tried %d times; once is enough for the session", n)
	}
	if n := len(app.seen("findObjectAtCoordinates")); n != 3 {
		t.Fatalf("findObjectAtCoordinates calls = %d, want 3", n)
	}
}

func TestOtherProbeErrorsAreReported(t *testing.T) {
	s := newServer(t, Options{})
	dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	// A wrong method name is a real error, not a missing build.
	r := NewResolver(s.Driver(), Probe{Type: DefaultProbe.Type, Method: "Nope", Assembly: DefaultProbe.Assembly})
	r.d = s.Driver()
	if _, err := r.ElementAt(context.Background(), 100, 100); err != nil {
		t.Fatalf("fake app answers any method: %v", err)
	}
	if probeAbsent(&AltError{Type: "nullReferenceException", Message: "x"}) {
		t.Fatal("a null reference is not a missing probe")
	}
	for _, e := range []*AltError{
		{Type: "componentNotFound"}, {Type: "assemblyNotFound"},
		{Type: "unknownError", Message: "Assembly not found"},
	} {
		if !probeAbsent(e) {
			t.Errorf("%v should mean the build has no probe", e)
		}
	}
}

func TestDecodeProbe(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want string
	}{
		{`null`, ""},
		{`"null"`, ""},
		{`""`, ""},
		{js(probeUITK), "/root/backpack/card-3"},
		{probeUITK, "/root/backpack/card-3"},
		{`"not json"`, ""},
	} {
		hit, ok := decodeProbe(json.RawMessage(c.raw))
		if (c.want == "") != !ok || (ok && hit.Path != c.want) {
			t.Errorf("decodeProbe(%s) = %+v %v", c.raw, hit, ok)
		}
	}
}

// A hit the tree does not know (its transformId, or an ancestor's, is
// missing: the object appeared after the fetch) refetches the tree once and
// then has its path; a refetch that still leaves it unknown keeps the bare
// name, and a second unknown within treeRefetchEvery does not refetch.
func TestFallbackPathRefetchesUnknownTransformsOncePerInterval(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", false)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	now := time.Unix(1_700_000_000, 0)
	r := NewResolver(s.Driver(), DefaultProbe)
	r.now = func() time.Time { return now }
	ctx := context.Background()
	fetches := func() int { return len(app.seen("getAllLoadedScenesAndObjects")) }

	// On connect the tree is loaded; the app has not yet created Play.
	app.setTree(fakeObjects[:1])
	if err := r.LoadTree(ctx); err != nil {
		t.Fatal(err)
	}
	// Play exists by the time it is hit: unknown transformId, refetch, path.
	app.setTree(fakeObjects)
	now = now.Add(treeRefetchEvery)
	el, err := r.ElementAt(ctx, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	if el == nil || el.Selector != "/Canvas/Play" || fetches() != 2 {
		t.Fatalf("unknown hit = %+v after %d fetches, want /Canvas/Play after 2", el, fetches())
	}

	// A tree missing Play's parent: the chain breaks, one refetch, and the
	// bare name when the refetch does not help either.
	app.setTree([]map[string]any{fakeObjects[1]})
	now = now.Add(treeRefetchEvery)
	if err := r.LoadTree(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(treeRefetchEvery)
	el, err = r.ElementAt(ctx, 640, 360)
	if err != nil {
		t.Fatal(err)
	}
	if el == nil || el.Selector != "Play" || fetches() != 4 {
		t.Fatalf("orphaned hit = %+v after %d fetches, want bare Play after 4", el, fetches())
	}
	// Within the interval the same unknown hit does not refetch.
	now = now.Add(treeRefetchEvery - time.Second)
	if el, _ := r.ElementAt(ctx, 640, 360); el == nil || el.Selector != "Play" || fetches() != 4 {
		t.Fatalf("throttled hit = %+v after %d fetches, want bare Play after 4", el, fetches())
	}
	// Once the interval has passed it does, and the tree is whole again.
	app.setTree(fakeObjects)
	now = now.Add(time.Second)
	if el, _ := r.ElementAt(ctx, 640, 360); el == nil || el.Selector != "/Canvas/Play" || fetches() != 5 {
		t.Fatalf("hit after the interval = %+v after %d fetches, want /Canvas/Play after 5", el, fetches())
	}
}

func TestTransformPath(t *testing.T) {
	tree := map[int]treeNode{
		101: {name: "Canvas", parent: 0},
		105: {name: "AuthScreen", parent: 101},
		106: {name: "Preview", parent: 105},
		107: {name: "PreviewRawImage", parent: 106},
		200: {name: "Loop", parent: 201},
		201: {name: "Loop2", parent: 200},
	}
	for _, c := range []struct {
		obj  Object
		want string
		ok   bool
	}{
		{Object{Name: "PreviewRawImage", TransformID: 107, TransformParentID: 106}, "/Canvas/AuthScreen/Preview/PreviewRawImage", true},
		{Object{Name: "Canvas", TransformID: 101}, "/Canvas", true},
		{Object{Name: "New", TransformID: 999, TransformParentID: 101}, "", false},
		{Object{Name: "Orphan", TransformID: 105, TransformParentID: 404}, "", false},
		{Object{Name: "Loop", TransformID: 200, TransformParentID: 201}, "", false},
		{Object{Name: "NoTransform"}, "", false},
	} {
		got, ok := transformPath(tree, &c.obj)
		if got != c.want || ok != c.ok {
			t.Errorf("transformPath(%s) = %q %v, want %q %v", c.obj.Name, got, ok, c.want, c.ok)
		}
	}
}

func TestBestComponent(t *testing.T) {
	comps := []Component{
		{ComponentName: "UnityEngine.RectTransform"}, {ComponentName: "UnityEngine.UI.Image"},
		{ComponentName: "UnityEngine.UI.Button"},
	}
	if got := bestComponent(comps); got != "Button" {
		t.Errorf("Button beats Image: %s", got)
	}
	if got := bestComponent([]Component{{ComponentName: "UnityEngine.RectTransform"}, {ComponentName: "DCL.Backpack.CardView"}}); got != "CardView" {
		t.Errorf("custom component after transforms: %s", got)
	}
	if got := bestComponent(nil); got != "" {
		t.Errorf("no components: %q", got)
	}
}
