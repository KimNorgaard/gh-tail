package source

import (
	"testing"
	"time"

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
	s := NewRefs(stubClient(t, stub), testRepo, sel, event.Branch)
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
