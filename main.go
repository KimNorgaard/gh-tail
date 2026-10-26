// Command gh-tail streams repository activity to the terminal.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/cli/go-gh/v2/pkg/term"

	"github.com/containdk/KimNorgaard/gh-tail/internal/details"
	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/output"
	"github.com/containdk/KimNorgaard/gh-tail/internal/poll"
	"github.com/containdk/KimNorgaard/gh-tail/internal/source"
	"github.com/containdk/KimNorgaard/gh-tail/internal/tui"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gh-tail:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	var (
		repoFlag    string
		kindsFlag   string
		filterFlag  string
		jsonOut     bool
		interactive bool
		interval    time.Duration
		backlog     int
		showVersion bool
	)
	flags := flag.NewFlagSet("gh-tail", flag.ContinueOnError)
	flags.StringVar(&repoFlag, "R", "", "shorthand for --repo")
	flags.StringVar(&repoFlag, "repo", "", "repository as [HOST/]OWNER/REPO (default: current repository)")
	flags.StringVar(&kindsFlag, "kinds", "tag,pr,issue", "comma-separated kind, kind:action or kind:* entries. Kinds: tag,branch,pr,issue,release,package")
	flags.StringVar(&filterFlag, "filter", "", "space-separated [-]actor:, author: or label: terms; * globs, @me is you")
	flags.BoolVar(&jsonOut, "json", false, "print events as JSON lines")
	flags.BoolVar(&interactive, "i", false, "shorthand for --interactive")
	flags.BoolVar(&interactive, "interactive", false, "browse events and their details in a full-screen view")
	flags.DurationVar(&interval, "interval", time.Minute, "poll interval, raised to the server's X-Poll-Interval")
	flags.IntVar(&backlog, "n", 10, "number of past events to show at startup")
	flags.BoolVar(&showVersion, "version", false, "print the version")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	if showVersion {
		fmt.Println(version)
		return nil
	}

	terminal := term.FromEnv()
	if interactive && jsonOut {
		return errors.New("--interactive and --json can't be combined")
	}
	if interactive && !terminal.IsTerminalOutput() {
		return errors.New("--interactive needs a terminal")
	}

	repo, err := resolveRepo(repoFlag)
	if err != nil {
		return err
	}
	sel, err := event.ParseSelection(kindsFlag)
	if err != nil {
		return err
	}
	transport := source.NewTransport(http.DefaultTransport)
	client, err := api.NewRESTClient(api.ClientOptions{Host: repo.Host, Transport: transport})
	if err != nil {
		return err
	}
	filter, err := parseFilter(client, filterFlag)
	if err != nil {
		return err
	}

	poller := &poll.Poller{
		Sources:   source.New(client, repo, source.Options{Selection: sel, Filter: filter, Backlog: backlog}),
		Filter:    filter,
		Backlog:   backlog,
		Interval:  interval,
		Transport: transport,
	}
	name := repo.Owner + "/" + repo.Name

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if interactive {
		return tui.Run(ctx, poller, details.New(client, repo), tui.Options{Repo: name, Theme: markdownTheme(terminal)})
	}
	if jsonOut {
		return poller.Run(ctx, output.NewJSON(os.Stdout, os.Stderr))
	}

	stream := output.NewStream(os.Stdout, os.Stderr, name, terminal.IsTerminalOutput(), func() int {
		w, _, err := terminal.Size()
		if err != nil {
			return 0
		}
		return w
	})
	animated := make(chan struct{})
	animCtx, stopAnim := context.WithCancel(ctx)
	go func() {
		stream.Animate(animCtx)
		close(animated)
	}()
	err = poller.Run(ctx, stream)
	stopAnim()
	<-animated
	return err
}

// markdownTheme picks the details theme from the terminal background. It queries the
// terminal, so it must run before the TUI takes over input.
func markdownTheme(t term.Term) string {
	if theme := t.Theme(); theme != "none" {
		return theme
	}
	return "notty"
}

func resolveRepo(s string) (repository.Repository, error) {
	if s == "" {
		return repository.Current()
	}
	return repository.Parse(s)
}

// parseFilter parses the --filter value after replacing @me with the authenticated login.
func parseFilter(client *api.RESTClient, s string) (event.Filter, error) {
	if strings.Contains(s, "@me") {
		var user struct {
			Login string `json:"login"`
		}
		if err := client.Get("user", &user); err != nil {
			return event.Filter{}, fmt.Errorf("resolve @me: %w", err)
		}
		s = strings.ReplaceAll(s, "@me", user.Login)
	}
	return event.ParseFilter(s)
}
