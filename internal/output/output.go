// Package output renders events for a terminal or as JSON.
package output

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"charm.land/bubbles/v2/spinner"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/poll"
)

// JSON writes one JSON object per event and reports poll errors on errOut.
type JSON struct {
	enc    *json.Encoder
	errOut io.Writer
}

func NewJSON(w, errOut io.Writer) *JSON {
	return &JSON{enc: json.NewEncoder(w), errOut: errOut}
}

func (j *JSON) Events(events []event.Event) error {
	for _, e := range events {
		if err := j.enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func (j *JSON) Status(st poll.Status) {
	if st.Err != nil {
		_, _ = fmt.Fprintln(j.errOut, "gh-tail:", st.Err)
	}
}

// Stream writes rows as they arrive. On a terminal it keeps a status line below the
// rows, which Animate redraws. Elsewhere it writes plain rows and reports poll errors on
// errOut.
type Stream struct {
	mu      sync.Mutex
	out     io.Writer
	errOut  io.Writer
	repo    string
	width   func() int
	tty     bool
	header  bool
	status  poll.Status
	spinner spinner.Model
}

// NewStream writes to out, downsampling colors and dropping links where out doesn't
// support them. width returns the terminal width, and tty enables the status line.
func NewStream(out *os.File, errOut io.Writer, repo string, tty bool, width func() int) *Stream {
	return &Stream{
		out:     colorprofile.NewWriter(out, os.Environ()),
		errOut:  errOut,
		repo:    repo,
		width:   width,
		tty:     tty,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot)),
	}
}

func (s *Stream) Events(events []event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(events) == 0 {
		return nil
	}
	s.clearStatus()
	if !s.header {
		if _, err := fmt.Fprintln(s.out, Header()); err != nil {
			return err
		}
		s.header = true
	}
	width := 0
	if s.tty {
		width = s.width()
	}
	for _, e := range events {
		if _, err := fmt.Fprintln(s.out, Row(e, width)); err != nil {
			return err
		}
	}
	s.drawStatus()
	return nil
}

func (s *Stream) Status(st poll.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = st
	if !s.tty {
		if st.Err != nil {
			_, _ = fmt.Fprintln(s.errOut, "gh-tail:", st.Err)
		}
		return
	}
	s.clearStatus()
	s.drawStatus()
}

// Animate redraws the status line until ctx is done, then clears it.
func (s *Stream) Animate(ctx context.Context) {
	if !s.tty {
		return
	}
	t := time.NewTicker(s.spinner.Spinner.FPS)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.clearStatus()
			s.mu.Unlock()
			return
		case <-t.C:
			s.mu.Lock()
			s.spinner, _ = s.spinner.Update(s.spinner.Tick())
			s.clearStatus()
			s.drawStatus()
			s.mu.Unlock()
		}
	}
}

func (s *Stream) clearStatus() {
	if s.tty {
		_, _ = io.WriteString(s.out, "\r\x1b[2K")
	}
}

func (s *Stream) drawStatus() {
	if s.tty {
		_, _ = io.WriteString(s.out, StatusLine(s.status, s.repo, s.spinner.View(), s.width()))
	}
}

// StatusLine renders the poll state, such as "● cli/cli · next poll in 42s · 15 requests,
// 13 cached", truncated to width. A width of 0 or less means no limit.
func StatusLine(st poll.Status, repo, spin string, width int) string {
	var line string
	if st.Polling {
		line = spin + " polling " + bold.Render(repo) + "…"
	} else {
		next := max(time.Until(st.Next).Round(time.Second), 0)
		line = faint.Render("●") + " " + bold.Render(repo) + faint.Render(fmt.Sprintf(" · next poll in %s", next))
	}
	line += faint.Render(fmt.Sprintf(" · %d requests, %d cached", st.Requests, st.Cached))
	if st.Err != nil {
		line += " " + errorS.Render("· "+st.Err.Error())
	}
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}
