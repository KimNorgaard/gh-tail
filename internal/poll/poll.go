// Package poll runs the sources on an interval and hands events and status to a sink.
package poll

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/source"
)

// Status describes the poll loop for display.
type Status struct {
	// Polling is true while a poll runs.
	Polling bool
	// Next is when the next poll starts. It is zero while polling.
	Next time.Time
	// Err is the error of the last poll, or nil.
	Err error
	// Requests and Cached count HTTP requests, and those answered by the ETag cache.
	Requests, Cached int64
}

// Sink receives what the poll loop produces.
type Sink interface {
	// Events receives each poll's new events, oldest first.
	Events(events []event.Event) error
	// Status receives the poll state when a poll starts and ends.
	Status(st Status)
}

type Poller struct {
	Sources   []source.Source
	Filter    event.Filter
	Backlog   int
	Interval  time.Duration
	Transport *source.Transport
}

// slot schedules one source. Its interval is Interval, raised to the X-Poll-Interval its
// own requests receive.
type slot struct {
	src      source.Source
	interval source.PollInterval
	due      time.Time
}

// Run polls until ctx is done. An error on the first poll is returned, since it usually
// means a bad repository or a missing scope. Later errors go to the sink's status.
func (p *Poller) Run(ctx context.Context, sink Sink) error {
	slots := make([]*slot, len(p.Sources))
	for i, src := range p.Sources {
		slots[i] = &slot{src: src}
	}

	// The first poll shows only the newest Backlog events. Later polls show all.
	for limit := p.Backlog; ; limit = -1 {
		sink.Status(p.status(Status{Polling: true}))
		events, err := p.poll(ctx, slots, limit)
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err != nil && limit >= 0 {
			return err
		}
		if err := sink.Events(events); err != nil {
			return err
		}

		next := slots[0].due
		for _, s := range slots[1:] {
			if s.due.Before(next) {
				next = s.due
			}
		}
		sink.Status(p.status(Status{Next: next, Err: err}))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
		}
	}
}

// poll runs every due source, so one failing source doesn't hide the others' events.
func (p *Poller) poll(ctx context.Context, slots []*slot, limit int) ([]event.Event, error) {
	var batch []event.Event
	var errs []error
	now := time.Now()
	for _, s := range slots {
		if s.due.After(now) {
			continue
		}
		events, err := s.src.Poll(source.WithPollInterval(ctx, &s.interval))
		s.due = now.Add(max(p.Interval, s.interval.Get()))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		batch = append(batch, events...)
	}

	batch = slices.DeleteFunc(batch, func(e event.Event) bool { return !p.Filter.Match(e) })
	slices.SortStableFunc(batch, func(a, b event.Event) int { return a.Time.Compare(b.Time) })
	if limit >= 0 && len(batch) > limit {
		batch = batch[len(batch)-limit:]
	}
	return batch, errors.Join(errs...)
}

func (p *Poller) status(st Status) Status {
	st.Requests, st.Cached = p.Transport.Stats()
	return st
}
