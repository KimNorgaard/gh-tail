package source

import (
	"testing"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func TestCommentsPoll(t *testing.T) {
	stub := stubTransport{
		"/repos/o/r/issues/comments": `[
		  {"id":2,"body":"Looks good,\n\nthanks!","html_url":"https://github.com/o/r/pull/4#issuecomment-2","issue_url":"https://api.github.com/repos/o/r/issues/4","user":{"login":"kim"},"created_at":"2026-10-26T10:02:00Z"},
		  {"id":1,"body":"Same here","html_url":"https://github.com/o/r/issues/3#issuecomment-1","issue_url":"https://api.github.com/repos/o/r/issues/3","user":{"login":"ann"},"created_at":"2026-10-26T10:01:00Z"}
		]`,
		"/repos/o/r/pulls/comments": `[
		  {"id":7,"body":"Nit","path":"main.go","line":12,"html_url":"https://github.com/o/r/pull/4#discussion_r7","pull_request_url":"https://api.github.com/repos/o/r/pulls/4","user":{"login":"kim"},"created_at":"2026-10-26T10:03:00Z"}
		]`,
	}
	sel, _ := event.ParseSelection("pr:commented,issue:commented")
	s := NewComments(stubClient(t, stub), testRepo, sel)
	assertPolls(t, s, []string{
		"pr commented #4: Looks good, thanks! @kim",
		"issue commented #3: Same here @ann",
		"pr commented #4 main.go:12: Nit @kim",
	}, nil)

	// A new comment shows. An old one resurfacing after an edit stays hidden.
	stub["/repos/o/r/issues/comments"] = `[
	  {"id":9,"body":"Ping","html_url":"https://github.com/o/r/pull/4#issuecomment-9","issue_url":"https://api.github.com/repos/o/r/issues/4","user":{"login":"dt"},"created_at":"2026-10-26T11:00:00Z"},
	  {"id":0,"body":"Ancient, edited","html_url":"https://github.com/o/r/issues/1#issuecomment-0","issue_url":"https://api.github.com/repos/o/r/issues/1","created_at":"2026-01-01T00:00:00Z"}
	]`
	assertPolls(t, s, []string{"pr commented #4: Ping @dt"})
}

func TestCommentsIssueOnlySkipsReviewComments(t *testing.T) {
	// The review comments endpoint isn't stubbed, so requesting it fails the poll.
	stub := stubTransport{"/repos/o/r/issues/comments": `[
	  {"id":2,"body":"PR talk","html_url":"https://github.com/o/r/pull/4#issuecomment-2","issue_url":"https://api.github.com/repos/o/r/issues/4","created_at":"2026-10-26T10:02:00Z"},
	  {"id":1,"body":"Issue talk","html_url":"https://github.com/o/r/issues/3#issuecomment-1","issue_url":"https://api.github.com/repos/o/r/issues/3","created_at":"2026-10-26T10:01:00Z"}
	]`}
	sel, _ := event.ParseSelection("issue:commented")
	assertPolls(t, NewComments(stubClient(t, stub), testRepo, sel), []string{"issue commented #3: Issue talk"})
}

func TestExcerpt(t *testing.T) {
	long := ""
	for range 300 {
		long += "x"
	}
	if got := []rune(excerpt(long)); len(got) != commentLength || got[len(got)-1] != '…' {
		t.Errorf("excerpt of 300 chars = %d runes, want %d ending in …", len(got), commentLength)
	}
	if got := excerpt("<!-- bot\nmarker --> ### Summary"); got != "### Summary" {
		t.Errorf("excerpt = %q, want the HTML comment dropped", got)
	}
	if got := excerpt("  a\r\n\tb  "); got != "a b" {
		t.Errorf("excerpt = %q, want %q", got, "a b")
	}
}
