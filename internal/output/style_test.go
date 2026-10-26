package output

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func TestRow(t *testing.T) {
	e := event.Event{
		Time:   time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC),
		Kind:   event.PR,
		Action: event.Merged,
		Number: 14823,
		Actor:  "a-very-long-login-name-indeed",
		Text:   "#14823 fix(issue create): print issue URL when post-creation mutations fail",
		URL:    "https://github.com/cli/cli/pull/14823",
	}

	full := Row(e, 0)
	if !strings.Contains(full, "\x1b]8;;"+e.URL) {
		t.Errorf("row has no link to %s: %q", e.URL, full)
	}
	if !strings.Contains(full, "a-very-long-login-n…") {
		t.Errorf("actor not truncated to %d cells: %q", actorWidth, full)
	}

	cut := Row(e, 70)
	if w := lipgloss.Width(cut); w > 70 {
		t.Errorf("row is %d cells wide, want at most 70", w)
	}
	if strings.Count(cut, "\x1b]8;") != 2 {
		t.Errorf("truncated row doesn't open and close its link exactly once: %q", cut)
	}
}
