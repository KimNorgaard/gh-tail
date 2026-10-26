package details

import "testing"

func TestPreviousTag(t *testing.T) {
	refs := []apiRef{
		{Ref: "refs/tags/v1.2.0"},
		{Ref: "refs/tags/v1.10.0"},
		{Ref: "refs/tags/v1.9.0"},
		{Ref: "refs/tags/v2.0.0-rc.1"},
		{Ref: "refs/tags/nightly"},
	}
	cases := map[string]string{
		"v1.10.0": "v1.9.0",
		"v2.0.0":  "v2.0.0-rc.1",
		"v1.2.0":  "",
		"nightly": "",
	}
	for tag, want := range cases {
		if got := previousTag(tag, refs); got != want {
			t.Errorf("previousTag(%q) = %q, want %q", tag, got, want)
		}
	}
}
