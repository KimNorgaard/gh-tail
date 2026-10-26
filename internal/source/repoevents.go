package source

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// RepoEvents polls the repository Events API for the PR and issue actions that the issues
// list can't show, such as labeled and reopened. The Events API lags by 30 seconds to
// 6 hours, so it only covers what nothing else does.
type RepoEvents struct {
	client  *api.RESTClient
	repo    repository.Repository
	opts    Options
	started bool
	seen    map[string]bool
}

// NewRepoEvents returns a source whose first poll keeps only the newest opts.Backlog events.
func NewRepoEvents(client *api.RESTClient, repo repository.Repository, opts Options) *RepoEvents {
	return &RepoEvents{client: client, repo: repo, opts: opts, seen: map[string]bool{}}
}

type apiEvent struct {
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	CreatedAt time.Time  `json:"created_at"`
	Actor     apiUser    `json:"actor"`
	Payload   apiPayload `json:"payload"`
}

type apiUser struct {
	Login string `json:"login"`
}

// apiPayload is the union of the payload fields used across event types.
type apiPayload struct {
	Action   string   `json:"action"`
	Ref      string   `json:"ref"`
	RefType  string   `json:"ref_type"`
	Head     string   `json:"head"`
	Number   int      `json:"number"`
	Issue    apiIssue `json:"issue"`
	Label    apiLabel `json:"label"`
	Assignee apiUser  `json:"assignee"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type apiIssue struct {
	Number  int        `json:"number"`
	Title   string     `json:"title"`
	HTMLURL string     `json:"html_url"`
	User    apiUser    `json:"user"`
	Labels  []apiLabel `json:"labels"`
}

type apiPullRequest struct {
	Title   string     `json:"title"`
	HTMLURL string     `json:"html_url"`
	User    apiUser    `json:"user"`
	Labels  []apiLabel `json:"labels"`
}

func (s *RepoEvents) Poll(ctx context.Context) ([]event.Event, error) {
	raw, err := listEvents(ctx, s.client, s.repo)
	if err != nil {
		return nil, err
	}

	var found []candidate
	for _, r := range raw {
		if s.seen[r.ID] {
			continue
		}
		if e, ok := s.convert(r); ok {
			found = append(found, candidate{e, r.Payload})
		}
	}
	limit := -1
	if !s.started {
		limit = s.opts.Backlog
	}
	out, err := backfill(found, limit, func(c candidate) time.Time { return c.event.Time }, func(c candidate) (event.Event, error) {
		if c.event.Kind != event.PR {
			return c.event, nil
		}
		return c.event, s.lookupPR(ctx, &c.event, c.payload)
	}, s.opts.Filter)
	if err != nil {
		return nil, err
	}

	// The response is a sliding window of recent events, so only its IDs need remembering.
	// They are recorded after the lookups, so a failed poll is retried whole.
	s.seen = make(map[string]bool, len(raw))
	for _, r := range raw {
		s.seen[r.ID] = true
	}
	s.started = true
	return out, nil
}

type candidate struct {
	event   event.Event
	payload apiPayload
}

func listEvents(ctx context.Context, client *api.RESTClient, repo repository.Repository) ([]apiEvent, error) {
	var raw []apiEvent
	path := fmt.Sprintf("repos/%s/%s/events?per_page=100", repo.Owner, repo.Name)
	if err := client.DoWithContext(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	return raw, nil
}

func (s *RepoEvents) convert(r apiEvent) (event.Event, bool) {
	p := r.Payload
	e := event.Event{
		ID:    "event:" + r.ID,
		Time:  r.CreatedAt,
		Repo:  s.repo.Owner + "/" + s.repo.Name,
		Actor: r.Actor.Login,
	}

	switch r.Type {
	case "PullRequestEvent":
		e.Kind, e.Action, e.Number = event.PR, p.Action, p.Number
	case "IssuesEvent":
		e.Kind, e.Action, e.Number = event.Issue, p.Action, p.Issue.Number
		e.Author, e.Labels = p.Issue.User.Login, labelNames(p.Issue.Labels)
		e.Text = fmt.Sprintf("#%d %s%s", p.Issue.Number, p.Issue.Title, detail(p))
		e.URL = p.Issue.HTMLURL
	}
	ok := e.Kind != "" && s.opts.Selection.Has(e.Kind, e.Action) && !listed(e.Kind, e.Action)
	return e, ok
}

// lookupPR fills in the text, author and labels of a PR event, which the Events API
// payload omits.
func (s *RepoEvents) lookupPR(ctx context.Context, e *event.Event, p apiPayload) error {
	var pr apiPullRequest
	path := fmt.Sprintf("repos/%s/%s/pulls/%d", s.repo.Owner, s.repo.Name, e.Number)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &pr); err != nil {
		return fmt.Errorf("get pull request #%d: %w", e.Number, err)
	}
	e.Text = fmt.Sprintf("#%d %s%s", e.Number, pr.Title, detail(p))
	e.URL = pr.HTMLURL
	e.Author, e.Labels = pr.User.Login, labelNames(pr.Labels)
	return nil
}

func labelNames(labels []apiLabel) []string {
	names := make([]string, 0, len(labels))
	for _, l := range labels {
		names = append(names, l.Name)
	}
	return names
}

// detail names the label or assignee that a labeled or assigned action changed.
func detail(p apiPayload) string {
	switch {
	case p.Label.Name != "":
		return " (" + p.Label.Name + ")"
	case p.Assignee.Login != "":
		return " (@" + p.Assignee.Login + ")"
	}
	return ""
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
