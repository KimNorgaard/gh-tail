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
	"slices"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/repository"

	"github.com/containdk/KimNorgaard/gh-tail/internal/event"
	"github.com/containdk/KimNorgaard/gh-tail/internal/output"
	"github.com/containdk/KimNorgaard/gh-tail/internal/source"
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
	sources := source.New(client, repo, source.Options{Selection: sel, Filter: filter, Backlog: backlog})

	printEvent := output.Table(os.Stdout)
	if jsonOut {
		printEvent = output.JSON(os.Stdout)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	for first := true; ; first = false {
		var batch []event.Event
		for _, s := range sources {
			events, err := s.Poll(ctx)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				// A failing first poll is a setup problem. Later failures are usually transient.
				if first {
					return err
				}
				fmt.Fprintln(os.Stderr, "gh-tail:", err)
				continue
			}
			batch = append(batch, events...)
		}

		batch = slices.DeleteFunc(batch, func(e event.Event) bool { return !filter.Match(e) })
		slices.SortStableFunc(batch, func(a, b event.Event) int { return a.Time.Compare(b.Time) })
		if first && len(batch) > backlog {
			batch = batch[len(batch)-backlog:]
		}
		for _, e := range batch {
			if err := printEvent(e); err != nil {
				return err
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(max(interval, transport.PollInterval())):
		}
	}
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
