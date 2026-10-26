package source

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// actorPolls is how many polls a ref change waits for its Events API entry, which names
// the actor and the exact time, before it prints without them.
const actorPolls = 3

// Refs diffs a repository's tags or branches between polls. Refs carry no timestamp or
// actor, so each change waits for the matching Events API entry, for at most actorPolls
// polls. The first poll records a baseline and, for tags, returns the newest tags as
// backlog.
type Refs struct {
	client   *api.RESTClient
	gql      *api.GraphQLClient
	repo     repository.Repository
	sel      event.Selection
	kind     event.Kind
	prefix   string
	prev     map[string]string
	prevPoll time.Time
	pending  []pendingRef
	now      func() time.Time
}

type pendingRef struct {
	event event.Event
	key   string
	since time.Time
	polls int
}

// NewRefs watches tags for event.Tag and branches for event.Branch. Without gql, tags
// have no backlog.
func NewRefs(client *api.RESTClient, gql *api.GraphQLClient, repo repository.Repository, sel event.Selection, kind event.Kind) *Refs {
	prefix := "heads"
	if kind == event.Tag {
		prefix = "tags"
	}
	return &Refs{client: client, gql: gql, repo: repo, sel: sel, kind: kind, prefix: prefix, now: time.Now}
}

type apiRef struct {
	Ref    string       `json:"ref"`
	Object apiRefObject `json:"object"`
}

type apiRefObject struct {
	SHA string `json:"sha"`
}

func (s *Refs) Poll(ctx context.Context) ([]event.Event, error) {
	var refs []apiRef
	path := fmt.Sprintf("repos/%s/%s/git/matching-refs/%s", s.repo.Owner, s.repo.Name, s.prefix)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &refs); err != nil {
		return nil, fmt.Errorf("list %s: %w", s.prefix, err)
	}

	cur := make(map[string]string, len(refs))
	for _, r := range refs {
		cur[strings.TrimPrefix(r.Ref, "refs/"+s.prefix+"/")] = r.Object.SHA
	}
	now := s.now()
	if s.prev == nil {
		var backlog []event.Event
		if s.gql != nil && s.kind == event.Tag && s.sel.Has(event.Tag, event.Created) {
			var err error
			if backlog, err = s.recentTags(ctx); err != nil {
				return nil, err
			}
		}
		s.prev, s.prevPoll = cur, now
		return backlog, nil
	}
	s.pending = append(s.pending, s.diff(cur, now)...)
	s.prev, s.prevPoll = cur, now
	if len(s.pending) == 0 {
		return nil, nil
	}

	raw, err := listEvents(ctx, s.client, s.repo)
	if err != nil {
		return nil, err
	}
	// The list is newest first, so the first entry per key is the latest.
	entries := map[string]apiEvent{}
	for _, r := range raw {
		if key := eventRefKey(r); key != "" {
			if _, ok := entries[key]; !ok {
				entries[key] = r
			}
		}
	}

	var out []event.Event
	keep := s.pending[:0]
	for _, p := range s.pending {
		p.polls++
		// An entry from before the change belongs to an earlier change to the same ref.
		// lateness absorbs skew between the local clock and GitHub's.
		if r, ok := entries[p.key]; ok && r.CreatedAt.After(p.since.Add(-lateness)) {
			p.event.Actor, p.event.Time = r.Actor.Login, r.CreatedAt
			out = append(out, p.event)
			continue
		}
		if p.polls >= actorPolls {
			out = append(out, p.event)
			continue
		}
		keep = append(keep, p)
	}
	s.pending = keep
	return out, nil
}

// recentTagsQuery lists the newest tags. TAG_COMMIT_DATE doesn't order strictly by the
// dates recentTags uses, so it fetches more than a backlog needs and the poller sorts.
const recentTagsQuery = `query($owner: String!, $name: String!) {
  repository(owner: $owner, name: $name) {
    refs(refPrefix: "refs/tags/", first: 50, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) {
      nodes {
        name
        target {
          oid
          ... on Tag { tagger { date user { login } } }
          ... on Commit { committedDate author { user { login } } }
        }
      }
    }
  }
}`

type gqlRecentTags struct {
	Repository gqlRepository `json:"repository"`
}

type gqlRepository struct {
	Refs gqlRefs `json:"refs"`
}

type gqlRefs struct {
	Nodes []gqlRef `json:"nodes"`
}

type gqlRef struct {
	Name   string    `json:"name"`
	Target gqlTarget `json:"target"`
}

// gqlTarget is an annotated tag, which has a tagger, or a commit.
type gqlTarget struct {
	OID           string        `json:"oid"`
	Tagger        *gqlSignature `json:"tagger"`
	CommittedDate time.Time     `json:"committedDate"`
	Author        *gqlSignature `json:"author"`
}

