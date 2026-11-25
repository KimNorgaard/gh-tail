package source

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// commentLength caps the comment body shown in an event's text.
const commentLength = 200

// Comments reports new comments on issues and PRs from the repository-wide comment
// lists, which are current, unlike the Events API. It reads conversation comments and
// inline review comments.
type Comments struct {
	client    *api.RESTClient
	repo      repository.Repository
	selection event.Selection
	window    *window
}

func NewComments(client *api.RESTClient, repo repository.Repository, selection event.Selection) *Comments {
	return &Comments{client: client, repo: repo, selection: selection, window: newWindow()}
}

type apiComment struct {
	ID             int64     `json:"id"`
	Body           string    `json:"body"`
	HTMLURL        string    `json:"html_url"`
	IssueURL       string    `json:"issue_url"`
	PullRequestURL string    `json:"pull_request_url"`
	Path           string    `json:"path"`
	Line           int       `json:"line"`
	OriginalLine   int       `json:"original_line"`
	User           apiUser   `json:"user"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *Comments) Poll(ctx context.Context) ([]event.Event, error) {
	// Both lists are fetched before any event is admitted, so a failed request is retried
	// whole.
	var conversation, review []apiComment
	if s.selection.Has(event.PR, event.Commented) || s.selection.Has(event.Issue, event.Commented) {
		p := fmt.Sprintf("repos/%s/%s/issues/comments?sort=updated&direction=desc&per_page=100", s.repo.Owner, s.repo.Name)
		if err := s.client.DoWithContext(ctx, http.MethodGet, p, nil, &conversation); err != nil {
			return nil, fmt.Errorf("list issue comments: %w", err)
		}
	}
	if s.selection.Has(event.PR, event.Commented) {
		p := fmt.Sprintf("repos/%s/%s/pulls/comments?sort=updated&direction=desc&per_page=100", s.repo.Owner, s.repo.Name)
		if err := s.client.DoWithContext(ctx, http.MethodGet, p, nil, &review); err != nil {
			return nil, fmt.Errorf("list review comments: %w", err)
		}
	}

	var out []event.Event
	add := func(e event.Event) {
		if s.selection.Has(e.Kind, e.Action) && s.window.admit(e) {
			out = append(out, e)
		}
	}
	for _, c := range conversation {
		kind := event.Issue
		if strings.Contains(c.HTMLURL, "/pull/") {
			kind = event.PR
		}
		add(s.event("comment:", kind, number(c.IssueURL), "", c))
	}
	for _, c := range review {
		line := c.Line
		if line == 0 {
			line = c.OriginalLine
		}
		where := c.Path
		if where != "" && line > 0 {
			where += ":" + strconv.Itoa(line)
		}
		add(s.event("review_comment:", event.PR, number(c.PullRequestURL), where, c))
	}
	s.window.advance()
	return out, nil
}

func (s *Comments) event(prefix string, kind event.Kind, num int, where string, c apiComment) event.Event {
	text := fmt.Sprintf("#%d", num)
	if where != "" {
		text += " " + where
	}
	if body := excerpt(c.Body); body != "" {
		text += ": " + body
	}
	return event.Event{
		ID:     prefix + strconv.FormatInt(c.ID, 10),
		Time:   c.CreatedAt,
		Repo:   s.repo.Owner + "/" + s.repo.Name,
		Kind:   kind,
		Action: event.Commented,
		Number: num,
		Actor:  c.User.Login,
		Text:   text,
		URL:    c.HTMLURL,
	}
}

// number returns the issue or PR number at the end of an API URL, or 0.
func number(url string) int {
	n, _ := strconv.Atoi(path.Base(url))
	return n
}

// htmlComment matches the hidden HTML comments that bots put in Markdown bodies.
var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// excerpt flattens body to one line of at most commentLength characters, without hidden
// HTML comments, so an event stays one line.
func excerpt(body string) string {
	s := strings.Join(strings.Fields(htmlComment.ReplaceAllString(body, " ")), " ")
	if r := []rune(s); len(r) > commentLength {
		s = string(r[:commentLength-1]) + "…"
	}
	return s
}
