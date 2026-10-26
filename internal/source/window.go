package source

import (
	"time"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// lateness is how far behind the newest event an event may become visible and still be
// reported.
const lateness = 10 * time.Minute

// window dedups events derived from list endpoints, which return the same items on every
// poll. It drops events older than the newest one seen minus lateness, so an item that
// falls off a list and comes back doesn't replay its past events. The floor follows event
// timestamps, so the local clock doesn't matter.
type window struct {
	floor  time.Time
	newest time.Time
	seen   map[string]time.Time
}

func newWindow() *window {
	return &window{seen: map[string]time.Time{}}
}

// fresh reports whether e is above the floor and not yet admitted.
func (w *window) fresh(e event.Event) bool {
	_, seen := w.seen[e.ID]
	return e.Time.After(w.floor) && !seen
}

// admit reports whether e is fresh, and records it.
func (w *window) admit(e event.Event) bool {
	if !w.fresh(e) {
		return false
	}
	w.seen[e.ID] = e.Time
	if e.Time.After(w.newest) {
		w.newest = e.Time
	}
	return true
}

// advance raises the floor after a poll and forgets events below it.
func (w *window) advance() {
	w.floor = w.newest.Add(-lateness)
	for id, t := range w.seen {
		if !t.After(w.floor) {
			delete(w.seen, id)
		}
	}
}
