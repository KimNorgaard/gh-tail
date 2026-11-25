package source

import (
	"testing"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func TestReviewsPoll(t *testing.T) {
	stub := stubTransport{
		"/repos/o/r/pulls": `[
		  {"number":4,"title":"Add thing","user":{"login":"dt"},"updated_at":"2026-10-26T10:05:00Z"},
		  {"number":2,"title":"Old","user":{"login":"ann"},"updated_at":"2026-01-01T00:00:00Z"}
		]`,
		"/repos/o/r/pulls/4/reviews": `[
		  {"id":10,"state":"CHANGES_REQUESTED","body":"Please fix","user":{"login":"kim"},"submitted_at":"2026-10-26T10:01:00Z"},
		  {"id":11,"state":"PENDING","user":{"login":"bob"}},
		  {"id":12,"state":"APPROVED","user":{"login":"kim"},"submitted_at":"2026-10-26T10:05:00Z"}
		]`,
	}
	sel, _ := event.ParseSelection("pr:approved,pr:changes_requested,pr:reviewed")
	// A backlog of 1 reads only #4, so #2's reviews, which aren't stubbed, are never requested.
	s := NewReviews(stubClient(t, stub), testRepo, Options{Selection: sel, Backlog: 1})
	assertPolls(t, s, []string{
		"pr changes_requested #4 Add thing: Please fix @kim",
		"pr approved #4 Add thing @kim",
	}, nil)

	// #2 is updated, so its reviews are read. Its old review is below the floor and stays
	// hidden, and only the new one shows.
	stub["/repos/o/r/pulls"] = `[
	  {"number":2,"title":"Old","user":{"login":"ann"},"updated_at":"2026-10-26T11:00:00Z"},
	  {"number":4,"title":"Add thing","user":{"login":"dt"},"updated_at":"2026-10-26T10:05:00Z"}
	]`
	stub["/repos/o/r/pulls/2/reviews"] = `[
	  {"id":1,"state":"APPROVED","user":{"login":"cy"},"submitted_at":"2026-01-01T00:00:00Z"},
	  {"id":20,"state":"COMMENTED","body":"Thoughts","user":{"login":"cy"},"submitted_at":"2026-10-26T11:00:00Z"}
	]`
	assertPolls(t, s, []string{"pr reviewed #2 Old: Thoughts @cy"})
}

func TestReviewsNoBacklogDoesNotReplay(t *testing.T) {
	stub := stubTransport{"/repos/o/r/pulls": `[{"number":4,"title":"Add thing","updated_at":"2026-10-26T10:05:00Z"}]`}
	sel, _ := event.ParseSelection("pr:approved")
	s := NewReviews(stubClient(t, stub), testRepo, Options{Selection: sel, Backlog: 0})
	assertPolls(t, s, nil)

	// A comment bumps #4. Its review from before the start must not show.
	stub["/repos/o/r/pulls"] = `[{"number":4,"title":"Add thing","updated_at":"2026-10-26T12:00:00Z"}]`
	stub["/repos/o/r/pulls/4/reviews"] = `[{"id":1,"state":"APPROVED","user":{"login":"kim"},"submitted_at":"2026-10-26T09:00:00Z"}]`
	assertPolls(t, s, nil)
}

func TestNewSelectsCommentAndReviewSources(t *testing.T) {
	sel, _ := event.ParseSelection("pr:commented,pr:approved")
	sources := New(stubClient(t, stubTransport{}), nil, testRepo, Options{Selection: sel})
	if len(sources) != 2 {
		t.Fatalf("got %d sources, want Comments and Reviews only", len(sources))
	}
	if _, ok := sources[0].(*Comments); !ok {
		t.Errorf("sources[0] = %T, want *Comments", sources[0])
	}
	if _, ok := sources[1].(*Reviews); !ok {
		t.Errorf("sources[1] = %T, want *Reviews", sources[1])
	}
}
