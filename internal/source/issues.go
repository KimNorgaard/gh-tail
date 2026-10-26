package source

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// Issues derives opened, closed and merged events for issues and PRs from the issues
// list, which includes PRs. The list is current, unlike the Events API. It has no actor
// for closes and merges, so each new one costs a lookup.
type Issues struct {
	client  *api.RESTClient
	repo    repository.Repository
	opts    Options
	started bool
	window  *window
}

// NewIssues returns a source whose first poll keeps only the newest opts.Backlog events.
func NewIssues(client *api.RESTClient, repo repository.Repository, opts Options) *Issues {
	return &Issues{client: client, repo: repo, opts: opts, window: newWindow()}
}

type apiListIssue struct {
	Number      int             `json:"number"`
	Title       string          `json:"title"`
	HTMLURL     string          `json:"html_url"`
	User        apiUser         `json:"user"`
	Labels      []apiLabel      `json:"labels"`
	CreatedAt   time.Time       `json:"created_at"`
	ClosedAt    *time.Time      `json:"closed_at"`
	PullRequest *apiIssuePRLink `json:"pull_request"`
}

type apiIssuePRLink struct {
	MergedAt *time.Time `json:"merged_at"`
}

func (s *Issues) Poll(ctx context.Context) ([]event.Event, error) {
	var items []apiListIssue
	path := fmt.Sprintf("repos/%s/%s/issues?state=all&sort=updated&direction=desc&per_page=100", s.repo.Owner, s.repo.Name)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &items); err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}

	var all, found []event.Event
	for _, it := range items {
		for _, e := range s.events(it) {
			all = append(all, e)
			if s.window.fresh(e) && s.opts.Selection.Has(e.Kind, e.Action) {
				found = append(found, e)
			}
		}
	}
	limit := -1
	if !s.started {
		limit = s.opts.Backlog
	}
	out, err := backfill(found, limit, func(e event.Event) time.Time { return e.Time }, func(e event.Event) (event.Event, error) {
		if e.Action == event.Opened {
			return e, nil
		}
		return e, s.lookupActor(ctx, &e)
	}, s.opts.Filter)
	if err != nil {
		return nil, err
	}

	// Record events only once every lookup succeeded, so a failed poll is retried whole.
	for _, e := range all {
		s.window.admit(e)
	}
	s.window.advance()
	s.started = true
	return out, nil
}

type apiActorLookup struct {
	ClosedBy apiUser `json:"closed_by"`
	MergedBy apiUser `json:"merged_by"`
}

// lookupActor sets the actor of a closed or merged event, which the list omits.
func (s *Issues) lookupActor(ctx context.Context, e *event.Event) error {
	endpoint := "issues"
	if e.Action == event.Merged {
		endpoint = "pulls"
	}
	path := fmt.Sprintf("repos/%s/%s/%s/%d", s.repo.Owner, s.repo.Name, endpoint, e.Number)
	var r apiActorLookup
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &r); err != nil {
		return fmt.Errorf("get %s #%d: %w", e.Kind, e.Number, err)
	}
	e.Actor = r.ClosedBy.Login
	if e.Action == event.Merged {
		e.Actor = r.MergedBy.Login
	}
	return nil
}

func (s *Issues) events(it apiListIssue) []event.Event {
	kind := event.Issue
	if it.PullRequest != nil {
		kind = event.PR
	}
	base := event.Event{
		Repo:   s.repo.Owner + "/" + s.repo.Name,
		Kind:   kind,
		Number: it.Number,
		Author: it.User.Login,
		Labels: labelNames(it.Labels),
		Text:   fmt.Sprintf("#%d %s", it.Number, it.Title),
		URL:    it.HTMLURL,
	}

	opened := base
	opened.Action, opened.Time, opened.Actor = event.Opened, it.CreatedAt, it.User.Login
	out := []event.Event{opened}

	switch {
	case it.PullRequest != nil && it.PullRequest.MergedAt != nil:
		merged := base
		merged.Action, merged.Time = event.Merged, *it.PullRequest.MergedAt
		out = append(out, merged)
	case it.ClosedAt != nil:
		closed := base
		closed.Action, closed.Time = event.Closed, *it.ClosedAt
		out = append(out, closed)
	}

	// A reopened and closed again item gets a new closed_at, so the time is part of the ID.
	for i := range out {
		out[i].ID = fmt.Sprintf("%s:%d:%s:%s", kind, it.Number, out[i].Action, strconv.FormatInt(out[i].Time.Unix(), 10))
	}
	return out
}
