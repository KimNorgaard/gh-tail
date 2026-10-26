package output

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// Column widths of a row. The text column takes the rest.
const (
	timeWidth   = 19
	kindWidth   = 7
	actionWidth = 10
	actorWidth  = 20
	gap         = "  "
)

// Colors are basic ANSI colors, so they follow the terminal's theme.
var (
	kindColors = map[event.Kind]color.Color{
		event.Tag:     lipgloss.Magenta,
		event.Branch:  lipgloss.Blue,
		event.PR:      lipgloss.Green,
		event.Issue:   lipgloss.Yellow,
		event.Release: lipgloss.Cyan,
		event.Package: lipgloss.BrightBlue,
	}
	actionColors = map[string]color.Color{
		event.Opened:    lipgloss.Green,
		event.Created:   lipgloss.Green,
		event.Published: lipgloss.Green,
		event.Merged:    lipgloss.Magenta,
		event.Closed:    lipgloss.Red,
		event.Deleted:   lipgloss.Red,
		event.Pushed:    lipgloss.Blue,
	}

	faint  = lipgloss.NewStyle().Faint(true)
	bold   = lipgloss.NewStyle().Bold(true)
	errorS = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

// Header renders the column titles.
func Header() string {
	cols := []string{
		pad("TIME", timeWidth),
		pad("KIND", kindWidth),
		pad("ACTION", actionWidth),
		pad("ACTOR", actorWidth),
		"TEXT",
	}
	return faint.Render(strings.Join(cols, gap))
}

// Row renders e as one line of at most width cells. A width of 0 or less means no limit.
// The PR or issue number, or the whole text for other kinds, links to e.URL.
func Row(e event.Event, width int) string {
	actor := faint.Render("-")
	if e.Actor != "" {
		st := lipgloss.NewStyle()
		if strings.HasSuffix(e.Actor, "[bot]") {
			st = faint
		}
		actor = st.Render(ansi.Truncate(e.Actor, actorWidth, "…"))
	}
	action := e.Action
	if c, ok := actionColors[action]; ok {
		action = lipgloss.NewStyle().Foreground(c).Render(action)
	} else {
		action = faint.Render(action)
	}

	line := strings.Join([]string{
		faint.Render(e.Time.Local().Format(time.DateTime)),
		pad(lipgloss.NewStyle().Foreground(kindColors[e.Kind]).Render(string(e.Kind)), kindWidth),
		pad(action, actionWidth),
		pad(actor, actorWidth),
		text(e),
	}, gap)
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

func text(e event.Event) string {
	link := lipgloss.NewStyle()
	if e.URL != "" {
		link = link.Hyperlink(e.URL)
	}
	if e.Number > 0 {
		num := fmt.Sprintf("#%d", e.Number)
		if rest, ok := strings.CutPrefix(e.Text, num); ok {
			return link.Bold(true).Render(num) + rest
		}
	}
	return link.Render(e.Text)
}

// pad right-pads s with spaces to w cells, measuring without escape codes.
func pad(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
