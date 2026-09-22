package tab

import "testing"

const deckTitle = "Acme · Q4 2026 Plan — Worth coming back"

func deckClick(t float64, title, text string) Event {
	return Event{T: t, Kind: "click", Title: title,
		URL:  "file:///home/user/slides/q4-plan.html#/2",
		Elem: &Element{Tag: "span", Text: text}}
}

func TestDeterministicSlugPrefersThePageTitle(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		deckClick(1000, deckTitle, "Qualification automated from platform data"),
		deckClick(2000, deckTitle, "Qualification automated from platform data"),
		deckClick(3000, deckTitle, "Qualification automated from platform data"),
		deckClick(4000, deckTitle, "Build. Play. Earn."),
	}
	if got := DeterministicSlug(events); got != "q4-2026-plan-worth-coming-back" {
		t.Errorf("slug = %q, want the deck's title, not its most-clicked element", got)
	}
}

func TestDeterministicSlugUsesTheRecordStartTitle(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start", URL: "http://localhost:5173/", Title: "Acme dashboard overview"},
		{T: 1000, Kind: "click", Elem: &Element{Tag: "button", Text: "Save"}},
	}
	if got := DeterministicSlug(events); got != "acme-dashboard-overview" {
		t.Errorf("slug = %q", got)
	}
}

func TestDeterministicSlugFallsBackToTheMostClickedElement(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		deckClick(1000, "", "$50/week"),
		deckClick(2000, "", "$50/week"),
		deckClick(3000, "", "Worth coming back."),
		{T: 4000, Kind: "navigation", URL: "http://x/#/3"},
	}
	if got := DeterministicSlug(events); got != "50-week" {
		t.Errorf("slug = %q, want the most-clicked element when nothing carries a title", got)
	}
}

// The --launch flow: recording starts at about:blank, the deck is navigated
// to, then flipped with the arrow keys. Only the navigation knows the title.
func TestDeterministicSlugUsesTheNavigationTitle(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start", URL: "about:blank"},
		{T: 500, Kind: "navigation", URL: "file:///home/user/slides/q4-plan.html", Title: deckTitle},
		{T: 2000, Kind: "navigation", URL: "file:///home/user/slides/q4-plan.html#/1"},
		{T: 3000, Kind: "navigation", URL: "file:///home/user/slides/q4-plan.html#/2"},
	}
	if got := DeterministicSlug(events); got != "q4-2026-plan-worth-coming-back" {
		t.Errorf("slug = %q, want the deck's title from the navigation", got)
	}
}

// The clip happens on the title half: a long title must not eat the error
// count the suffix is there to carry.
func TestDeterministicSlugClipsTheTitleNotTheErrorSuffix(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start", Title: "Publishing is the start of a loop, not the end — Acme"},
		{T: 1000, Kind: "network-error", Status: 500, URL: "http://localhost/api"},
	}
	got := DeterministicSlug(events)
	if want := "publishing-is-the-start-of-a-loop-not-t-1-errors"; got != want {
		t.Errorf("slug = %q, want %q", got, want)
	}
	if len(got) > slugMaxLen {
		t.Errorf("slug is %d chars, over the %d cap", len(got), slugMaxLen)
	}
}

func TestDeterministicSlugKeepsTheErrorSuffix(t *testing.T) {
	events := []Event{
		deckClick(1000, deckTitle, "x"),
		{T: 2000, Kind: "network-error", Status: 500, URL: "http://localhost/api"},
		{T: 2100, Kind: "network-error", Status: 404, URL: "http://localhost/favicon.ico"},
		{T: 3000, Kind: "exception", Text: "TypeError"},
	}
	if got := DeterministicSlug(events); got != "q4-2026-plan-worth-coming-back-2-errors" {
		t.Errorf("slug = %q", got)
	}
	if got := DeterministicSlug(nil); got != "session" {
		t.Errorf("empty slug = %q", got)
	}
}

func TestStripSiteSuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{deckTitle, "Q4 2026 Plan — Worth coming back"},
		{"Build fails on main after rebase · GitHub", "Build fails on main after rebase"},
		{"GitHub | Build fails on main after rebase", "Build fails on main after rebase"},
		{"Weekly review notes - Acme Corp", "Weekly review notes"},
		{"Settings — Acme", "Settings — Acme"},
		{"Dashboard - Acme Corp", "Dashboard - Acme Corp"},
		{"recgo-tab fixture", "recgo-tab fixture"},
		{"", ""},
	} {
		if got := stripSiteSuffix(tc.in); got != tc.want {
			t.Errorf("stripSiteSuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
