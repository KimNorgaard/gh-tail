package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func update(t *testing.T, m model, msgs ...tea.Msg) model {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(model)
	}
	return m
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func TestFollowAndDetails(t *testing.T) {
	m := newModel(context.Background(), nil, Options{Repo: "o/r", Theme: "notty"})
	m = update(t, m,
		tea.WindowSizeMsg{Width: 100, Height: 5},
		eventsMsg{{ID: "1", Text: "one"}, {ID: "2", Text: "two"}, {ID: "3", Text: "three"}, {ID: "4", Text: "four"}},
	)
	if m.cursor != 3 || m.offset != 1 {
		t.Fatalf("after first batch cursor=%d offset=%d, want 3 and 1", m.cursor, m.offset)
	}

	// Moving up stops following, so a new event leaves the cursor in place.
	m = update(t, m, key("up"), eventsMsg{{ID: "5", Text: "five"}})
	if m.cursor != 2 || m.follow {
		t.Fatalf("after up and a new event cursor=%d follow=%v, want 2 and false", m.cursor, m.follow)
	}

	// Cached details open without a fetch, and esc closes them.
	m.docs["3"] = "# three"
	m = update(t, m, key("enter"))
	if m.detail == nil || m.detail.event.ID != "3" || m.detail.loading {
		t.Fatalf("enter didn't open the cached details of event 3: %+v", m.detail)
	}
	m = update(t, m, key("esc"))
	if m.detail != nil {
		t.Fatal("esc didn't close the details")
	}

	// G jumps to the newest event and follows again.
	m = update(t, m, key("G"))
	if m.cursor != 4 || !m.follow {
		t.Fatalf("after G cursor=%d follow=%v, want 4 and true", m.cursor, m.follow)
	}
}
