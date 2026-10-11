package stack_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

func TestCountManagedResources(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		deployment string
		want       int64
	}{
		{name: "no resources", deployment: `{"resources":[]}`, want: 0},
		{name: "only the stack resource", deployment: `{"resources":[{"type":"pulumi:pulumi:Stack"}]}`, want: 0},
		{
			name:       "one managed resource",
			deployment: `{"resources":[{"type":"pulumi:pulumi:Stack"},{"type":"custom:resource:Type"}]}`,
			want:       1,
		},
		{
			name:       "two managed resources",
			deployment: `{"resources":[{"type":"a:one:X"},{"type":"b:two:Y"}]}`,
			want:       2,
		},
		{name: "no resources field", deployment: `{"v":1}`, want: 0},
		{name: "empty deployment", deployment: ``, want: 1},
		{name: "invalid JSON", deployment: `{`, want: 1},
		{name: "null deployment", deployment: `null`, want: 0},
		{name: "null resources", deployment: `{"resources":null}`, want: 0},
		{name: "resources of another type", deployment: `{"resources":{}}`, want: 1},
		{name: "trailing text", deployment: `{"resources":[]} trailing`, want: 1},
		{name: "case-insensitive field", deployment: `{"Resources":[{"Type":"a:one:X"}]}`, want: 1},
		{name: "entry without a type", deployment: `{"resources":[{}]}`, want: 1},
		{name: "entry of another shape", deployment: `{"resources":[5]}`, want: 1},
		{name: "entry type of another shape", deployment: `{"resources":[{"type":5}]}`, want: 1},
		{name: "nested resources stay out", deployment: `{"a":{"resources":[{"type":"a:one:X"}]}}`, want: 0},
		{
			name:       "extra fields stay out",
			deployment: `{"version":3,"resources":[{"type":"a:one:X","outputs":{"big":[1,2,3]}}]}`,
			want:       1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := stack.CountManagedResources([]byte(tc.deployment)); got != tc.want {
				t.Fatalf("count of %q = %d, want %d", tc.deployment, got, tc.want)
			}
		})
	}
}
