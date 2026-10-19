package migrate_test

import (
	"errors"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"opendustpan.org/opendustpan/pkg/store/internal/migrate"
	"opendustpan.org/opendustpan/pkg/store/schema"
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

func applied(minimums map[int64]string, versions ...int64) []schema.AppliedMigration {
	out := make([]schema.AppliedMigration, 0, len(versions))
	for _, v := range versions {
		out = append(out, schema.AppliedMigration{Version: v, Name: "", MinBinaryVersion: minimums[v]})
	}

	return out
}

func TestCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		recorded    []schema.AppliedMigration
		embedded    []int64
		binary      string
		wantPending []int64
		wantMinimum string
		wantErr     error
	}{
		{
			name: "empty database", recorded: nil, embedded: []int64{1, 2}, binary: "v0.1.0",
			wantPending: []int64{1, 2}, wantMinimum: "", wantErr: nil,
		},
		{
			name: "older", recorded: applied(nil, 1), embedded: []int64{1, 2}, binary: "v0.1.0",
			wantPending: []int64{2}, wantMinimum: "", wantErr: nil,
		},
		{
			name:     "newer schema at the minimum applies nothing",
			recorded: applied(map[int64]string{3: "v0.2.0"}, 1, 2, 3), embedded: []int64{1, 2}, binary: "v0.2.0",
			wantPending: nil, wantMinimum: "v0.2.0", wantErr: nil,
		},
		{
			name:     "newer schema with no minimum applies nothing",
			recorded: applied(nil, 1, 2, 3), embedded: []int64{1, 2}, binary: "v0.1.0",
			wantPending: nil, wantMinimum: "", wantErr: nil,
		},
		{
			name:     "binary below the minimum",
			recorded: applied(map[int64]string{1: "v0.1.0", 3: "v0.3.0"}, 1, 2, 3), embedded: []int64{1, 2},
			binary: "v0.2.0", wantPending: nil, wantMinimum: "v0.3.0", wantErr: schema.ErrBinaryBelowMinimum,
		},
		{
			name:     "dev build below the minimum",
			recorded: applied(map[int64]string{3: "v9.0.0"}, 1, 2, 3), embedded: []int64{1, 2},
			binary: "dev", wantPending: nil, wantMinimum: "v9.0.0", wantErr: nil,
		},
		{
			name:     "diverged schema",
			recorded: applied(nil, 1, 3), embedded: []int64{1, 2}, binary: "v0.1.0",
			wantPending: nil, wantMinimum: "", wantErr: schema.ErrSchemaDiverged,
		},
		{
			name:     "bad recorded minimum",
			recorded: applied(map[int64]string{1: "later"}, 1), embedded: []int64{1}, binary: "v0.1.0",
			wantPending: nil, wantMinimum: "", wantErr: schema.ErrBadMinimum,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan, err := migrate.Check(tc.recorded, migrations(tc.embedded...), tc.binary)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Check err = %v, want %v", err, tc.wantErr)
				}

				return
			}

			checkPlan(t, plan, err, tc.wantPending, tc.wantMinimum)
		})
	}
}

// checkPlan proves that Check succeeded with the pending versions and the
// minimum.
func checkPlan(t *testing.T, plan migrate.Plan, err error, wantPending []int64, wantMinimum string) {
	t.Helper()

	if err != nil {
		t.Fatalf("Check: %v", err)
	}

	pending := make([]int64, 0, len(plan.Pending))
	for _, m := range plan.Pending {
		pending = append(pending, m.Version)
	}

	if !slices.Equal(pending, wantPending) {
		t.Fatalf("pending = %v, want %v", pending, wantPending)
	}

	if plan.Minimum != wantMinimum {
		t.Fatalf("minimum = %q, want %q", plan.Minimum, wantMinimum)
	}
}

func TestLoadReadsMinimumHeader(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"m/0001_init.sql": {Data: []byte("CREATE TABLE a (x INTEGER);\n"), Mode: 0, ModTime: time.Time{}, Sys: nil},
		"m/0002_contract.sql": {Data: []byte(
			"-- Drop the old column.\n-- minimum-binary-version: v0.3.0\nALTER TABLE a DROP COLUMN x;\n",
		), Mode: 0, ModTime: time.Time{}, Sys: nil},
	}

	all, err := migrate.Load(fsys, "m")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if len(all) != 2 || all[0].MinBinaryVersion != "" || all[1].MinBinaryVersion != "v0.3.0" {
		t.Fatalf("minimums = %+v, want \"\" and v0.3.0", all)
	}
}

func TestLoadReadsMinimumHeaderForms(t *testing.T) {
	t.Parallel()

	for name, sql := range map[string]string{
		"blank line in the leading block": "-- Drop the old column.\n\n-- minimum-binary-version: v0.3.0\nSELECT 1;\n",
		"no space after the dashes":       "--minimum-binary-version: v0.3.0\nSELECT 1;\n",
		"another case":                    "-- MINIMUM-Binary-Version: v0.3.0\nSELECT 1;\n",
		"space before the colon":          "-- minimum-binary-version : v0.3.0\nSELECT 1;\n",
	} {
		fsys := fstest.MapFS{"m/0001_contract.sql": mapFile(sql)}

		all, err := migrate.Load(fsys, "m")
		if err != nil || len(all) != 1 || all[0].MinBinaryVersion != "v0.3.0" {
			t.Errorf("%s: load = %+v, %v; want minimum v0.3.0", name, all, err)
		}
	}
}

