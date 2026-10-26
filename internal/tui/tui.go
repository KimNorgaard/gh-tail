// Package tui is the interactive view: a scrolling event list, a details pane and a
// status bar.
package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/cli/go-gh/v2/pkg/markdown"

	"github.com/containdk/KimNorgaard/gh-tail/internal/details"
	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/output"
	"github.com/containdk/KimNorgaard/gh-tail/internal/poll"
)

type Options struct {
	Repo string
	// Theme is the markdown theme for details, such as "dark" or "light".
	Theme string
}

// Run shows the TUI until the user quits or the poller fails.
func Run(ctx context.Context, poller *poll.Poller, fetcher *details.Fetcher, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	p := tea.NewProgram(newModel(ctx, fetcher, opts), tea.WithContext(ctx))
	go func() {
		err := poller.Run(ctx, sink{p})
		p.Send(doneMsg{err})
	}()
	final, err := p.Run()
	if err != nil && !errors.Is(err, tea.ErrProgramKilled) {
		return err
	}
	if m, ok := final.(model); ok {
		return m.err
	}
	return nil
}

type (
	eventsMsg []event.Event
	statusMsg poll.Status
	doneMsg   struct{ err error }
	noticeMsg string
	detailMsg struct {
		id, markdown string
		err          error
	}
)

// sink forwards poll output into the program.
type sink struct{ p *tea.Program }

func (s sink) Events(events []event.Event) error {
	if len(events) > 0 {
		s.p.Send(eventsMsg(events))
	}
	return nil
}

func (s sink) Status(st poll.Status) { s.p.Send(statusMsg(st)) }

var (
	faint   = lipgloss.NewStyle().Faint(true)
	cursorS = lipgloss.NewStyle().Foreground(lipgloss.Magenta)
	errorS  = lipgloss.NewStyle().Foreground(lipgloss.Red)
)

type model struct {
	ctx     context.Context
	fetcher *details.Fetcher
	opts    Options

	events []event.Event
	cursor int
	offset int
	// follow keeps the cursor on the newest event as events arrive.
	follow bool

	width, height int
	status        poll.Status
	spinner       spinner.Model
	notice        string
	err           error

	detail *detail
	// docs caches fetched details by event ID.
	docs map[string]string
}

type detail struct {
	event   event.Event
	vp      viewport.Model
	loading bool
	err     error
}

func newModel(ctx context.Context, fetcher *details.Fetcher, opts Options) model {
	return model{
		ctx:     ctx,
		fetcher: fetcher,
		opts:    opts,
		follow:  true,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		docs:    map[string]string{},
	}
}

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.detail != nil {
			m.detail.vp.SetWidth(m.width)
			m.detail.vp.SetHeight(m.bodyHeight())
			m.renderDetail()
		}
		m.scroll()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case eventsMsg:
		m.events = append(m.events, msg...)
		if m.follow {
			m.cursor = len(m.events) - 1
		}
		m.scroll()
	case statusMsg:
		m.status = poll.Status(msg)
	case doneMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
	case noticeMsg:
		m.notice = string(msg)
	case detailMsg:
		if msg.err == nil {
			m.docs[msg.id] = msg.markdown
		}
		if m.detail != nil && m.detail.event.ID == msg.id {
			m.detail.loading, m.detail.err = false, msg.err
			m.renderDetail()
		}
	case tea.KeyPressMsg:
		m.notice = ""
		if m.detail != nil {
			return m.detailKey(msg)
		}
		return m.listKey(msg)
	case tea.MouseWheelMsg:
		if m.detail != nil {
			var cmd tea.Cmd
			m.detail.vp, cmd = m.detail.vp.Update(msg)
			return m, cmd
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.move(-1)
		case tea.MouseWheelDown:
			m.move(1)
		}
	case tea.MouseClickMsg:
		return m.click(msg)
	}
	return m, nil
}

func (m model) listKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "b":
		m.move(-m.listHeight())
	case "pgdown", "f", "space":
		m.move(m.listHeight())
	case "home", "g":
		m.move(-len(m.events))
	case "end", "G":
		m.move(len(m.events))
	case "enter":
		return m.openDetail()
	case "o":
		if e, ok := m.selected(); ok {
			return m, browse(e.URL)
		}
	}
	return m, nil
}

func (m model) detailKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "backspace", "left", "h":
		m.detail = nil
		return m, nil
	case "o":
		return m, browse(m.detail.event.URL)
	}
	var cmd tea.Cmd
	m.detail.vp, cmd = m.detail.vp.Update(msg)
	return m, cmd
}

