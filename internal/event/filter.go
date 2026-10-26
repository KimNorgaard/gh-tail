package event

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Filter selects events by actor, author and label. The zero Filter matches everything.
type Filter struct {
	terms []term
}

type term struct {
	key    string
	negate bool
	value  *regexp.Regexp
}

// filterKeys are the qualifiers ParseFilter accepts.
var filterKeys = []string{"actor", "author", "label"}

// ParseFilter parses space-separated key:value terms, where key is actor, author or label.
// A leading - negates a term, * in a value matches any run of characters, and double
// quotes keep spaces in a value. Terms with the same key are ORed and different keys are
// ANDed, as in GitHub search. Matching ignores case.
func ParseFilter(s string) (Filter, error) {
	tokens, err := splitTerms(s)
	if err != nil {
		return Filter{}, err
	}
	var f Filter
	for _, tok := range tokens {
		t := term{}
		tok, t.negate = strings.CutPrefix(tok, "-")
		key, value, ok := strings.Cut(tok, ":")
		if !ok || value == "" {
			return Filter{}, fmt.Errorf("filter term %q is not key:value", tok)
		}
		if !slices.Contains(filterKeys, key) {
			return Filter{}, fmt.Errorf("unknown filter key %q, want one of %s", key, strings.Join(filterKeys, ","))
		}
		t.key = key
		t.value = regexp.MustCompile("(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(value), `\*`, ".*") + "$")
		f.terms = append(f.terms, t)
	}
	return f, nil
}

// Match reports whether e passes every negated term and, for each key with positive
// terms, at least one of them.
func (f Filter) Match(e Event) bool {
	wanted := map[string]bool{}
	for _, t := range f.terms {
		hit := t.matches(e)
		if t.negate {
			if hit {
				return false
			}
			continue
		}
		wanted[t.key] = wanted[t.key] || hit
	}
	for _, hit := range wanted {
		if !hit {
			return false
		}
	}
	return true
}

// matches reports whether the term's value matches the field. An empty field never
// matches, so actor:* drops events with no known actor.
func (t term) matches(e Event) bool {
	var values []string
	switch t.key {
	case "actor":
		values = []string{e.Actor}
	case "author":
		values = []string{e.Author}
	case "label":
		values = e.Labels
	}
	for _, v := range values {
		if v != "" && t.value.MatchString(v) {
			return true
		}
	}
	return false
}

// splitTerms splits s on spaces outside double quotes and drops the quotes.
func splitTerms(s string) ([]string, error) {
	var (
		out     []string
		cur     strings.Builder
		quoted  bool
		pending bool
	)
	for _, r := range s {
		switch {
		case r == '"':
			quoted, pending = !quoted, true
		case unicode.IsSpace(r) && !quoted:
			if pending {
				out = append(out, cur.String())
				cur.Reset()
				pending = false
			}
		default:
			cur.WriteRune(r)
			pending = true
		}
	}
	if quoted {
		return nil, errors.New("filter has an unterminated quote")
	}
	if pending {
		out = append(out, cur.String())
	}
	return out, nil
}
