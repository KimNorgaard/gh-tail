package source

import (
	"context"
	"testing"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

const deploymentJSON = `[{"id":42,"ref":"main","sha":"abc123","environment":"production","description":"Deploy app","creator":{"login":"ann"},"created_at":"2026-10-26T10:00:00Z"}]`

func TestDeploymentsHistory(t *testing.T) {
	stub := stubTransport{
		"/repos/o/r/deployments": deploymentJSON,
		"/repos/o/r/deployments/42/statuses": `[
   {"id":3,"state":"success","description":"Ready","log_url":"https://logs.example/42","environment_url":"https://app.example","creator":{"login":"bot"},"created_at":"2026-10-26T10:03:00Z"},
   {"id":2,"state":"in_progress","created_at":"2026-10-26T10:02:00Z"},
   {"id":1,"state":"queued","created_at":"2026-10-26T10:01:00Z"}]`,
	}
	sel, _ := event.ParseSelection("deployment")
	s := NewDeployments(stubClient(t, stub), testRepo, sel)
	events, err := s.Poll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("got %d events, want creation and all 3 statuses", len(events))
	}
	e := events[3]
	if e.ID != "deployment_status:3" || e.Action != event.Success || e.Actor != "bot" || e.Author != "ann" || e.Ref != "main" || e.SHA != "abc123" || e.Environment != "production" || e.DeploymentID != 42 || e.URL != "https://logs.example/42" || e.EnvironmentURL != "https://app.example" {
		t.Fatalf("wrong status metadata: %+v", e)
	}
	assertPolls(t, s, nil)
	// A second success is a distinct status, even though the state is unchanged.
	stub["/repos/o/r/deployments/42/statuses"] = `[
   {"id":4,"state":"success","target_url":"https://legacy.example","created_at":"2026-10-26T10:04:00Z"},
   {"id":3,"state":"success","description":"Ready","created_at":"2026-10-26T10:03:00Z"}]`
	events, err = s.Poll(context.Background())
	if err != nil || len(events) != 1 || events[0].ID != "deployment_status:4" || events[0].URL != "https://legacy.example" {
		t.Fatalf("new status: %+v, %v", events, err)
	}
}

func TestDeploymentsCreatedOnly(t *testing.T) {
	sel, _ := event.ParseSelection("deployment:created")
	// No status endpoint is stubbed: creation-only must not request it.
	s := NewDeployments(stubClient(t, stubTransport{"/repos/o/r/deployments": deploymentJSON}), testRepo, sel)
	assertPolls(t, s, []string{"deployment created production main: Deploy app @ann"}, nil)
}

func TestDeploymentsRetryDoesNotConsumeEvents(t *testing.T) {
	stub := stubTransport{"/repos/o/r/deployments": deploymentJSON}
	sel, _ := event.ParseSelection("deployment")
	s := NewDeployments(stubClient(t, stub), testRepo, sel)
	if _, err := s.Poll(context.Background()); err == nil {
		t.Fatal("missing status endpoint must fail")
	}
	stub["/repos/o/r/deployments/42/statuses"] = `[]`
	assertPolls(t, s, []string{"deployment created production main: Deploy app @ann"}, nil)
}

func TestDeploymentsSelectionAndWildcard(t *testing.T) {
	stub := stubTransport{"/repos/o/r/deployments": deploymentJSON, "/repos/o/r/deployments/42/statuses": `[{"id":5,"state":"future_state","created_at":"2026-10-26T10:05:00Z"},{"id":4,"state":"failure","created_at":"2026-10-26T10:04:00Z"}]`}
	sel, _ := event.ParseSelection("deployment:failure")
	assertPolls(t, NewDeployments(stubClient(t, stub), testRepo, sel), []string{"deployment failure production main"})
	sel, _ = event.ParseSelection("deployment:*")
	assertPolls(t, NewDeployments(stubClient(t, stub), testRepo, sel), []string{"deployment created production main: Deploy app @ann", "deployment failure production main", "deployment future_state production main"})
}

func TestNewSelectsDeployments(t *testing.T) {
	sel, _ := event.ParseSelection("deployment:success")
	sources := New(stubClient(t, stubTransport{}), nil, testRepo, Options{Selection: sel})
	if len(sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(sources))
	}
	if _, ok := sources[0].(*Deployments); !ok {
		t.Fatalf("got %T, want *Deployments", sources[0])
	}
}

func TestDeploymentsSkipsRepeatedStatuses(t *testing.T) {
	// Newest first, as the API returns them. The CI posts each state twice.
	stub := stubTransport{
		"/repos/o/r/deployments": `[{"id":7,"ref":"8d663ac230df2e98a038e5201f4f17fbcfeac19a","environment":"penny","created_at":"2026-11-24T17:20:30Z"}]`,
		"/repos/o/r/deployments/7/statuses": `[
   {"id":6,"state":"success","description":"Pipeline was successful","created_at":"2026-11-24T17:21:56Z"},
   {"id":5,"state":"pending","description":"Pipeline is running","created_at":"2026-11-24T17:21:17Z"},
   {"id":4,"state":"pending","description":"Pipeline is running","created_at":"2026-11-24T17:21:17Z"},
   {"id":3,"state":"pending","description":"Pipeline is pending","created_at":"2026-11-24T17:20:38Z"},
   {"id":2,"state":"pending","description":"Pipeline is pending","created_at":"2026-11-24T17:20:37Z"}]`,
	}
	sel, _ := event.ParseSelection("deployment:pending,deployment:success")
	assertPolls(t, NewDeployments(stubClient(t, stub), testRepo, sel), []string{
		"deployment pending penny 8d663ac: Pipeline is pending",
		"deployment pending penny 8d663ac: Pipeline is running",
		"deployment success penny 8d663ac: Pipeline was successful",
	}, nil)
}
