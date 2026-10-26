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

// Releases reports published releases from the releases list.
type Releases struct {
	client *api.RESTClient
	repo   repository.Repository
	window *window
}

func NewReleases(client *api.RESTClient, repo repository.Repository) *Releases {
	return &Releases{client: client, repo: repo, window: newWindow()}
}

type apiListRelease struct {
	ID          int64      `json:"id"`
	TagName     string     `json:"tag_name"`
	Name        string     `json:"name"`
	HTMLURL     string     `json:"html_url"`
	Author      apiUser    `json:"author"`
	PublishedAt *time.Time `json:"published_at"`
}

func (s *Releases) Poll(ctx context.Context) ([]event.Event, error) {
	var releases []apiListRelease
	path := fmt.Sprintf("repos/%s/%s/releases?per_page=30", s.repo.Owner, s.repo.Name)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &releases); err != nil {
		return nil, fmt.Errorf("list releases: %w", err)
	}

	var out []event.Event
	for _, r := range releases {
		// Drafts have no published_at.
		if r.PublishedAt == nil {
			continue
		}
		e := event.Event{
			ID:     "release:" + strconv.FormatInt(r.ID, 10),
			Time:   *r.PublishedAt,
			Repo:   s.repo.Owner + "/" + s.repo.Name,
			Kind:   event.Release,
			Action: event.Published,
			Actor:  r.Author.Login,
			Author: r.Author.Login,
			Ref:    r.TagName,
			Text:   r.TagName,
			URL:    r.HTMLURL,
		}
		if r.Name != "" && r.Name != r.TagName {
			e.Text += " " + r.Name
		}
		if s.window.admit(e) {
			out = append(out, e)
		}
	}
	s.window.advance()
	return out, nil
}
