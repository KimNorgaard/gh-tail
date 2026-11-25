package source

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// reviewPRs is how many recently updated PRs each poll checks for new reviews.
const reviewPRs = 30

// reviewActions maps a review state to its action. Pending reviews aren't visible to
// others yet, and dismissal changes an existing review, so neither is reported.
var reviewActions = map[string]string{
	"APPROVED":          event.Approved,
	"CHANGES_REQUESTED": event.ChangesRequested,
	"COMMENTED":         event.Reviewed,
}

// Reviews reports submitted PR reviews. GitHub has no repository-wide review list, so it
// reads the reviews of each PR whose updated_at moved since the previous poll. A review
// always moves it.
type Reviews struct {
	client  *api.RESTClient
	repo    repository.Repository
	opts    Options
	started bool
	updated map[int]time.Time
	window  *window
}

// NewReviews returns a source whose first poll reads the reviews of the opts.Backlog most
// recently updated PRs.
func NewReviews(client *api.RESTClient, repo repository.Repository, opts Options) *Reviews {
	return &Reviews{client: client, repo: repo, opts: opts, updated: map[int]time.Time{}, window: newWindow()}
}

type apiListPR struct {
	Number    int        `json:"number"`
	Title     string     `json:"title"`
	User      apiUser    `json:"user"`
	Labels    []apiLabel `json:"labels"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type apiReview struct {
	ID          int64      `json:"id"`
	State       string     `json:"state"`
	Body        string     `json:"body"`
	HTMLURL     string     `json:"html_url"`
	User        apiUser    `json:"user"`
	SubmittedAt *time.Time `json:"submitted_at"`
}

func (s *Reviews) Poll(ctx context.Context) ([]event.Event, error) {
	var prs []apiListPR
	p := fmt.Sprintf("repos/%s/%s/pulls?state=all&sort=updated&direction=desc&per_page=%d", s.repo.Owner, s.repo.Name, reviewPRs)
	if err := s.client.DoWithContext(ctx, http.MethodGet, p, nil, &prs); err != nil {
		return nil, fmt.Errorf("list pull requests: %w", err)
	}

	var changed []apiListPR
	var newest time.Time
	for i, pr := range prs {
		if pr.UpdatedAt.After(newest) {
			newest = pr.UpdatedAt
		}
		if s.started {
			if !pr.UpdatedAt.Equal(s.updated[pr.Number]) {
				changed = append(changed, pr)
			}
		} else if s.opts.Backlog < 0 || i < s.opts.Backlog {
			changed = append(changed, pr)
		}
	}

	batches, err := completeAll(changed, func(pr apiListPR) ([]event.Event, error) {
		return s.reviews(ctx, pr)
	})
	if err != nil {
		return nil, err
	}

	// Record updated_at only once every lookup succeeded, so a failed poll is retried
	// whole.
	for _, pr := range prs {
		s.updated[pr.Number] = pr.UpdatedAt
	}
	var out []event.Event
	for _, batch := range batches {
		for _, e := range batch {
			if s.window.admit(e) {
				out = append(out, e)
			}
		}
	}
	if !s.started {
		// Raise the floor to the newest PR update, so a later lookup of an older PR
		// doesn't replay its past reviews, even when the backlog showed none.
		if newest.After(s.window.newest) {
			s.window.newest = newest
		}
		s.started = true
	}
	s.window.advance()
	return out, nil
}

func (s *Reviews) reviews(ctx context.Context, pr apiListPR) ([]event.Event, error) {
	var reviews []apiReview
	p := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews?per_page=100", s.repo.Owner, s.repo.Name, pr.Number)
	if err := s.client.DoWithContext(ctx, http.MethodGet, p, nil, &reviews); err != nil {
		return nil, fmt.Errorf("list reviews of #%d: %w", pr.Number, err)
	}
	var out []event.Event
	for _, r := range reviews {
		action, ok := reviewActions[r.State]
		if !ok || r.SubmittedAt == nil || !s.opts.Selection.Has(event.PR, action) {
			continue
		}
		text := fmt.Sprintf("#%d %s", pr.Number, pr.Title)
		if body := excerpt(r.Body); body != "" {
			text += ": " + body
		}
		out = append(out, event.Event{
			ID:     "review:" + strconv.FormatInt(r.ID, 10),
			Time:   *r.SubmittedAt,
			Repo:   s.repo.Owner + "/" + s.repo.Name,
			Kind:   event.PR,
			Action: action,
			Number: pr.Number,
			Actor:  r.User.Login,
			Author: pr.User.Login,
			Labels: labelNames(pr.Labels),
			Text:   strings.TrimSpace(text),
			URL:    r.HTMLURL,
		})
	}
	return out, nil
}