func TestLoadRefusesBadMinimumHeader(t *testing.T) {
	t.Parallel()

	for name, sql := range map[string]string{
		"not a version":         "-- minimum-binary-version: 0.3\nCREATE TABLE a (x INTEGER);\n",
		"a pre-release":         "-- minimum-binary-version: v0.3.0-rc.1\nSELECT 1;\n",
		"after SQL":             "SELECT 1;\n-- minimum-binary-version: v0.3.0\n",
		"in a block comment":    "/* minimum-binary-version: v0.3.0 */\nSELECT 1;\n",
		"twice":                 "-- minimum-binary-version: v0.3.0\n-- minimum-binary-version: v0.4.0\nSELECT 1;\n",
		"inside a comment line": "-- The header minimum-binary-version: v0.3.0\nSELECT 1;\n",
		"no colon":              "-- minimum-binary-version v0.3.0\nSELECT 1;\n",
	} {
		fsys := fstest.MapFS{"m/0001_init.sql": mapFile(sql)}

		if _, err := migrate.Load(fsys, "m"); !errors.Is(err, schema.ErrBadMinimum) {
			t.Errorf("%s: load err = %v, want ErrBadMinimum", name, err)
		}
	}
}

// mapFile returns a file of fstest.MapFS that holds sql.
func mapFile(sql string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(sql), Mode: 0, ModTime: time.Time{}, Sys: nil}
}

func TestCheckRefusesPendingMinimumAboveBinary(t *testing.T) {
	t.Parallel()

	embedded := migrations(1, 2)
	embedded[1].MinBinaryVersion = "v0.3.0"

	_, err := migrate.Check(applied(nil, 1), embedded, "v0.2.0")
	if !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("Check with a pending migration above the binary = %v, want ErrBinaryBelowMinimum", err)
	}

	plan, err := migrate.Check(applied(nil, 1), embedded, "v0.3.0")
	if err != nil || len(plan.Pending) != 1 {
		t.Fatalf("Check at the minimum = %+v, %v; want one pending migration", plan, err)
	}

	plan, err = migrate.Check(applied(nil, 1), embedded, "dev")
	if err != nil || len(plan.Pending) != 1 {
		t.Fatalf("Check of a dev build = %+v, %v; want one pending migration", plan, err)
	}
}

func TestCheckRefusesStoredMinimumWithPendingMigrations(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"m/0001_init.sql":  mapFile("CREATE TABLE a (x INTEGER);\n"),
		"m/0002_extra.sql": mapFile("ALTER TABLE a ADD COLUMN y INTEGER;\n"),
	}

	embedded, err := migrate.Load(fsys, "m")
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	recorded := applied(map[int64]string{1: "v0.3.0"}, 1)

	_, err = migrate.Check(recorded, embedded, "v0.2.0")
	if !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("Check below the stored minimum with a pending migration = %v, want ErrBinaryBelowMinimum", err)
	}

	plan, err := migrate.Check(recorded, embedded, "v0.3.0")
	if err != nil {
		t.Fatalf("Check at the stored minimum: %v", err)
	}

	if len(plan.Pending) != 1 || plan.Pending[0].Version != 2 {
		t.Fatalf("pending = %+v, want the version 2 migration", plan.Pending)
	}
}

func TestCheckRefusesBadBinaryVersion(t *testing.T) {
	t.Parallel()

	if _, err := migrate.Check(nil, migrations(1), "release"); !errors.Is(err, schema.ErrBadBinaryVersion) {
		t.Fatalf("Check with a bad binary version = %v, want ErrBadBinaryVersion", err)
	}
}

func TestCollapseVersion(t *testing.T) {
	t.Parallel()

	b := schema.BaselineVersion

	cases := []struct {
		name     string
		versions []int64
		want     int64
	}{
		{name: "no files", versions: nil, want: b},
		{name: "files below the baseline", versions: []int64{1, b / 2, b - 1}, want: b},
		{name: "files from the baseline on", versions: []int64{b, b + 3, b + 4}, want: b + 5},
		{name: "mixed", versions: []int64{7, b - 1, b, b + 4}, want: b + 5},
		{name: "unordered", versions: []int64{b + 4, 3, b}, want: b + 5},
		{name: "one file at the baseline", versions: []int64{b}, want: b + 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := migrate.CollapseVersion(tc.versions); got != tc.want {
				t.Fatalf("CollapseVersion(%v) = %d, want %d", tc.versions, got, tc.want)
			}

			for _, v := range tc.versions {
				if migrate.CollapseVersion(tc.versions) <= v {
					t.Fatalf("CollapseVersion(%v) is not above %d", tc.versions, v)
				}
			}
		})
	}
}
