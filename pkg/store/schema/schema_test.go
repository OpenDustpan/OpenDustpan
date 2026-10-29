package schema_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

func TestCheckMinimum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		minimum string
		binary  string
		want    error
	}{
		{name: "no minimum", minimum: "", binary: "v0.1.0", want: nil},
		{name: "equal", minimum: "v0.2.0", binary: "v0.2.0", want: nil},
		{name: "above", minimum: "v0.2.0", binary: "v0.10.0", want: nil},
		{name: "major above", minimum: "v0.9.9", binary: "v1.0.0", want: nil},
		{name: "below patch", minimum: "v0.2.1", binary: "v0.2.0", want: schema.ErrBinaryBelowMinimum},
		{name: "below minor", minimum: "v0.10.0", binary: "v0.9.0", want: schema.ErrBinaryBelowMinimum},
		{name: "below major", minimum: "v2.0.0", binary: "v1.9.9", want: schema.ErrBinaryBelowMinimum},
		{name: "release stamp with no v", minimum: "v0.3.0", binary: "0.2.0", want: schema.ErrBinaryBelowMinimum},
		{name: "release stamp with no v at the minimum", minimum: "v0.3.0", binary: "0.3.0", want: nil},
		{name: "capital V", minimum: "v0.3.0", binary: "V0.2.0", want: schema.ErrBinaryBelowMinimum},
		{name: "spaces around", minimum: "v0.3.0", binary: " v0.2.0 ", want: schema.ErrBinaryBelowMinimum},
		{name: "describe build counts as its tag", minimum: "v0.2.0", binary: "v0.2.0-3-gabc1234-dirty", want: nil},
		{
			name:    "describe build below",
			minimum: "v0.3.0",
			binary:  "v0.2.0-3-gabc1234",
			want:    schema.ErrBinaryBelowMinimum,
		},
		{name: "dirty tag build", minimum: "v0.2.0", binary: "v0.2.0-dirty", want: nil},
		{
			name:    "pre-release below its release",
			minimum: "v0.2.0",
			binary:  "v0.2.0-rc.1",
			want:    schema.ErrBinaryBelowMinimum,
		},
		{name: "pre-release above the release before", minimum: "v0.1.0", binary: "v0.2.0-rc.1", want: nil},
		{name: "build metadata ignored", minimum: "v0.2.0", binary: "v0.2.0+linux", want: nil},
		{name: "dev build", minimum: "v9.0.0", binary: "dev", want: nil},
		{name: "empty build", minimum: "v9.0.0", binary: "", want: nil},
		{name: "commit hash build", minimum: "v9.0.0", binary: "c69fb92", want: nil},
		{name: "dirty commit hash build", minimum: "v9.0.0", binary: "c69fb92-dirty", want: nil},
		{
			name:    "overflow fails closed",
			minimum: "v0.1.0",
			binary:  "v99999999999999999999.0.0",
			want:    schema.ErrBadBinaryVersion,
		},
		{name: "two parts fail closed", minimum: "v0.1.0", binary: "v1.2", want: schema.ErrBadBinaryVersion},
		{name: "a word fails closed", minimum: "", binary: "release", want: schema.ErrBadBinaryVersion},
		{name: "an empty pre-release fails closed", minimum: "", binary: "v1.2.3-", want: schema.ErrBadBinaryVersion},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := schema.CheckMinimum(tc.minimum, tc.binary)

			switch {
			case tc.want == nil && err != nil:
				t.Fatalf("CheckMinimum(%q, %q) = %v, want nil", tc.minimum, tc.binary, err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("CheckMinimum(%q, %q) = %v, want %v", tc.minimum, tc.binary, err, tc.want)
			default:
			}
		})
	}
}

// TestReleaseStampFormat pins the version stamps of a release build: the
// tag with the v, and the version with no v that goreleaser stamps. Both
// count as the same release, never as a dev build.
func TestReleaseStampFormat(t *testing.T) {
	t.Parallel()

	for _, stamp := range []string{"v0.1.0", "0.1.0"} {
		if schema.IsDevBuild(stamp) {
			t.Errorf("IsDevBuild(%q) = true, want a release", stamp)
		}

		if err := schema.CheckBinaryVersion(stamp); err != nil {
			t.Errorf("CheckBinaryVersion(%q) = %v, want nil", stamp, err)
		}

		if err := schema.CheckMinimum("v0.1.1", stamp); !errors.Is(err, schema.ErrBinaryBelowMinimum) {
			t.Errorf("CheckMinimum(v0.1.1, %q) = %v, want ErrBinaryBelowMinimum", stamp, err)
		}

		if err := schema.CheckMinimum("v0.1.0", stamp); err != nil {
			t.Errorf("CheckMinimum(v0.1.0, %q) = %v, want nil", stamp, err)
		}
	}
}

