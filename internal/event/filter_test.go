package event

import "testing"

func TestFilter(t *testing.T) {
	bot := Event{Actor: "kubernetes-prow[bot]", Author: "ann", Labels: []string{"sig/etcd"}}
	ann := Event{Actor: "Ann", Author: "ann", Labels: []string{"good first issue", "bug"}}
	tag := Event{}

	cases := []struct {
		filter string
		want   []bool // bot, ann, tag
	}{
		{"", []bool{true, true, true}},
		{"-actor:*[bot]", []bool{false, true, true}},
		{"actor:ann", []bool{false, true, false}},
		{"actor:bob actor:ann", []bool{false, true, false}},
		{"author:ann label:bug", []bool{false, true, false}},
		{"label:sig/*", []bool{true, false, false}},
		{`label:"good first issue"`, []bool{false, true, false}},
		{`-label:"good first issue" author:ann`, []bool{true, false, false}},
	}
	for _, c := range cases {
		f, err := ParseFilter(c.filter)
		if err != nil {
			t.Fatalf("ParseFilter(%q): %v", c.filter, err)
		}
		for i, e := range []Event{bot, ann, tag} {
			if got := f.Match(e); got != c.want[i] {
				t.Errorf("%q matching event %d = %v, want %v", c.filter, i, got, c.want[i])
			}
		}
	}

	for _, bad := range []string{"bug", "kind:pr", "actor:", `label:"open`} {
		if _, err := ParseFilter(bad); err == nil {
			t.Errorf("ParseFilter(%q) succeeded, want error", bad)
		}
	}
}
