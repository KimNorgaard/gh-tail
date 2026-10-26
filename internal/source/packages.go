package source

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// packageTypes are the values the packages API accepts for package_type.
var packageTypes = []string{"container", "npm", "maven", "rubygems", "nuget", "docker"}

// Packages polls the package versions of every package linked to the repository.
// Packages hang off the owner, so each poll lists the owner's packages per type and
// keeps the ones whose repository matches.
//
// ponytail: 6 + one request per package on every poll, cache the package list if that hurts.
type Packages struct {
	client *api.RESTClient
	repo   repository.Repository
	scope  string
	seen   map[string]bool
}

func NewPackages(client *api.RESTClient, repo repository.Repository) *Packages {
	return &Packages{client: client, repo: repo, seen: map[string]bool{}}
}

type apiPackage struct {
	Name       string         `json:"name"`
	Repository *apiRepository `json:"repository"`
}

type apiRepository struct {
	FullName string   `json:"full_name"`
	Owner    apiOwner `json:"owner"`
}

type apiOwner struct {
	Type string `json:"type"`
}

type apiPackageVersion struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	CreatedAt time.Time          `json:"created_at"`
	HTMLURL   string             `json:"html_url"`
	Metadata  apiPackageMetadata `json:"metadata"`
}

type apiPackageMetadata struct {
	Container apiContainerMetadata `json:"container"`
}

type apiContainerMetadata struct {
	Tags []string `json:"tags"`
}

func (s *Packages) Poll(ctx context.Context) ([]event.Event, error) {
	if s.scope == "" {
		scope, err := s.ownerScope(ctx)
		if err != nil {
			return nil, err
		}
		s.scope = scope
	}

	fullName := s.repo.Owner + "/" + s.repo.Name
	seen := map[string]bool{}
	var out []event.Event
	for _, typ := range packageTypes {
		var pkgs []apiPackage
		path := fmt.Sprintf("%s/%s/packages?package_type=%s&per_page=100", s.scope, s.repo.Owner, typ)
		if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &pkgs); err != nil {
			return nil, fmt.Errorf("list %s packages (needs read:packages, see gh auth refresh -s read:packages): %w", typ, err)
		}

		for _, pkg := range pkgs {
			if pkg.Repository == nil || !strings.EqualFold(pkg.Repository.FullName, fullName) {
				continue
			}
			var versions []apiPackageVersion
			path := fmt.Sprintf("%s/%s/packages/%s/%s/versions?per_page=30", s.scope, s.repo.Owner, typ, url.PathEscape(pkg.Name))
			if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &versions); err != nil {
				return nil, fmt.Errorf("list versions of %s: %w", pkg.Name, err)
			}

			for _, v := range versions {
				id := "package:" + strconv.FormatInt(v.ID, 10)
				seen[id] = true
				if s.seen[id] {
					continue
				}
				text := pkg.Name + "@" + v.Name
				if typ == "container" {
					tags := userTags(v.Metadata.Container.Tags)
					if len(tags) == 0 {
						continue
					}
					text = pkg.Name + ":" + strings.Join(tags, ",")
				}
				out = append(out, event.Event{
					ID:     id,
					Time:   v.CreatedAt,
					Repo:   fullName,
					Kind:   event.Package,
					Action: event.Published,
					Text:   text,
					URL:    v.HTMLURL,
				})
			}
		}
	}
	s.seen = seen
	return out, nil
}

// ownerScope returns the API path prefix for the repository owner's packages.
func (s *Packages) ownerScope(ctx context.Context) (string, error) {
	var repo apiRepository
	path := fmt.Sprintf("repos/%s/%s", s.repo.Owner, s.repo.Name)
	if err := s.client.DoWithContext(ctx, http.MethodGet, path, nil, &repo); err != nil {
		return "", fmt.Errorf("get repository: %w", err)
	}
	if repo.Owner.Type == "Organization" {
		return "orgs", nil
	}
	return "users", nil
}

// userTags drops sha256-<digest> tags, which signing and attestation tools attach as
// OCI referrers. Versions left with no tags are such artifacts or per-platform
// manifests of a multi-arch push.
func userTags(tags []string) []string {
	return slices.DeleteFunc(slices.Clone(tags), func(t string) bool {
		return strings.HasPrefix(t, "sha256-")
	})
}
