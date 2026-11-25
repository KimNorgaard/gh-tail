package source

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// checkPRs is how many open PRs one poll reads, most recently updated first.
const checkPRs = 30

// Checks reads the check runs and commit statuses on the head commit of the default
// branch and of each open PR: what the PR page lists as checks. One GraphQL query
// covers them all. GraphQL has no conditional requests, so every poll costs one point
// of the GraphQL rate limit.
type Checks struct {
	gql       *api.GraphQLClient
	repo      repository.Repository
	selection event.Selection
	window    *window
}

func NewChecks(gql *api.GraphQLClient, repo repository.Repository, selection event.Selection) *Checks {
	return &Checks{gql: gql, repo: repo, selection: selection, window: newWindow()}
}

const checksQuery = `query($owner: String!, $name: String!, $prs: Int!) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name target { ... on Commit { ...checks } } }
    pullRequests(states: OPEN, first: $prs, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes { number title author { login } commits(last: 1) { nodes { commit { ...checks } } } }
    }
  }
}
fragment checks on Commit {
  oid
  statusCheckRollup { contexts(first: 100) { nodes {
    __typename
    ... on CheckRun { databaseId name status conclusion completedAt detailsUrl
      checkSuite { app { slug } workflowRun { workflow { name } } } }
    ... on StatusContext { id context state createdAt targetUrl description creator { login } }
  } } }
}`

type gqlChecks struct {
	Repository gqlChecksRepository `json:"repository"`
}

type gqlChecksRepository struct {
	DefaultBranchRef *gqlCheckBranch `json:"defaultBranchRef"`
	PullRequests     gqlCheckPRs     `json:"pullRequests"`
}

type gqlCheckBranch struct {
	Name   string         `json:"name"`
	Target gqlCheckCommit `json:"target"`
}

type gqlCheckPRs struct {
	Nodes []gqlCheckPR `json:"nodes"`
}

type gqlCheckPR struct {
	Number  int              `json:"number"`
	Title   string           `json:"title"`
	Author  apiUser          `json:"author"`
	Commits gqlCheckPRCommit `json:"commits"`
}

type gqlCheckPRCommit struct {
	Nodes []gqlCheckPRCommitNode `json:"nodes"`
}

type gqlCheckPRCommitNode struct {
	Commit gqlCheckCommit `json:"commit"`
}

type gqlCheckCommit struct {
	OID               string          `json:"oid"`
	StatusCheckRollup *gqlCheckRollup `json:"statusCheckRollup"`
}

type gqlCheckRollup struct {
	Contexts gqlCheckContexts `json:"contexts"`
}

type gqlCheckContexts struct {
	Nodes []gqlCheckContext `json:"nodes"`
}

// gqlCheckContext is a check run or a commit status, told apart by Typename.
type gqlCheckContext struct {
	Typename string `json:"__typename"`

	DatabaseID  int64         `json:"databaseId"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	Conclusion  string        `json:"conclusion"`
	CompletedAt time.Time     `json:"completedAt"`
	DetailsURL  string        `json:"detailsUrl"`
	CheckSuite  gqlCheckSuite `json:"checkSuite"`

	ID          string    `json:"id"`
	Context     string    `json:"context"`
	State       string    `json:"state"`
	CreatedAt   time.Time `json:"createdAt"`
	TargetURL   string    `json:"targetUrl"`
	Description string    `json:"description"`
	Creator     apiUser   `json:"creator"`
}

type gqlCheckSuite struct {
	App         gqlCheckApp          `json:"app"`
	WorkflowRun *gqlCheckWorkflowRun `json:"workflowRun"`
}

type gqlCheckApp struct {
	Slug string `json:"slug"`
}

type gqlCheckWorkflowRun struct {
	Workflow gqlCheckWorkflow `json:"workflow"`
}

type gqlCheckWorkflow struct {
	Name string `json:"name"`
}

func (s *Checks) Poll(ctx context.Context) ([]event.Event, error) {
	var resp gqlChecks
	vars := map[string]any{"owner": s.repo.Owner, "name": s.repo.Name, "prs": checkPRs}
	if err := s.gql.DoWithContext(ctx, checksQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("list checks: %w", err)
	}
	var out []event.Event
	add := func(base event.Event, subject string, commit gqlCheckCommit) {
		if commit.StatusCheckRollup == nil {
			return
		}
		base.Repo = s.repo.Owner + "/" + s.repo.Name
		base.Kind = event.Check
		base.SHA = commit.OID
		for _, c := range commit.StatusCheckRollup.Contexts.Nodes {
			e, ok := checkEvent(base, subject, c)
			if ok && s.selection.Has(event.Check, e.Action) && s.window.admit(e) {
				out = append(out, e)
			}
		}
	}
	if ref := resp.Repository.DefaultBranchRef; ref != nil {
		add(event.Event{Ref: ref.Name}, ref.Name, ref.Target)
	}
	for _, pr := range resp.Repository.PullRequests.Nodes {
		if len(pr.Commits.Nodes) == 0 {
			continue
		}
		subject := "#" + strconv.Itoa(pr.Number) + " " + pr.Title
		add(event.Event{Number: pr.Number, Author: pr.Author.Login}, subject, pr.Commits.Nodes[0].Commit)
	}
	s.window.advance()
	return out, nil
}

// checkEvent completes base from a check run that has finished or from a commit
// status. It reports false for a check run that is still queued or running. The
// action is the run's conclusion or the status's state, in lower case.
func checkEvent(base event.Event, subject string, c gqlCheckContext) (event.Event, bool) {
	e := base
	name := c.Context
	switch c.Typename {
	case "CheckRun":
		if c.Status != "COMPLETED" || c.Conclusion == "" {
			return e, false
		}
		e.ID = "check_run:" + strconv.FormatInt(c.DatabaseID, 10)
		e.Time = c.CompletedAt
		e.Action = strings.ToLower(c.Conclusion)
		e.Actor = c.CheckSuite.App.Slug
		e.URL = c.DetailsURL
		name = c.Name
		if run := c.CheckSuite.WorkflowRun; run != nil && run.Workflow.Name != "" {
			name = run.Workflow.Name + " / " + name
		}
	case "StatusContext":
		// A status has a new ID each time its state changes.
		e.ID = "status:" + c.ID
		e.Time = c.CreatedAt
		e.Action = strings.ToLower(c.State)
		e.Actor = c.Creator.Login
		e.URL = c.TargetURL
		if c.Description != "" {
			name += " (" + c.Description + ")"
		}
	default:
		return e, false
	}
	e.Text = subject + ": " + name
	return e, true
}
