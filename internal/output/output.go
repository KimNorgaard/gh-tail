// Package output renders events.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// Printer writes one event.
type Printer func(event.Event) error

// JSON writes one JSON object per line.
func JSON(w io.Writer) Printer {
	enc := json.NewEncoder(w)
	return func(e event.Event) error { return enc.Encode(e) }
}

const (
	rowFormat  = "%-19s  %-7s  %-10s  %-20s  %s\n"
	actorWidth = 20
)

// Table writes fixed-width rows. It pads instead of using tabwriter because rows stream.
func Table(w io.Writer) Printer {
	header := false
	return func(e event.Event) error {
		if !header {
			if _, err := fmt.Fprintf(w, rowFormat, "TIME", "KIND", "ACTION", "ACTOR", "TEXT"); err != nil {
				return err
			}
			header = true
		}
		_, err := fmt.Fprintf(w, rowFormat, e.Time.Local().Format(time.DateTime), e.Kind, e.Action, truncate(e.Actor, actorWidth), e.Text)
		return err
	}
}

// truncate shortens s to n runes, marking the cut with an ellipsis.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
