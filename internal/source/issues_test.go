package source

import (
	"testing"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func TestIssuesPoll(t *testing.T) {
	// Unstubbed lookups return 404 and fail the poll, so #2 closed must not be looked up.
	stub := stubTransport{
		"/repos/o/r/issues": `[
		  {"number":3,"title":"Fix","user":{"login":"ann"},"created_at":"2026-10-26T10:00:00Z","closed_at":"2026-10-26T11:00:00Z","pull_request":{"merged_at":"2026-10-26T11:00:00Z"}},
		  {"number":2,"title":"Drop","created_at":"2026-10-26T09:00:00Z","closed_at":"2026-10-26T09:30:00Z","pull_request":{"merged_at":null}},
		  {"number":1,"title":"Bug","user":{"login":"bob"},"created_at":"2026-10-26T08:00:00Z","closed_at":null}
		]`,
		"/repos/o/r/pulls/3":  `{"merged_by":{"login":"cy"}}`,
		"/repos/o/r/issues/1": `{"closed_by":{"login":"dee"}}`,
	}
	sel, err := event.ParseSelection("pr,issue")
	if err != nil {
		t.Fatal(err)
	}
	// A backlog of 2 keeps the first poll to #3's events and skips the #2 lookup.
	s := NewIssues(stubClient(t, stub), testRepo, Options{Selection: sel, Backlog: 2})
	assertPolls(t, s, []string{"pr opened #3 Fix @ann", "pr merged #3 Fix @cy"}, nil)

	// #1 closes. #9 is old and resurfaces after a comment, so its past events stay hidden.
	stub["/repos/o/r/issues"] = `[
	  {"number":1,"title":"Bug","created_at":"2026-10-26T08:00:00Z","closed_at":"2026-10-26T11:05:00Z"},
	  {"number":9,"title":"Old","created_at":"2026-01-01T00:00:00Z","closed_at":"2026-01-02T00:00:00Z"}
	]`
	assertPolls(t, s, []string{"issue closed #1 Bug @dee"})
}

func TestIssuesBackfillFilter(t *testing.T) {
	// The newest event is filtered out, so the first poll walks on to fill the backlog.
	// #1 is never looked up, which proves the walk stops once the backlog is full.
	stub := stubTransport{
		"/repos/o/r/issues": `[
		  {"number":3,"title":"Bump","user":{"login":"dependabot[bot]"},"created_at":"2026-10-26T10:00:00Z","closed_at":null,"pull_request":{"merged_at":null}},
		  {"number":2,"title":"Fix","user":{"login":"ann"},"labels":[{"name":"bug"}],"created_at":"2026-10-26T09:00:00Z","closed_at":"2026-10-26T09:30:00Z"},
		  {"number":1,"title":"Old","user":{"login":"bob"},"created_at":"2026-10-26T08:00:00Z","closed_at":"2026-10-26T08:30:00Z"}
		]`,
		"/repos/o/r/issues/2": `{"closed_by":{"login":"cy"}}`,
	}
	sel, err := event.ParseSelection("pr,issue")
	if err != nil {
		t.Fatal(err)
	}
	filter, err := event.ParseFilter("-author:*[bot]")
	if err != nil {
		t.Fatal(err)
	}
	s := NewIssues(stubClient(t, stub), testRepo, Options{Selection: sel, Filter: filter, Backlog: 2})
	assertPolls(t, s, []string{"issue opened #2 Fix @ann", "issue closed #2 Fix @cy"})
}
