package alt

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eordano/recgo/internal/tab"
)

// Probe names the static method the dev build exposes for recgo:
//
//	static string DCL.Input.AltTesterUiProbe.ElementAtScreenPoint(float x, float y)
//
// called through callComponentMethodForObject with altObject null, the way
// the SDK's own CallStaticMethod does it.
type Probe struct {
	Type, Method, Assembly string
}

var DefaultProbe = Probe{
	Type: "DCL.Input.AltTesterUiProbe", Method: "ElementAtScreenPoint", Assembly: "DCL.Input",
}

// probeHit is the JSON the probe returns (or the literal "null").
type probeHit struct {
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Text    string `json:"text"`
	ID      int64  `json:"id"`
	Classes string `json:"classes"`
	TestID  string `json:"testId"`
}

// Resolver names the UI element at Unity screen coordinates: through the
// probe when the build has it, else through the SDK's own UGUI raycast,
// whose hits are given the transform path the probe would have reported
// (see pathFor).
type Resolver struct {
	d     *Driver
	probe Probe
	now   func() time.Time

	mu      sync.Mutex
	noProbe string           // why the probe is skipped, once known
	tree    map[int]treeNode // transformId -> the object's name and parent
	treeAt  time.Time        // last getAllLoadedScenesAndObjects attempt
}

// treeNode is what the object tree keeps per transform: enough to walk a
// hit's parent chain up to its scene root.
type treeNode struct {
	name   string
	parent int
}

// treeRefetchEvery bounds how often a hit the tree does not know (an
// object created since the last fetch) makes the resolver fetch it again.
const treeRefetchEvery = 5 * time.Second

func NewResolver(d *Driver, p Probe) *Resolver {
	if p.Type == "" {
		p = DefaultProbe
	}
	if p.Method == "" {
		p.Method = DefaultProbe.Method
	}
	return &Resolver{d: d, probe: p, now: time.Now}
}

// LoadTree fetches the object tree now, so the first fallback hit already
// has a path; without it the first hit fetches the tree itself.
func (r *Resolver) LoadTree(ctx context.Context) error {
	return r.fetchTree(ctx)
}

func (r *Resolver) fetchTree(ctx context.Context) error {
	objs, err := r.d.GetAllLoadedObjects(ctx)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.treeAt = r.now()
	if err != nil {
		return err
	}
	tree := make(map[int]treeNode, len(objs))
	for _, o := range objs {
		if o.TransformID == 0 {
			continue // a scene entry, not an object
		}
		tree[o.TransformID] = treeNode{name: o.Name, parent: o.TransformParentID}
	}
	r.tree = tree
	return nil
}

// pathFor is obj's transform path from its scene root, the selector the
// probe reports for a UGUI hit ("/Canvas/AuthScreen/Preview/PreviewRawImage").
// A hit the tree does not know, or whose ancestor it lacks, refetches the
// tree once (at most once per treeRefetchEvery); "" when still unknown, and
// the caller keeps the bare name.
func (r *Resolver) pathFor(ctx context.Context, obj *Object) string {
	r.mu.Lock()
	tree, at := r.tree, r.treeAt
	r.mu.Unlock()
	if p, ok := transformPath(tree, obj); ok {
		return p
	}
	if r.now().Sub(at) < treeRefetchEvery {
		return ""
	}
	if err := r.fetchTree(ctx); err != nil {
		return ""
	}
	r.mu.Lock()
	tree = r.tree
	r.mu.Unlock()
	if p, ok := transformPath(tree, obj); ok {
		return p
	}
	return ""
}

// transformPath walks obj's parent chain through tree to a root (parent
// 0), the way altdrive.py's build_path does; ok is false when obj itself
// or any ancestor is missing from tree.
func transformPath(tree map[int]treeNode, obj *Object) (string, bool) {
	if obj.TransformID == 0 {
		return "", false
	}
	if _, known := tree[obj.TransformID]; !known {
		return "", false
	}
	parts := []string{obj.Name}
	seen := map[int]bool{obj.TransformID: true}
	for id := obj.TransformParentID; id != 0; {
		n, ok := tree[id]
		if !ok || seen[id] {
			return "", false
		}
		seen[id] = true
		parts = append(parts, n.name)
		id = n.parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	return "/" + strings.Join(parts, "/"), true
}

// ProbeMissing reports why the session fell back to the SDK raycast, once
// the build has answered that it has no probe.
func (r *Resolver) ProbeMissing() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.noProbe, r.noProbe != ""
}

