package migrate_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
)

func migrations(versions ...int64) []migrate.Migration {
	out := make([]migrate.Migration, 0, len(versions))
	for _, v := range versions {
		//nolint:exhaustruct_v5 // The test compares versions only; names and SQL stay empty.
		out = append(out, migrate.Migration{Version: v})
	}

	return out
}

func TestInspect(t *testing.T) {
	t.Parallel()

	embedded := func() []migrate.Migration {
		out := make([]migrate.Migration, 0, 3)
		for i, name := range []string{"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql"} {
			//nolint:exhaustruct_v5 // The test compares versions and names only; SQL stays empty.
			out = append(out, migrate.Migration{Name: name, Version: int64(i + 1)})
		}

		return out
	}()

	cases := []struct {
		name     string
		recorded []int64
		wantDB   int64
		wantBin  int64
		wantPend []string
		wantUnkn []int64
	}{
		{
			name:     "empty database",
			recorded: nil,
			wantDB:   0,
			wantBin:  3,
			wantPend: []string{"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql"},
			wantUnkn: nil,
		},
		{
			name:     "older",
			recorded: []int64{1, 2},
			wantDB:   2,
			wantBin:  3,
			wantPend: []string{"0003_stack_id.sql"},
			wantUnkn: nil,
		},
		{
			name:     "equal",
			recorded: []int64{1, 2, 3},
			wantDB:   3,
			wantBin:  3,
			wantPend: nil,
			wantUnkn: nil,
		},
		{
			name:     "newer",
			recorded: []int64{1, 2, 3, 4},
			wantDB:   4,
			wantBin:  3,
			wantPend: nil,
			wantUnkn: []int64{4},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := migrate.Inspect(tc.recorded, embedded)
			if got.DatabaseVersion != tc.wantDB || got.BinaryVersion != tc.wantBin {
				t.Fatalf("versions = %d/%d, want %d/%d",
					got.DatabaseVersion, got.BinaryVersion, tc.wantDB, tc.wantBin)
			}

			if !slices.Equal(got.Pending, tc.wantPend) {
				t.Fatalf("pending = %v, want %v", got.Pending, tc.wantPend)
			}

			if !slices.Equal(got.Unknown, tc.wantUnkn) {
				t.Fatalf("unknown = %v, want %v", got.Unknown, tc.wantUnkn)
			}
		})
	}
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
