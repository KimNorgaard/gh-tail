package source

import (
	"context"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

func stubGraphQL(t *testing.T, stub stubTransport) *api.GraphQLClient {
	t.Helper()
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: "github.com", AuthToken: "x", Transport: stub})
	if err != nil {
		t.Fatal(err)
	}
	return gql
}

// checksJSON wraps the default branch's and PR #7's check contexts in a response.
func checksJSON(mainContexts, prContexts string) string {
	return `{"data":{"repository":{
	  "defaultBranchRef":{"name":"main","target":{"oid":"aaaaaaa","statusCheckRollup":{"contexts":{"nodes":[` + mainContexts + `]}}}},
	  "pullRequests":{"nodes":[
	    {"number":7,"title":"Fix","author":{"login":"ann"},"commits":{"nodes":[{"commit":{"oid":"bbbbbbb","statusCheckRollup":{"contexts":{"nodes":[` + prContexts + `]}}}}]}},
	    {"number":8,"title":"No checks","author":{"login":"bob"},"commits":{"nodes":[{"commit":{"oid":"ccccccc","statusCheckRollup":null}}]}}
	  ]}}}}`
}

const (
	runLint     = `{"__typename":"CheckRun","databaseId":1,"name":"lint","status":"COMPLETED","conclusion":"SUCCESS","completedAt":"2026-10-26T10:01:00Z","detailsUrl":"https://ci.example/1","checkSuite":{"app":{"slug":"github-actions"},"workflowRun":{"workflow":{"name":"CI"}}}}`
	runTestBusy = `{"__typename":"CheckRun","databaseId":2,"name":"test","status":"IN_PROGRESS","conclusion":null,"completedAt":null,"checkSuite":{"app":{"slug":"github-actions"},"workflowRun":{"workflow":{"name":"CI"}}}}`
	runTestFail = `{"__typename":"CheckRun","databaseId":2,"name":"test","status":"COMPLETED","conclusion":"FAILURE","completedAt":"2026-10-26T10:03:00Z","detailsUrl":"https://ci.example/2","checkSuite":{"app":{"slug":"github-actions"},"workflowRun":{"workflow":{"name":"CI"}}}}`
	runSkipped  = `{"__typename":"CheckRun","databaseId":3,"name":"deploy","status":"COMPLETED","conclusion":"SKIPPED","completedAt":"2026-10-26T10:01:30Z","checkSuite":{"app":{"slug":"github-actions"},"workflowRun":null}}`
	statusWait  = `{"__typename":"StatusContext","id":"S1","context":"ci/build","state":"PENDING","createdAt":"2026-10-26T10:00:30Z","targetUrl":"https://ext.example/9","creator":{"login":"bot"}}`
	statusDone  = `{"__typename":"StatusContext","id":"S2","context":"ci/build","state":"SUCCESS","createdAt":"2026-10-26T10:04:00Z","targetUrl":"https://ext.example/9","description":"Built","creator":{"login":"bot"}}`
)

func TestChecksReportsFinishedRunsAndStatuses(t *testing.T) {
	stub := stubTransport{"/graphql": checksJSON(runLint, runTestBusy+","+runSkipped+","+statusWait)}
	sel, _ := event.ParseSelection("check")
	s := NewChecks(stubGraphQL(t, stub), testRepo, sel)
	// A running check, a skipped one and a pending status are not in the defaults.
	events, err := s.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want the default branch's finished run: %+v", len(events), events)
	}
	e := events[0]
	if e.ID != "check_run:1" || e.Kind != event.Check || e.Action != event.Success || e.Ref != "main" || e.Number != 0 || e.SHA != "aaaaaaa" || e.Actor != "github-actions" || e.URL != "https://ci.example/1" || e.Text != "main: CI / lint" {
		t.Fatalf("wrong check run metadata: %+v", e)
	}
	assertPolls(t, s, nil)

	// The run finishes and the status changes state, which gives it a new ID.
	stub["/graphql"] = checksJSON(runLint, runTestFail+","+runSkipped+","+statusDone)
	events, err = s.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want the failed run and the new status: %+v", len(events), events)
	}
	e = events[0]
	if e.ID != "check_run:2" || e.Action != event.Failure || e.Number != 7 || e.Author != "ann" || e.SHA != "bbbbbbb" || e.Text != "#7 Fix: CI / test" {
		t.Fatalf("wrong PR check run metadata: %+v", e)
	}
	e = events[1]
	if e.ID != "status:S2" || e.Action != event.Success || e.Actor != "bot" || e.URL != "https://ext.example/9" || e.Text != "#7 Fix: ci/build (Built)" {
		t.Fatalf("wrong status metadata: %+v", e)
	}
	assertPolls(t, s, nil)
}

func TestChecksSelectionAndWildcard(t *testing.T) {
	stub := stubTransport{"/graphql": checksJSON("", runTestFail+","+runSkipped+","+statusWait)}
	sel, _ := event.ParseSelection("check:failure")
	assertPolls(t, NewChecks(stubGraphQL(t, stub), testRepo, sel), []string{"check failure #7 Fix: CI / test @github-actions"})
	sel, _ = event.ParseSelection("check:*")
	assertPolls(t, NewChecks(stubGraphQL(t, stub), testRepo, sel), []string{
		"check failure #7 Fix: CI / test @github-actions",
		"check skipped #7 Fix: deploy @github-actions",
		"check pending #7 Fix: ci/build @bot",
	})
}

func TestChecksQueryFailure(t *testing.T) {
	sel, _ := event.ParseSelection("check")
	if _, err := NewChecks(stubGraphQL(t, stubTransport{}), testRepo, sel).Poll(context.Background()); err == nil {
		t.Fatal("a failed query must be an error")
	}
}

func TestNewSelectsChecks(t *testing.T) {
	sel, _ := event.ParseSelection("check")
	sources := New(stubClient(t, stubTransport{}), stubGraphQL(t, stubTransport{}), testRepo, Options{Selection: sel})
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
	if _, ok := sources[0].(*Checks); !ok {
		t.Fatalf("got %T, want *Checks", sources[0])
	}
}
