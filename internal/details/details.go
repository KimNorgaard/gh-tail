// Package details fetches the long form of an event as Markdown: a PR or issue body,
// release notes, or the commits a tag adds.
package details

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
	"golang.org/x/mod/semver"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
)

// maxCommits caps the commit list of a tag. The compare API returns at most 250.
const maxCommits = 100

type Fetcher struct {
	client *api.RESTClient
	repo   repository.Repository
}

func New(client *api.RESTClient, repo repository.Repository) *Fetcher {
	return &Fetcher{client: client, repo: repo}
}

// Markdown returns the details of e. Kinds without details return a short note.
func (f *Fetcher) Markdown(ctx context.Context, e event.Event) (string, error) {
	switch e.Kind {
	case event.PR:
		return f.pr(ctx, e.Number)
	case event.Issue:
		return f.issue(ctx, e.Number)
	case event.Release:
		return f.release(ctx, e.Ref)
	case event.Tag:
		if e.Action != event.Deleted {
			return f.tag(ctx, e.Ref)
		}
	}
	return fmt.Sprintf("# %s\n\nNo details for a %s %s event.\n", e.Text, e.Kind, e.Action), nil
}

type apiUser struct {
	Login string `json:"login"`
}

type apiLabel struct {
	Name string `json:"name"`
}

type apiPR struct {
	Title        string     `json:"title"`
	State        string     `json:"state"`
	Merged       bool       `json:"merged"`
	Draft        bool       `json:"draft"`
	User         apiUser    `json:"user"`
	Labels       []apiLabel `json:"labels"`
	Body         string     `json:"body"`
	Additions    int        `json:"additions"`
	Deletions    int        `json:"deletions"`
	ChangedFiles int        `json:"changed_files"`
	Base         apiBranch  `json:"base"`
	Head         apiBranch  `json:"head"`
}

type apiBranch struct {
	Ref string `json:"ref"`
}

