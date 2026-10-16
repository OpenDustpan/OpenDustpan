package schema_test

import (
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/schema"
)

func TestCheckMinimum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		minimum string
		binary  string
		refuse  bool
	}{
		{name: "no minimum", minimum: "", binary: "v0.1.0", refuse: false},
		{name: "equal", minimum: "v0.2.0", binary: "v0.2.0", refuse: false},
		{name: "above", minimum: "v0.2.0", binary: "v0.10.0", refuse: false},
		{name: "major above", minimum: "v0.9.9", binary: "v1.0.0", refuse: false},
		{name: "below patch", minimum: "v0.2.1", binary: "v0.2.0", refuse: true},
		{name: "below minor", minimum: "v0.10.0", binary: "v0.9.0", refuse: true},
		{name: "below major", minimum: "v2.0.0", binary: "v1.9.9", refuse: true},
		{
			name:    "describe suffix counts as its tag",
			minimum: "v0.2.0",
			binary:  "v0.2.0-3-gabc1234-dirty",
			refuse:  false,
		},
		{name: "describe suffix below", minimum: "v0.3.0", binary: "v0.2.0-3-gabc1234", refuse: true},
		{name: "dev build", minimum: "v9.0.0", binary: "dev", refuse: false},
		{name: "empty build", minimum: "v9.0.0", binary: "", refuse: false},
		{name: "commit hash build", minimum: "v9.0.0", binary: "c69fb92-dirty", refuse: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := schema.CheckMinimum(tc.minimum, tc.binary)
			if tc.refuse != errors.Is(err, schema.ErrBinaryBelowMinimum) {
				t.Fatalf("CheckMinimum(%q, %q) = %v, want refusal %v", tc.minimum, tc.binary, err, tc.refuse)
			}

			if !tc.refuse && err != nil {
				t.Fatalf("CheckMinimum(%q, %q) = %v, want nil", tc.minimum, tc.binary, err)
			}
		})
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

	for _, v := range []string{"", "dev", "c69fb92", "c69fb92-dirty", "v1", "1.2.3"} {
		if !schema.IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false, want true", v)
		}
	}

	for _, v := range []string{"v0.1.0", "v1.2.3-rc.1", "v0.1.0-3-gabc1234-dirty"} {
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