func TestMinimumErrorNamesBothVersions(t *testing.T) {
	t.Parallel()

	err := schema.CheckMinimum("v0.3.0", "v0.2.0")
	if err == nil {
		t.Fatal("CheckMinimum below the minimum = nil, want a *MinimumError")
	}

	minimum, ok := errors.AsType[*schema.MinimumError](err)
	if !ok {
		t.Fatalf("err = %v, want a *MinimumError", err)
	}

	if minimum.SchemaMinimum != "v0.3.0" || minimum.BinaryVersion != "v0.2.0" {
		t.Fatalf("versions = %q/%q, want v0.3.0/v0.2.0", minimum.SchemaMinimum, minimum.BinaryVersion)
	}

	want := "binary version v0.2.0 is below v0.3.0, the minimum binary version of the database schema: " +
		"start a binary of version v0.3.0 or later, or restore a backup"
	if err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
}

func TestCheckMinimumRefusesBadMinimum(t *testing.T) {
	t.Parallel()

	if err := schema.CheckMinimum("next", "v0.2.0"); !errors.Is(err, schema.ErrBadMinimum) {
		t.Fatalf("CheckMinimum with a bad minimum = %v, want ErrBadMinimum", err)
	}
}

func TestHighestMinimum(t *testing.T) {
	t.Parallel()

	got, err := schema.HighestMinimum([]string{"", "v0.2.0", "v0.10.0", "v0.9.1", ""})
	if err != nil || got != "v0.10.0" {
		t.Fatalf("HighestMinimum = %q, %v, want v0.10.0, nil", got, err)
	}

	if got, err := schema.HighestMinimum([]string{"", ""}); err != nil || got != "" {
		t.Fatalf("HighestMinimum of none = %q, %v, want \"\", nil", got, err)
	}

	if _, err := schema.HighestMinimum([]string{"v0.1.0", "soon"}); !errors.Is(err, schema.ErrBadMinimum) {
		t.Fatalf("HighestMinimum with a bad value = %v, want ErrBadMinimum", err)
	}
}

func TestIsDevBuild(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"", "dev", " dev ", "c69fb92", "c69fb92-dirty", "0123456789abcdef0123456789abcdef01234567"} {
		if !schema.IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false, want true", v)
		}
	}

	for _, v := range []string{"v0.1.0", "0.1.0", "v1.2.3-rc.1", "v0.1.0-3-gabc1234-dirty", "v1", "release"} {
		if schema.IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = true, want false", v)
		}
	}
}

func TestVersions(t *testing.T) {
	t.Parallel()

	if got := schema.BinaryVersion([]int64{1, 3, 2}); got != 3 {
		t.Fatalf("binary version = %d, want 3", got)
	}

	if got := schema.DatabaseVersion([]int64{1, 11, 4}); got != 11 {
		t.Fatalf("database version = %d, want 11", got)
	}

	if got := schema.BinaryVersion(nil); got != 0 {
		t.Fatalf("empty binary version = %d, want 0", got)
	}

	if got := schema.DatabaseVersion(nil); got != 0 {
		t.Fatalf("empty database version = %d, want 0", got)
	}
}

func TestParseMigrationVersion(t *testing.T) {
	t.Parallel()

	if got, err := schema.ParseMigrationVersion("0014_checkpoint_resource_count.sql"); err != nil || got != 14 {
		t.Fatalf("parse = %d, %v, want 14, nil", got, err)
	}

	for _, name := range []string{"", "init.sql", "0000_zero.sql", "-1_neg.sql", "xx_bad.sql"} {
		if _, err := schema.ParseMigrationVersion(name); !errors.Is(err, schema.ErrBadMigrationName) {
			t.Fatalf("parse %q: err = %v, want ErrBadMigrationName", name, err)
		}
	}
}

func TestEmbeddedMigrationNames(t *testing.T) {
	t.Parallel()

	names, newest := schema.EmbeddedMigrationNames()

	if len(names) == 0 || newest <= 0 {
		t.Fatalf("embedded list holds %d names with newest %d, want a non-empty list", len(names), newest)
	}

	for _, name := range names {
		version, err := schema.ParseMigrationVersion(name)
		if err != nil {
			t.Fatalf("embedded name %q: %v", name, err)
		}

		if version > newest {
			t.Fatalf("embedded name %q holds version %d above newest %d", name, version, newest)
		}
	}

	second, secondNewest := schema.EmbeddedMigrationNames()
	if !slices.Equal(names, second) || newest != secondNewest {
		t.Fatalf("second call = %v/%d, want %v/%d", second, secondNewest, names, newest)
	}

	names[0] = "changed.sql"

	third, _ := schema.EmbeddedMigrationNames()
	if third[0] == "changed.sql" {
		t.Fatal("embedded names share backing storage, want a copy per call")
	}
}

