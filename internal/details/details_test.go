package details

import "testing"

func TestPreviousTag(t *testing.T) {
	refs := []apiRef{
		{Ref: "refs/tags/v1.2.0"},
		{Ref: "refs/tags/v1.10.0"},
		{Ref: "refs/tags/v1.9.0"},
		{Ref: "refs/tags/v2.0.0-rc.1"},
		{Ref: "refs/tags/nightly"},
		{Ref: "refs/tags/platform/v3.2.3"},
		{Ref: "refs/tags/platform/v3.2.10"},
		{Ref: "refs/tags/psc/v3.2.5"},
		{Ref: "refs/tags/0.9.0"},
	}
	cases := map[string]string{
		"v1.10.0":         "v1.9.0",
		"v2.0.0":          "v2.0.0-rc.1",
		"v1.2.0":          "0.9.0",
		"nightly":         "",
		"platform/v3.2.4": "platform/v3.2.3",
		"platform/v3.3.0": "platform/v3.2.10",
		"psc/v3.2.5":      "",
		"1.0.0":           "0.9.0",
	}
	for tag, want := range cases {
		if got := previousTag(tag, refs); got != want {
			t.Errorf("previousTag(%q) = %q, want %q", tag, got, want)
		}
	}
}
