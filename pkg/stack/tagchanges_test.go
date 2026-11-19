package stack_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestTagChanges proves that a replace of the tags gives one change for each
// key that it adds, removes or changes, sorted by key, and never a tag value.
func TestTagChanges(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-value"

	cases := []struct {
		old, next map[string]string
		name      string
		want      []string
	}{
		{name: "no tags", old: nil, next: nil, want: nil},
		{name: "same tags", old: map[string]string{"a": "1"}, next: map[string]string{"a": "1"}, want: nil},
		{name: "added", old: nil, next: map[string]string{"b": secret}, want: []string{"tags.b:not set>set"}},
		{
			name: "removed",
			old:  map[string]string{"a": secret},
			next: map[string]string{},
			want: []string{"tags.a:set>not set"},
		},
		{
			name: "changed",
			old:  map[string]string{"a": secret},
			next: map[string]string{"a": "x"},
			want: []string{"tags.a:set>set"},
		},
		{
			name: "mixed and sorted",
			old:  map[string]string{"z": "1", "m": "1", "keep": "1"},
			next: map[string]string{"a": secret, "m": "2", "keep": "1"},
			want: []string{"tags.a:not set>set", "tags.m:set>set", "tags.z:set>not set"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := describeTagChanges(t, stack.TagChanges(tc.old, tc.next), secret)

			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("changes = %v, want %v", got, tc.want)
			}
		})
	}
}

// describeTagChanges returns "key:old>new" for each change and fails the test
// if a change has no old side or holds secret.
func describeTagChanges(t *testing.T, changes []audit.Change, secret string) []string {
	t.Helper()

	out := make([]string, 0, len(changes))

	for _, c := range changes {
		if c.Old == nil {
			t.Fatalf("change %+v has no old side", c)
		}

		if strings.Contains(c.New, secret) || strings.Contains(*c.Old, secret) {
			t.Fatalf("change %+v holds a tag value", c)
		}

		out = append(out, c.Key+":"+*c.Old+">"+c.New)
	}

	return out
}
