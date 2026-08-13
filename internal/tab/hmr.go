package tab

import (
	"encoding/json"
	"fmt"
	"strings"
)

type remoteObject struct {
	Type                string          `json:"type"`
	Description         string          `json:"description"`
	Value               json.RawMessage `json:"value"`
	UnserializableValue string          `json:"unserializableValue"`
}

type cdpStackTrace struct {
	CallFrames []struct {
		FunctionName string `json:"functionName"`
		URL          string `json:"url"`
		LineNumber   int    `json:"lineNumber"`
		ColumnNumber int    `json:"columnNumber"`
	} `json:"callFrames"`
}

func renderArgs(args []remoteObject) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case len(a.Value) > 0:
			var s string
			if json.Unmarshal(a.Value, &s) == nil {
				parts = append(parts, s)
			} else {
				parts = append(parts, string(a.Value))
			}
		case a.UnserializableValue != "":
			parts = append(parts, a.UnserializableValue)
		case a.Description != "":
			parts = append(parts, a.Description)
		default:
			parts = append(parts, a.Type)
		}
	}
	return strings.Join(parts, " ")
}

func topFrame(st *cdpStackTrace) string {
	if st == nil || len(st.CallFrames) == 0 {
		return ""
	}
	f := st.CallFrames[0]
	where := "<anonymous>"
	if f.URL != "" {
		where = fmt.Sprintf("%s:%d:%d", f.URL, f.LineNumber+1, f.ColumnNumber+1)
	}
	if f.FunctionName != "" {
		return fmt.Sprintf("%s (%s)", f.FunctionName, where)
	}
	return where
}

type HMR struct {
	Flavor string
	Type   string
	Files  []string
}

var (
	viteTypes    = map[string]bool{"connected": true, "update": true, "full-reload": true, "prune": true, "error": true, "custom": true}
	webpackTypes = map[string]bool{"ok": true, "hash": true, "invalid": true, "static-changed": true, "content-changed": true, "warnings": true, "errors": true}
)

func ClassifyHMR(payload string) *HMR {
	var probe map[string]json.RawMessage
	if json.Unmarshal([]byte(payload), &probe) != nil {
		return nil
	}
	var typ string
	if raw, ok := probe["type"]; !ok || json.Unmarshal(raw, &typ) != nil {
		return nil
	}

	if viteTypes[typ] {
		h := &HMR{Flavor: "vite", Type: typ}
		switch typ {
		case "update":
			var v struct {
				Updates []struct {
					Path         string `json:"path"`
					AcceptedPath string `json:"acceptedPath"`
				} `json:"updates"`
			}
			if json.Unmarshal([]byte(payload), &v) == nil {
				for _, u := range v.Updates {
					if u.AcceptedPath != "" {
						h.Files = append(h.Files, u.AcceptedPath)
					} else if u.Path != "" {
						h.Files = append(h.Files, u.Path)
					}
				}
			}
		case "full-reload":
			var v struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(payload), &v) == nil && v.Path != "" {
				h.Files = []string{v.Path}
			}
		case "prune":
			var v struct {
				Paths []string `json:"paths"`
			}
			if json.Unmarshal([]byte(payload), &v) == nil {
				h.Files = v.Paths
			}
		case "error":
			var v struct {
				Err struct {
					ID string `json:"id"`
				} `json:"err"`
			}
			if json.Unmarshal([]byte(payload), &v) == nil && v.Err.ID != "" {
				h.Files = []string{v.Err.ID}
			}
		}
		return h
	}

	if webpackTypes[typ] {
		h := &HMR{Flavor: "webpack", Type: typ}
		var v struct {
			Data []string `json:"data"`
		}
		if json.Unmarshal([]byte(payload), &v) == nil {
			h.Files = v.Data
		}
		return h
	}

	return nil
}
