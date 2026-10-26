package event

import "testing"

func TestParseSelection(t *testing.T) {
	sel, err := ParseSelection("pr:merged, tag,issue:*,pr:opened")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind   Kind
		action string
		want   bool
	}{
		{PR, "merged", true},
		{PR, "opened", true},
		{PR, "closed", false},
		{Tag, "created", true},
		{Tag, "deleted", false},
		{Issue, "labeled", true},
		{Issue, "some-new-action", true},
		{Branch, "pushed", false},
	}
	for _, c := range cases {
		if got := sel.Has(c.kind, c.action); got != c.want {
			t.Errorf("Has(%s, %s) = %v, want %v", c.kind, c.action, got, c.want)
		}
	}

	for _, bad := range []string{"prs", "pr:merge", "tag,", "release:created"} {
		if _, err := ParseSelection(bad); err == nil {
			t.Errorf("ParseSelection(%q) succeeded, want error", bad)
		}
	}
}