// ElementAt is the element under (x, y) in Unity screen space (pixels,
// origin bottom-left), or nil when nothing is there.
func (r *Resolver) ElementAt(ctx context.Context, x, y float64) (*tab.Element, error) {
	r.mu.Lock()
	skip := r.noProbe != ""
	r.mu.Unlock()
	if !skip {
		el, err := r.viaProbe(ctx, x, y)
		if err == nil {
			return el, nil
		}
		if !probeAbsent(err) {
			return nil, err
		}
		r.mu.Lock()
		r.noProbe = err.Error()
		r.mu.Unlock()
	}
	return r.viaRaycast(ctx, x, y)
}

func (r *Resolver) viaProbe(ctx context.Context, x, y float64) (*tab.Element, error) {
	raw, err := r.d.CallStaticMethod(ctx, r.probe.Type, r.probe.Method, r.probe.Assembly,
		[]string{JSONNumber(x), JSONNumber(y)}, []string{"System.Single", "System.Single"})
	if err != nil {
		return nil, err
	}
	hit, ok := decodeProbe(raw)
	if !ok {
		return nil, nil
	}
	return hit.element(), nil
}

// decodeProbe unwraps the probe's return value: the app serializes the
// returned string, so the JSON object usually arrives as a string.
func decodeProbe(raw json.RawMessage) (*probeHit, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		s = strings.TrimSpace(s)
		if s == "" || s == "null" {
			return nil, false
		}
		raw = json.RawMessage(s)
	}
	if string(raw) == "null" {
		return nil, false
	}
	var hit probeHit
	if json.Unmarshal(raw, &hit) != nil || (hit.Path == "" && hit.Name == "" && hit.Type == "") {
		return nil, false
	}
	return &hit, true
}

func (h *probeHit) element() *tab.Element {
	sel := h.Path
	if sel == "" {
		sel = h.Name
	}
	if h.Kind == "uitk" {
		sel = "uitk:" + sel
	}
	el := &tab.Element{
		Selector: sel, Tag: h.Type, Classes: h.Classes, TestID: h.TestID, Text: h.Text,
	}
	if h.ID != 0 {
		el.ID = strconv.FormatInt(h.ID, 10)
	}
	return el
}

// probeAbsent is the app saying the probe type is not in this build, which
// is permanent for the session.
func probeAbsent(err error) bool {
	ae, ok := err.(*AltError)
	if !ok {
		return false
	}
	switch ae.Type {
	case "componentNotFound", "assemblyNotFound", "methodNotFound":
		return true
	}
	return strings.Contains(ae.Message, "Assembly not found")
}

// uiComponents ranks the UGUI components worth naming an element by, most
// specific first; a Transform or CanvasRenderer never wins.
var uiComponents = []string{
	"TMP_InputField", "InputField", "Button", "Toggle", "Slider", "Dropdown", "Scrollbar",
	"ScrollRect", "TextMeshProUGUI", "TMP_Text", "Text", "RawImage", "Image",
}

func (r *Resolver) viaRaycast(ctx context.Context, x, y float64) (*tab.Element, error) {
	obj, err := r.d.FindObjectAtCoordinates(ctx, x, y)
	if err != nil || obj == nil {
		return nil, err
	}
	el := &tab.Element{Selector: obj.Name, Tag: obj.Type, ID: strconv.Itoa(obj.ID)}
	if p := r.pathFor(ctx, obj); p != "" {
		el.Selector = p
	}
	comps, err := r.d.GetAllComponents(ctx, obj.ID)
	if err != nil {
		return el, nil
	}
	if t := bestComponent(comps); t != "" {
		el.Tag = t
	}
	if hasText(comps) {
		if text, err := r.d.GetText(ctx, obj); err == nil {
			el.Text = clip(strings.TrimSpace(text), 80)
		}
	}
	return el, nil
}

func shortName(full string) string {
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

func bestComponent(comps []Component) string {
	bestRank := len(uiComponents)
	best := ""
	for _, c := range comps {
		name := shortName(c.ComponentName)
		for rank, want := range uiComponents {
			if name == want && rank < bestRank {
				bestRank, best = rank, name
			}
		}
	}
	if best == "" {
		for _, c := range comps {
			name := shortName(c.ComponentName)
			switch name {
			case "Transform", "RectTransform", "CanvasRenderer":
				continue
			}
			return name
		}
	}
	return best
}

func hasText(comps []Component) bool {
	for _, c := range comps {
		name := shortName(c.ComponentName)
		if strings.Contains(name, "Text") || strings.Contains(name, "InputField") {
			return true
		}
	}
	return false
}