func (f *Fetcher) pr(ctx context.Context, number int) (string, error) {
	var pr apiPR
	if err := f.get(ctx, fmt.Sprintf("pulls/%d", number), &pr); err != nil {
		return "", err
	}
	state := pr.State
	switch {
	case pr.Merged:
		state = "merged"
	case pr.Draft:
		state = "draft"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s #%d\n\n", pr.Title, number)
	fmt.Fprintf(&b, "**%s** · @%s · `%s` → `%s` · +%d −%d in %s\n\n",
		state, pr.User.Login, pr.Head.Ref, pr.Base.Ref, pr.Additions, pr.Deletions, plural(pr.ChangedFiles, "file"))
	writeLabels(&b, pr.Labels)
	writeBody(&b, pr.Body)
	return b.String(), nil
}

type apiIssue struct {
	Title       string     `json:"title"`
	State       string     `json:"state"`
	StateReason string     `json:"state_reason"`
	User        apiUser    `json:"user"`
	Labels      []apiLabel `json:"labels"`
	Comments    int        `json:"comments"`
	Body        string     `json:"body"`
}

func (f *Fetcher) issue(ctx context.Context, number int) (string, error) {
	var is apiIssue
	if err := f.get(ctx, fmt.Sprintf("issues/%d", number), &is); err != nil {
		return "", err
	}
	state := is.State
	if is.StateReason != "" {
		state += " as " + strings.ReplaceAll(is.StateReason, "_", " ")
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s #%d\n\n", is.Title, number)
	fmt.Fprintf(&b, "**%s** · opened by @%s · %s\n\n", state, is.User.Login, plural(is.Comments, "comment"))
	writeLabels(&b, is.Labels)
	writeBody(&b, is.Body)
	return b.String(), nil
}

type apiRelease struct {
	Name        string    `json:"name"`
	TagName     string    `json:"tag_name"`
	Author      apiUser   `json:"author"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Body        string    `json:"body"`
}

func (f *Fetcher) release(ctx context.Context, tag string) (string, error) {
	var r apiRelease
	if err := f.get(ctx, "releases/tags/"+url.PathEscape(tag), &r); err != nil {
		return "", err
	}
	title := r.Name
	if title == "" {
		title = r.TagName
	}
	kind := "release"
	if r.Prerelease {
		kind = "pre-release"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "**%s** `%s` · published by @%s on %s\n\n", kind, r.TagName, r.Author.Login, r.PublishedAt.Local().Format(time.DateOnly))
	writeBody(&b, r.Body)
	return b.String(), nil
}

type apiRef struct {
	Ref string `json:"ref"`
}

type apiCompare struct {
	TotalCommits int         `json:"total_commits"`
	Commits      []apiCommit `json:"commits"`
}

type apiCommit struct {
	SHA    string        `json:"sha"`
	Author *apiUser      `json:"author"`
	Commit apiCommitInfo `json:"commit"`
}

type apiCommitInfo struct {
	Message string        `json:"message"`
	Author  apiCommitUser `json:"author"`
}

type apiCommitUser struct {
	Name string `json:"name"`
}

// tag lists the commits since the previous tag, which is the highest semver tag below
// this one.
func (f *Fetcher) tag(ctx context.Context, tag string) (string, error) {
	var refs []apiRef
	if err := f.get(ctx, "git/matching-refs/tags", &refs); err != nil {
		return "", err
	}
	prev := previousTag(tag, refs)
	if prev == "" {
		return fmt.Sprintf("# %s\n\nNo earlier semver tag to compare with.\n", tag), nil
	}

	var cmp apiCompare
	path := "compare/" + url.PathEscape(prev) + "..." + url.PathEscape(tag)
	if err := f.get(ctx, path, &cmp); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n%d commits since `%s`\n\n", tag, cmp.TotalCommits, prev)
	// The compare API lists commits oldest first. Newest first reads like a changelog.
	commits := slices.Clone(cmp.Commits)
	slices.Reverse(commits)
	for i, c := range commits {
		if i == maxCommits {
			fmt.Fprintf(&b, "\n…and %d more\n", cmp.TotalCommits-maxCommits)
			break
		}
		who := c.Commit.Author.Name
		if c.Author != nil && c.Author.Login != "" {
			who = "@" + c.Author.Login
		}
		subject, _, _ := strings.Cut(c.Commit.Message, "\n")
		fmt.Fprintf(&b, "- `%s` %s — %s\n", c.SHA[:7], subject, who)
	}
	return b.String(), nil
}

// previousTag returns the highest semver tag in refs below tag, or "" if tag isn't
// semver or has no predecessor.
func previousTag(tag string, refs []apiRef) string {
	if !semver.IsValid(tag) {
		return ""
	}
	prev := ""
	for _, r := range refs {
		name := strings.TrimPrefix(r.Ref, "refs/tags/")
		if semver.IsValid(name) && semver.Compare(name, tag) < 0 && (prev == "" || semver.Compare(name, prev) > 0) {
			prev = name
		}
	}
	return prev
}

func (f *Fetcher) get(ctx context.Context, path string, v any) error {
	full := fmt.Sprintf("repos/%s/%s/%s", f.repo.Owner, f.repo.Name, path)
	if err := f.client.DoWithContext(ctx, http.MethodGet, full, nil, v); err != nil {
		return fmt.Errorf("get %s: %w", path, err)
	}
	return nil
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func writeLabels(b *strings.Builder, labels []apiLabel) {
	if len(labels) == 0 {
		return
	}
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = "`" + l.Name + "`"
	}
	fmt.Fprintf(b, "%s\n\n", strings.Join(names, " "))
}

func writeBody(b *strings.Builder, body string) {
	b.WriteString("---\n\n")
	if strings.TrimSpace(body) == "" {
		b.WriteString("_No description._\n")
		return
	}
	b.WriteString(body)
	b.WriteString("\n")
}
