package source

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

type stubTransport map[string]string

func (s stubTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := s[r.URL.Path]
	status := http.StatusOK
	if !ok {
		status, body = http.StatusNotFound, `{"message":"Not Found"}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

const eventsJSON = `[
  {"id":"7","type":"PullRequestEvent","created_at":"2026-10-26T10:07:00Z","payload":{"action":"assigned","number":3,"assignee":{"login":"kn"}}},
  {"id":"6","type":"PullRequestEvent","created_at":"2026-10-26T10:06:00Z","actor":{"login":"ann"},"payload":{"action":"unlabeled","number":2,"label":{"name":"wip"}}},
  {"id":"5","type":"PullRequestEvent","created_at":"2026-10-26T10:05:00Z","payload":{"action":"merged","number":1}},
  {"id":"4","type":"IssuesEvent","created_at":"2026-10-26T10:04:00Z","payload":{"action":"labeled","issue":{"number":9,"title":"Broken"},"label":{"name":"bug"}}},
  {"id":"3","type":"IssuesEvent","created_at":"2026-10-26T10:03:00Z","payload":{"action":"opened","issue":{"number":9,"title":"Broken"}}},
  {"id":"2","type":"CreateEvent","created_at":"2026-10-26T10:02:00Z","payload":{"ref_type":"tag","ref":"v1.0.0"}}
]`

func TestRepoEventsPoll(t *testing.T) {
	// Unstubbed paths return 404, so a PR lookup for an unselected event (#3) or a
	// lifecycle event (#1) fails the poll.
	client := stubClient(t, stubTransport{
		"/repos/o/r/events":  eventsJSON,
		"/repos/o/r/pulls/2": `{"title":"Drop thing"}`,
	})
	sel, err := event.ParseSelection("pr:*,issue:*,tag")
	if err != nil {
		t.Fatal(err)
	}
	sel[event.PR] = map[string]bool{event.Unlabeled: true, event.Merged: true}
	s := NewRepoEvents(client, testRepo, Options{Selection: sel, Backlog: 10})

	assertPolls(t, s,
		[]string{"issue labeled #9 Broken (bug)", "pr unlabeled #2 Drop thing (wip) @ann"},
		nil,
	)
}

var testRepo = repository.Repository{Host: "github.com", Owner: "o", Name: "r"}

func stubClient(t *testing.T, tr http.RoundTripper) *api.RESTClient {
	t.Helper()
	client, err := api.NewRESTClient(api.ClientOptions{Host: "github.com", AuthToken: "x", Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// assertPolls polls s once per want and compares "kind action text [@actor]" lines.
func assertPolls(t *testing.T, s Source, want ...[]string) {
	t.Helper()
	for i, w := range want {
		events, err := s.Poll(context.Background())
		if err != nil {
			t.Fatalf("poll %d: %v", i+1, err)
		}
		var got []string
		for _, e := range events {
			line := string(e.Kind) + " " + e.Action + " " + e.Text
			if e.Actor != "" {
				line += " @" + e.Actor
			}
			got = append(got, line)
		}
		if strings.Join(got, "\n") != strings.Join(w, "\n") {
			t.Errorf("poll %d: got\n%s\nwant\n%s", i+1, strings.Join(got, "\n"), strings.Join(w, "\n"))
		}
	}
}
