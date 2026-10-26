// Package event defines the normalized event that sources emit and printers render.
package event

import "time"

// Kind is the category of an event. Its values double as the --kinds filter values.
type Kind string

const (
	Tag     Kind = "tag"
	Branch  Kind = "branch"
	PR      Kind = "pr"
	Issue   Kind = "issue"
	Release Kind = "release"
	Package Kind = "package"
)

// Kinds lists every supported kind.
var Kinds = []Kind{Tag, Branch, PR, Issue, Release, Package}

type Event struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Repo   string    `json:"repo"`
	Kind   Kind      `json:"kind"`
	Action string    `json:"action"`
	Number int       `json:"number,omitempty"`
	Actor  string    `json:"actor,omitempty"`
	Author string    `json:"author,omitempty"`
	Labels []string  `json:"labels,omitempty"`
	Text   string    `json:"text"`
	URL    string    `json:"url,omitempty"`
}
