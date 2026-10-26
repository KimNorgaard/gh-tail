package poll

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/source"
)

// fakeSource returns its batches in order and then cancels the test's context.
type fakeSource struct {
	batches [][]event.Event
	errs    []error
	cancel  context.CancelFunc
}

func (f *fakeSource) Poll(context.Context) ([]event.Event, error) {
	if len(f.batches) == 0 {
		f.cancel()
		return nil, nil
	}
	b, err := f.batches[0], f.errs[0]
	f.batches, f.errs = f.batches[1:], f.errs[1:]
	return b, err
}

type recorder struct {
	events   []string
	statuses []Status
}

func (r *recorder) Events(events []event.Event) error {
	for _, e := range events {
		r.events = append(r.events, e.Text)
	}
	return nil
}

func (r *recorder) Status(st Status) { r.statuses = append(r.statuses, st) }

func at(min int, text string) event.Event {
	return event.Event{Time: time.Date(2026, 1, 1, 0, min, 0, 0, time.UTC), Text: text}
}

func TestRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	failing := &fakeSource{
		batches: [][]event.Event{{at(3, "c"), at(1, "a")}, nil},
		errs:    []error{nil, errors.New("boom")},
		cancel:  cancel,
	}
	healthy := &fakeSource{
		batches: [][]event.Event{{at(2, "b")}, {at(4, "d")}},
		errs:    []error{nil, nil},
		cancel:  cancel,
	}
	p := &Poller{Sources: []source.Source{failing, healthy}, Backlog: 2, Transport: source.NewTransport(http.DefaultTransport)}

	rec := &recorder{}
	if err := p.Run(ctx, rec); err != nil {
		t.Fatal(err)
	}

	// The first poll keeps the 2 newest, sorted. In the second, the failing source doesn't
	// hide the healthy one's event.
	if got := rec.events; len(got) != 3 || got[0] != "b" || got[1] != "c" || got[2] != "d" {
		t.Errorf("events = %v, want [b c d]", got)
	}
	var sawErr bool
	for _, st := range rec.statuses {
		sawErr = sawErr || (st.Err != nil && !st.Polling)
	}
	if !sawErr {
		t.Error("the second poll's error never reached the status")
	}
}

func TestRunFirstPollError(t *testing.T) {
	src := &fakeSource{batches: [][]event.Event{nil}, errs: []error{errors.New("no such repo")}}
	p := &Poller{Sources: []source.Source{src}, Transport: source.NewTransport(http.DefaultTransport)}
	if err := p.Run(context.Background(), &recorder{}); err == nil {
		t.Error("Run succeeded, want the first poll's error")
	}
}