// TestBaselineVersionsAgree pins the baseline version of the backup package,
// which cannot import this package, to the baseline version of the schema.
func TestBaselineVersionsAgree(t *testing.T) {
	t.Parallel()

	if int64(backup.BaselineSchemaVersion) != schema.BaselineVersion {
		t.Fatalf(
			"backup baseline %d, schema baseline %d, want equal",
			backup.BaselineSchemaVersion,
			schema.BaselineVersion,
		)
	}

	version, err := schema.ParseMigrationVersion(schema.BaselineName)
	if err != nil || version != schema.BaselineVersion {
		t.Fatalf(
			"baseline name %q has version %d, %v, want %d",
			schema.BaselineName,
			version,
			err,
			schema.BaselineVersion,
		)
	}
}

func TestCheckBaseline(t *testing.T) {
	t.Parallel()

	for _, recorded := range [][]int64{nil, {schema.BaselineVersion}, {schema.BaselineVersion, schema.BaselineVersion + 1}} {
		if err := schema.CheckBaseline(recorded); err != nil {
			t.Fatalf("check baseline of %v: %v, want nil", recorded, err)
		}
	}

	err := schema.CheckBaseline([]int64{1, 75, 76})

	before, ok := errors.AsType[*schema.BeforeBaselineError](err)
	if !ok || !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("check baseline of 1, 75, 76 = %v, want a BeforeBaselineError", err)
	}

	want := "database schema version 76 is older than baseline schema version " +
		strconv.FormatInt(schema.BaselineVersion, 10) + ": " +
		"no binary can upgrade this database. Start the server on an empty database"
	if before.Error() != want {
		t.Fatalf("message = %q, want %q", before.Error(), want)
	}
}

func TestCheckMinimums(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		minimums []string
		binary   string
		want     error
		wantMin  string
	}{
		{name: "none", minimums: nil, binary: "v0.1.0", want: nil, wantMin: ""},
		{name: "empty entries", minimums: []string{"", ""}, binary: "v0.1.0", want: nil, wantMin: ""},
		{name: "at the highest", minimums: []string{"v0.2.0", "v0.5.0"}, binary: "v0.5.0", want: nil, wantMin: ""},
		{
			name: "below the highest", minimums: []string{"v0.5.0", "v0.2.0"}, binary: "v0.4.0",
			want: schema.ErrBinaryBelowMinimum, wantMin: "v0.5.0",
		},
		{
			name: "numeric order, not text order", minimums: []string{"v0.9.0", "v0.10.0"}, binary: "v0.9.5",
			want: schema.ErrBinaryBelowMinimum, wantMin: "v0.10.0",
		},
		{name: "dev passes", minimums: []string{"v9.0.0"}, binary: "dev", want: nil, wantMin: ""},
		{name: "bad minimum", minimums: []string{"latest"}, binary: "v1.0.0", want: schema.ErrBadMinimum, wantMin: ""},
		{
			name:     "bad binary",
			minimums: []string{"v0.1.0"},
			binary:   "1.x",
			want:     schema.ErrBadBinaryVersion,
			wantMin:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := schema.CheckMinimums(tc.minimums, tc.binary)
			if !errors.Is(err, tc.want) {
				t.Fatalf("CheckMinimums(%v, %q) = %v, want %v", tc.minimums, tc.binary, err, tc.want)
			}

			if tc.wantMin == "" {
				return
			}

			below, ok := errors.AsType[*schema.MinimumError](err)
			if !ok || below.SchemaMinimum != tc.wantMin || below.BinaryVersion != tc.binary {
				t.Fatalf("error = %+v, want a MinimumError that names %s and %s", below, tc.wantMin, tc.binary)
			}
		})
	}
}

func TestInstancesBelowMinimumErrorNamesTheRows(t *testing.T) {
	t.Parallel()

	err := error(&schema.InstancesBelowMinimumError{
		Migration: "0200_contract.sql", Minimum: "v0.5.0",
		Instances: []schema.InstanceRow{{ID: "i-1", BinaryVersion: "v0.4.0"}, {ID: "i-2", BinaryVersion: "v0.3.0"}},
	})

	if !errors.Is(err, schema.ErrInstancesBelowMinimum) {
		t.Fatalf("errors.Is(%v, ErrInstancesBelowMinimum) = false, want true", err)
	}

	for _, want := range []string{"0200_contract.sql", "v0.5.0", "i-1 (v0.4.0)", "i-2 (v0.3.0)", "DELETE /api/server/instances"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}