type gqlSignature struct {
	Date time.Time `json:"date"`
	User *apiUser  `json:"user"`
}

// recentTags returns created events for the newest tags. Refs record no creation time, so
// an annotated tag uses its tagger date and a lightweight tag its commit date.
func (s *Refs) recentTags(ctx context.Context) ([]event.Event, error) {
	var resp gqlRecentTags
	vars := map[string]any{"owner": s.repo.Owner, "name": s.repo.Name}
	if err := s.gql.DoWithContext(ctx, recentTagsQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("list recent tags: %w", err)
	}

	out := make([]event.Event, 0, len(resp.Repository.Refs.Nodes))
	for _, r := range resp.Repository.Refs.Nodes {
		t := r.Target
		sig := t.Author
		at := t.CommittedDate
		if t.Tagger != nil {
			sig, at = t.Tagger, t.Tagger.Date
		}
		e := event.Event{
			ID:     fmt.Sprintf("%s:%s:%s:%s", event.Tag, r.Name, event.Created, t.OID),
			Time:   at,
			Repo:   s.repo.Owner + "/" + s.repo.Name,
			Kind:   event.Tag,
			Action: event.Created,
			Ref:    r.Name,
			Text:   r.Name,
			URL:    s.refURL(event.Created, r.Name),
		}
		if sig != nil && sig.User != nil {
			e.Actor = sig.User.Login
		}
		out = append(out, e)
	}
	return out, nil
}

// diff returns the selected changes between the previous refs and cur.
func (s *Refs) diff(cur map[string]string, now time.Time) []pendingRef {
	var out []pendingRef
	add := func(action, name, text, sha string) {
		if !s.sel.Has(s.kind, action) {
			return
		}
		key := refKey(s.kind, action, name, "")
		if action == event.Pushed {
			key = refKey(s.kind, action, name, sha)
		}
		out = append(out, pendingRef{
			event: event.Event{
				ID:     fmt.Sprintf("%s:%s:%s:%s", s.kind, name, action, sha),
				Time:   now,
				Repo:   s.repo.Owner + "/" + s.repo.Name,
				Kind:   s.kind,
				Action: action,
				Ref:    name,
				Text:   text,
				URL:    s.refURL(action, name),
			},
			key:   key,
			since: s.prevPoll,
		})
	}
	for name, sha := range cur {
		before, ok := s.prev[name]
		switch {
		case !ok:
			add(event.Created, name, name, sha)
		case before != sha:
			add(event.Pushed, name, fmt.Sprintf("%s %s..%s", name, short(before), short(sha)), sha)
		}
	}
	for name, sha := range s.prev {
		if _, ok := cur[name]; !ok {
			add(event.Deleted, name, name, sha)
		}
	}

	// Map iteration is random and the changes share one timestamp, so sort by ref name.
	sort.Slice(out, func(i, j int) bool { return out[i].event.ID < out[j].event.ID })
	return out
}

// refKinds maps an Events API ref_type to a kind. Other ref types, such as repository,
// map to "".
var refKinds = map[string]event.Kind{"tag": event.Tag, "branch": event.Branch}

// refURL links to the tag's or branch's page, except for deleted refs, which have none.
func (s *Refs) refURL(action, name string) string {
	if action == event.Deleted {
		return ""
	}
	page := "tree"
	if s.kind == event.Tag {
		page = "releases/tag"
	}
	return fmt.Sprintf("https://%s/%s/%s/%s/%s", s.repo.Host, s.repo.Owner, s.repo.Name, page, name)
}

func refKey(kind event.Kind, action, name, sha string) string {
	return fmt.Sprintf("%s:%s:%s:%s", kind, action, name, sha)
}

// eventRefKey returns the refKey an Events API entry describes, or "" for other entries.
func eventRefKey(r apiEvent) string {
	p := r.Payload
	switch r.Type {
	case "CreateEvent", "DeleteEvent":
		kind := refKinds[p.RefType]
		if kind == "" {
			return ""
		}
		action := event.Created
		if r.Type == "DeleteEvent" {
			action = event.Deleted
		}
		return refKey(kind, action, p.Ref, "")
	case "PushEvent":
		if name, ok := strings.CutPrefix(p.Ref, "refs/heads/"); ok {
			return refKey(event.Branch, event.Pushed, name, p.Head)
		}
		if name, ok := strings.CutPrefix(p.Ref, "refs/tags/"); ok {
			return refKey(event.Tag, event.Pushed, name, p.Head)
		}
	}
	return ""
}