// click selects the clicked row, and opens the details of an already selected one.
func (m model) click(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.detail != nil || msg.Button != tea.MouseLeft {
		return m, nil
	}
	row := m.offset + msg.Y - 1
	if msg.Y < 1 || msg.Y > m.listHeight() || row >= len(m.events) {
		return m, nil
	}
	if row == m.cursor {
		return m.openDetail()
	}
	m.move(row - m.cursor)
	return m, nil
}

// move shifts the cursor by n rows and follows new events when it lands on the last row.
func (m *model) move(n int) {
	if len(m.events) == 0 {
		return
	}
	m.cursor = min(max(m.cursor+n, 0), len(m.events)-1)
	m.follow = m.cursor == len(m.events)-1
	m.scroll()
}

// scroll adjusts the offset so the cursor row is visible.
func (m *model) scroll() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(m.offset, 0)
}

func (m model) selected() (event.Event, bool) {
	if m.cursor < 0 || m.cursor >= len(m.events) {
		return event.Event{}, false
	}
	return m.events[m.cursor], true
}

func (m model) openDetail() (tea.Model, tea.Cmd) {
	e, ok := m.selected()
	if !ok {
		return m, nil
	}
	m.detail = &detail{event: e, vp: viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.bodyHeight()))}
	if _, ok := m.docs[e.ID]; ok {
		m.renderDetail()
		return m, nil
	}
	m.detail.loading = true
	m.renderDetail()
	return m, m.fetch(e)
}

func (m model) fetch(e event.Event) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		defer cancel()
		md, err := m.fetcher.Markdown(ctx, e)
		return detailMsg{id: e.ID, markdown: md, err: err}
	}
}

func (m *model) renderDetail() {
	d := m.detail
	switch {
	case d.loading:
		d.vp.SetContent(m.spinner.View() + " loading…")
	case d.err != nil:
		d.vp.SetContent(errorS.Render(d.err.Error()))
	default:
		out, err := markdown.Render(m.docs[d.event.ID], markdown.WithTheme(m.opts.Theme), markdown.WithWrap(m.width))
		if err != nil {
			out = errorS.Render(err.Error())
		}
		d.vp.SetContent(out)
	}
}

func browse(url string) tea.Cmd {
	return func() tea.Msg {
		if url == "" {
			return noticeMsg("this event has no link")
		}
		if err := browser.New("", io.Discard, io.Discard).Browse(url); err != nil {
			return noticeMsg("open in browser: " + err.Error())
		}
		return noticeMsg("opened " + url)
	}
}

// listHeight is the number of event rows: the screen minus the header and status bar.
func (m model) listHeight() int { return max(m.height-2, 1) }

// bodyHeight is the details viewport height: the screen minus the title and status bar.
func (m model) bodyHeight() int { return max(m.height-2, 1) }

func (m model) View() tea.View {
	var content string
	if m.detail != nil {
		content = m.detailView()
	} else {
		content = m.listView()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "gh tail " + m.opts.Repo
	return v
}

func (m model) listView() string {
	lines := make([]string, 0, m.height)
	lines = append(lines, ansi.Truncate("  "+output.Header(), m.width, ""))
	for i := m.offset; i < m.offset+m.listHeight(); i++ {
		switch {
		case i < len(m.events):
			gutter := "  "
			if i == m.cursor {
				gutter = cursorS.Render("▌ ")
			}
			lines = append(lines, gutter+output.Row(m.events[i], m.width-2))
		case i == 0:
			lines = append(lines, faint.Render("  waiting for events…"))
		default:
			lines = append(lines, "")
		}
	}
	return strings.Join(append(lines, m.statusBar("↑↓ move · enter details · o open · q quit")), "\n")
}

func (m model) detailView() string {
	e := m.detail.event
	title := ansi.Truncate(output.Row(e, m.width), m.width, "…")
	hints := "↑↓ scroll · o open · esc back"
	if !m.detail.loading && m.detail.err == nil {
		hints = fmt.Sprintf("%.0f%% · %s", m.detail.vp.ScrollPercent()*100, hints)
	}
	return strings.Join([]string{title, m.detail.vp.View(), m.statusBar(hints)}, "\n")
}

// statusBar puts the poll status on the left and key hints, or a notice, on the right.
func (m model) statusBar(hints string) string {
	left := output.StatusLine(m.status, m.opts.Repo, m.spinner.View(), 0) +
		faint.Render(" · "+plural(len(m.events), "event"))
	right := faint.Render(hints)
	if m.notice != "" {
		right = m.notice
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 2 {
		return ansi.Truncate(left, m.width, "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.Itoa(n) + " " + word + "s"
}
