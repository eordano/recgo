package tab

import "fmt"

// Every URL the page reports -- a navigation, a click, the visibility report a
// fresh document sends on install -- passes through here, so one URL change
// becomes exactly one navigation event however many sources saw it. The title
// rides along only when it changed: on a deck it is constant, on an app it is
// the cheapest section label there is.
func (r *Recorder) observeLocation(t float64, url, title string) {
	if url == "" {
		return
	}
	r.mu.Lock()
	prevURL, prevTitle := r.lastURL, r.lastTitle
	r.lastURL = url
	if title != "" {
		r.lastTitle = title
	}
	nav, navPrevTitle := r.lastNav, r.navPrevT
	r.mu.Unlock()

	// An unknown starting point is seeded, not reported: the Page header
	// already says where the recording began.
	if prevURL == "" {
		return
	}
	if url == prevURL {
		// A fresh document reports itself before <title> is parsed, and apps
		// set the title after the URL: the first title seen after a
		// navigation belongs to it. Later changes on the same URL do not.
		if nav != nil && nav.URL == url && title != "" && title != navPrevTitle {
			r.session.Update(nav, func(e *Event) {
				if e.Title == "" {
					e.Title = title
				}
			})
		}
		return
	}
	ev := Event{T: t, Kind: "navigation", URL: url}
	if title != "" && title != prevTitle {
		ev.Title = title
	}
	pushed := r.push(ev)
	r.mu.Lock()
	r.lastNav, r.navPrevT = pushed, prevTitle
	r.mu.Unlock()
}

func (r *Recorder) seedLocation(url, title string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if url != "" {
		r.lastURL = url
	}
	if title != "" {
		r.lastTitle = title
	}
}

// pageLocation asks the page where it is at record start. The injected
// script's own install-time report is emitted before the event handlers are
// registered, so it cannot be relied on for the seed.
func (r *Recorder) pageLocation() (url, title string) {
	var res struct {
		Result struct {
			Value struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"value"`
		} `json:"result"`
	}
	if err := r.cdp.Send("Runtime.evaluate", map[string]any{
		"expression":    "({url: location.href, title: document.title})",
		"returnByValue": true,
	}, &res); err != nil {
		return "", ""
	}
	return res.Result.Value.URL, res.Result.Value.Title
}

func navigationLine(e Event) string {
	if e.Title == "" {
		return fmt.Sprintf("Navigate: %s", e.URL)
	}
	return fmt.Sprintf("Navigate: %s — %s", e.URL, oneLine(e.Title))
}
