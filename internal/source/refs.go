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
// polls. The first poll only records a baseline.
type Refs struct {
	client   *api.RESTClient
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

// NewRefs watches tags for event.Tag and branches for event.Branch.
func NewRefs(client *api.RESTClient, repo repository.Repository, sel event.Selection, kind event.Kind) *Refs {
	prefix := "heads"
	if kind == event.Tag {
		prefix = "tags"
	}
	return &Refs{client: client, repo: repo, sel: sel, kind: kind, prefix: prefix, now: time.Now}
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
	if s.prev != nil {
		s.pending = append(s.pending, s.diff(cur, now)...)
	}
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
				Text:   text,
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
