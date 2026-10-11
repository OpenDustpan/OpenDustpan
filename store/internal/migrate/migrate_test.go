package migrate_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/store/internal/migrate"
)

func migrations(versions ...int64) []migrate.Migration {
	out := make([]migrate.Migration, 0, len(versions))
	for _, v := range versions {
		//nolint:exhaustruct_v5 // The test compares versions only; names and SQL stay empty.
		out = append(out, migrate.Migration{Version: v})
	}

	return out
}

func TestUnknownRecorded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		recorded []int64
		embedded []int64
		want     []int64
	}{
		{name: "equal", recorded: []int64{1, 2, 3}, embedded: []int64{1, 2, 3}, want: nil},
		{name: "older", recorded: []int64{1, 2}, embedded: []int64{1, 2, 3}, want: nil},
		{name: "empty database", recorded: nil, embedded: []int64{1, 2, 3}, want: nil},
		{name: "newer", recorded: []int64{1, 2, 3, 4}, embedded: []int64{1, 2, 3}, want: []int64{4}},
		{name: "newer gap", recorded: []int64{1, 2, 4}, embedded: []int64{1, 2, 3}, want: []int64{4}},
		{
			name:     "recorded gap is not newer",
			recorded: []int64{1, 3},
			embedded: []int64{1, 2, 3},
			want:     nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := migrate.UnknownRecorded(tc.recorded, migrations(tc.embedded...))
			if !slices.Equal(got, tc.want) {
				t.Fatalf("unknown = %v, want %v", got, tc.want)
			}
		})
	}
}
