package event

import (
	"fmt"
	"slices"
	"strings"
)

// Action names, as the GitHub API spells them where it has a name.
const (
	Created    = "created"
	Deleted    = "deleted"
	Pushed     = "pushed"
	Opened     = "opened"
	Closed     = "closed"
	Merged     = "merged"
	Reopened   = "reopened"
	Assigned   = "assigned"
	Unassigned = "unassigned"
	Labeled    = "labeled"
	Unlabeled  = "unlabeled"
	Published  = "published"
)

// Actions lists the actions each kind's source emits, as observed from the API.
var Actions = map[Kind][]string{
	Tag:     {Created, Deleted},
	Branch:  {Created, Deleted, Pushed},
	PR:      {Opened, Closed, Merged, Reopened, Assigned, Unassigned, Labeled, Unlabeled},
	Issue:   {Opened, Closed, Reopened, Assigned, Unassigned, Labeled, Unlabeled},
	Release: {Published},
	Package: {Published},
}

// DefaultActions are the actions a bare kind selects.
var DefaultActions = map[Kind][]string{
	Tag:     {Created},
	Branch:  {Created, Pushed},
	PR:      {Opened, Closed, Merged},
	Issue:   {Opened, Closed},
	Release: {Published},
	Package: {Published},
}

// Selection holds the actions to show per kind. The action "*" selects every action,
// including ones missing from Actions.
type Selection map[Kind]map[string]bool

// ParseSelection parses comma-separated entries of kind, kind:action or kind:*.
func ParseSelection(s string) (Selection, error) {
	sel := Selection{}
	for entry := range strings.SplitSeq(s, ",") {
		name, action, hasAction := strings.Cut(strings.TrimSpace(entry), ":")
		kind := Kind(name)
		all, ok := Actions[kind]
		if !ok {
			return nil, fmt.Errorf("unknown kind %q, want one of %s", name, joinKinds())
		}

		actions := DefaultActions[kind]
		if hasAction {
			if action != "*" && !slices.Contains(all, action) {
				return nil, fmt.Errorf("unknown action %q for kind %s, want * or one of %s", action, kind, strings.Join(all, ","))
			}
			actions = []string{action}
		}

		if sel[kind] == nil {
			sel[kind] = map[string]bool{}
		}
		for _, a := range actions {
			sel[kind][a] = true
		}
	}
	return sel, nil
}

// Has reports whether the selection includes the action of the kind.
func (s Selection) Has(kind Kind, action string) bool {
	return s[kind]["*"] || s[kind][action]
}

func joinKinds() string {
	names := make([]string, len(Kinds))
	for i, k := range Kinds {
		names[i] = string(k)
	}
	return strings.Join(names, ",")
}
