package source

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// Deployments reads deployment creation and status history directly, avoiding the
// latency of the repository Events API. Each poll examines the latest 100
// deployments and up to 100 statuses per deployment.
type Deployments struct {
	client    *api.RESTClient
	repo      repository.Repository
	selection event.Selection
	window    *window
}

func NewDeployments(client *api.RESTClient, repo repository.Repository, selection event.Selection) *Deployments {
	return &Deployments{client: client, repo: repo, selection: selection, window: newWindow()}
}

type apiDeployment struct {
	ID          int64     `json:"id"`
	Ref         string    `json:"ref"`
	SHA         string    `json:"sha"`
	Environment string    `json:"environment"`
	Description string    `json:"description"`
	Creator     apiUser   `json:"creator"`
	CreatedAt   time.Time `json:"created_at"`
}

type apiDeploymentStatus struct {
	ID             int64     `json:"id"`
	State          string    `json:"state"`
	Description    string    `json:"description"`
	Environment    string    `json:"environment"`
	LogURL         string    `json:"log_url"`
	TargetURL      string    `json:"target_url"`
	EnvironmentURL string    `json:"environment_url"`
	Creator        apiUser   `json:"creator"`
	CreatedAt      time.Time `json:"created_at"`
}

func (s *Deployments) Poll(ctx context.Context) ([]event.Event, error) {
	var deployments []apiDeployment
	path := fmt.Sprintf("repos/%s/%s/deployments?per_page=100", s.repo.Owner, s.repo.Name)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &deployments); err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	// Gather every response before admitting events: a failed status request must
	// not consume creation events or earlier successful lookups on retry.
	batches, err := completeAll(deployments, func(d apiDeployment) ([]event.Event, error) {
		base := event.Event{ID: "deployment:" + strconv.FormatInt(d.ID, 10), Time: d.CreatedAt, Repo: s.repo.Owner + "/" + s.repo.Name, Kind: event.Deployment, Action: event.Created, Actor: d.Creator.Login, Author: d.Creator.Login, Ref: d.Ref, DeploymentID: d.ID, Environment: d.Environment, SHA: d.SHA}
		base.URL = fmt.Sprintf("https://%s/%s/%s/deployments", s.repo.Host, s.repo.Owner, s.repo.Name)
		base.Text = deploymentText(d.Environment, d.Ref, d.Description)
		var out []event.Event
		if s.selection.Has(event.Deployment, event.Created) {
			out = append(out, base)
		}
		needsStatuses := false
		for action := range s.selection[event.Deployment] {
			if action != event.Created {
				needsStatuses = true
			}
		}
		if !needsStatuses {
			return out, nil
		}
		var statuses []apiDeploymentStatus
		path := fmt.Sprintf("repos/%s/%s/deployments/%d/statuses?per_page=100", s.repo.Owner, s.repo.Name, d.ID)
		if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &statuses); err != nil {
			return nil, fmt.Errorf("list deployment %d statuses: %w", d.ID, err)
		}
		// The list is newest first. Walk it oldest first and skip a status that repeats
		// the previous one's state and description: some CI systems post each state
		// twice.
		slices.Reverse(statuses)
		var prev *apiDeploymentStatus
		for i := range statuses {
			st := statuses[i]
			repeat := prev != nil && prev.State == st.State && prev.Description == st.Description
			prev = &statuses[i]
			if repeat || !s.selection.Has(event.Deployment, st.State) {
				continue
			}
			e := base
			e.ID = "deployment_status:" + strconv.FormatInt(st.ID, 10)
			e.Time = st.CreatedAt
			e.Action = st.State
			e.Actor = st.Creator.Login
			if st.Environment != "" {
				e.Environment = st.Environment
			}
			e.LogURL = st.LogURL
			if e.LogURL == "" {
				e.LogURL = st.TargetURL
			}
			e.EnvironmentURL = st.EnvironmentURL
			if e.LogURL != "" {
				e.URL = e.LogURL
			} else if e.EnvironmentURL != "" {
				e.URL = e.EnvironmentURL
			}
			e.Text = deploymentText(e.Environment, d.Ref, st.Description)
			out = append(out, e)
		}
		return out, nil
	})
	if err != nil {
		return nil, err
	}
	var out []event.Event
	for _, batch := range batches {
		for _, e := range batch {
			if s.window.admit(e) {
				out = append(out, e)
			}
		}
	}
	s.window.advance()
	return out, nil
}

// shaRef matches a ref that is a full commit SHA.
var shaRef = regexp.MustCompile(`^[0-9a-f]{40}$`)

// deploymentText is "environment ref: description", with a SHA ref shortened. The
// deployment ID is in the JSON output.
func deploymentText(environment, ref, description string) string {
	if shaRef.MatchString(ref) {
		ref = short(ref)
	}
	text := environment + " " + ref
	if description != "" {
		text += ": " + description
	}
	return text
}
