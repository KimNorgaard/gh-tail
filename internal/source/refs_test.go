package source

import (
	"context"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func TestRefsPoll(t *testing.T) {
	stub := stubTransport{
		"/repos/o/r/git/matching-refs/heads": `[
		  {"ref":"refs/heads/main","object":{"sha":"aaaaaaaaaa"}},
		  {"ref":"refs/heads/old","object":{"sha":"cccccccccc"}}
		]`,
		"/repos/o/r/events": `[]`,
	}
	sel, err := event.ParseSelection("branch:*")
	if err != nil {
		t.Fatal(err)
	}
	s := NewRefs(stubClient(t, stub), nil, testRepo, sel, event.Branch)
	s.now = func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) }
	assertPolls(t, s, nil)

	stub["/repos/o/r/git/matching-refs/heads"] = `[
	  {"ref":"refs/heads/main","object":{"sha":"bbbbbbbbbb"}},
	  {"ref":"refs/heads/feature/x","object":{"sha":"dddddddddd"}}
	]`
	// The CreateEvent for main is from before the previous poll, so it describes an
	// earlier creation and must not match anything.
	stub["/repos/o/r/events"] = `[
	  {"id":"3","type":"PushEvent","created_at":"2026-10-26T12:00:40Z","actor":{"login":"ann"},"payload":{"ref":"refs/heads/main","head":"bbbbbbbbbb"}},
	  {"id":"2","type":"CreateEvent","created_at":"2026-10-26T12:00:30Z","actor":{"login":"bob"},"payload":{"ref_type":"branch","ref":"feature/x"}},
	  {"id":"1","type":"CreateEvent","created_at":"2026-10-25T00:00:00Z","actor":{"login":"old"},"payload":{"ref_type":"branch","ref":"main"}}
	]`
	// The deletion has no Events API entry, so it waits actorPolls polls and prints
	// without an actor.
	assertPolls(t, s,
		[]string{"branch created feature/x @bob", "branch pushed main aaaaaaa..bbbbbbb @ann"},
		nil,
		[]string{"branch deleted old"},
		nil,
	)
}

func TestRefsTagBacklog(t *testing.T) {
	stub := stubTransport{
		"/repos/o/r/git/matching-refs/tags": `[{"ref":"refs/tags/v1.1.0","object":{"sha":"bbbbbbbbbb"}}]`,
		"/graphql": `{"data":{"repository":{"refs":{"nodes":[
		  {"name":"v1.1.0","target":{"oid":"bbbbbbbbbb","committedDate":"2026-10-26T10:00:00Z","author":{"user":{"login":"ann"}}}},
		  {"name":"v1.0.0","target":{"oid":"aaaaaaaaaa","tagger":{"date":"2026-10-20T10:00:00Z","user":{"login":"bob"}}}}
		]}}}}`,
	}
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "x", Transport: stub})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := event.ParseSelection("tag")
	if err != nil {
		t.Fatal(err)
	}
	s := NewRefs(stubClient(t, stub), gql, testRepo, sel, event.Tag)

	events, err := s.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Actor != "bob" || !events[1].Time.Equal(time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("backlog = %+v, want v1.1.0 by ann and v1.0.0 by bob at its tagger date", events)
	}
	// The baseline holds v1.1.0, so the next poll reports nothing new.
	assertPolls(t, s, nil)
}
