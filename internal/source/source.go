// Package source fetches GitHub activity and normalizes it into events.
package source

import (
	"context"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// Source is one origin of events.
type Source interface {
	// Poll returns the events that appeared since the previous Poll, in any order.
	Poll(ctx context.Context) ([]event.Event, error)
}
