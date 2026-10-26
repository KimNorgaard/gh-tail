package source

import (
	"slices"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// lifecycle holds the PR and issue actions that the issues list reports. RepoEvents
// reports the rest.
var lifecycle = map[event.Kind][]string{
	event.PR:    {event.Opened, event.Closed, event.Merged},
	event.Issue: {event.Opened, event.Closed},
}

func listed(kind event.Kind, action string) bool {
	return slices.Contains(lifecycle[kind], action)
}

// Options configures the sources.
type Options struct {
	Selection event.Selection
	Filter    event.Filter
	// Backlog is the number of past events shown at startup. It bounds the lookups a
	// first poll makes.
	Backlog int
}

// New returns the sources that cover the selection. gql lists recent tags for the
// startup backlog.
func New(client *api.RESTClient, gql *api.GraphQLClient, repo repository.Repository, opts Options) []Source {
	sel := opts.Selection
	var out []Source
	if sel[event.PR] != nil || sel[event.Issue] != nil {
		out = append(out, NewIssues(client, repo, opts))
	}
	if needsEvents(sel) {
		out = append(out, NewRepoEvents(client, repo, opts))
	}
	if sel[event.Release] != nil {
		out = append(out, NewReleases(client, repo))
	}
	for _, kind := range []event.Kind{event.Tag, event.Branch} {
		if sel[kind] != nil {
			out = append(out, NewRefs(client, gql, repo, sel, kind))
		}
	}
	if sel[event.Package] != nil {
		out = append(out, NewPackages(client, repo))
	}
	return out
}

// needsEvents reports whether the selection has a PR or issue action outside lifecycle.
// "*" counts, since it includes every other action.
func needsEvents(sel event.Selection) bool {
	for _, kind := range []event.Kind{event.PR, event.Issue} {
		for action := range sel[kind] {
			if !listed(kind, action) {
				return true
			}
		}
	}
	return false
}

// backfill completes candidates with their lookups and keeps those the filter matches.
// With n >= 0 it walks them newest first and stops at n matches, so a first poll makes
// lookups in proportion to the backlog, not to the list. Results are oldest first.
func backfill[T any](items []T, n int, at func(T) time.Time, complete func(T) (event.Event, error), filter event.Filter) ([]event.Event, error) {
	if n >= 0 {
		slices.SortStableFunc(items, func(a, b T) int { return at(b).Compare(at(a)) })
	}
	var out []event.Event
	for _, it := range items {
		if n >= 0 && len(out) == n {
			break
		}
		e, err := complete(it)
		if err != nil {
			return nil, err
		}
		if filter.Match(e) {
			out = append(out, e)
		}
	}
	if n >= 0 {
		slices.Reverse(out)
	}
	return out, nil
}
