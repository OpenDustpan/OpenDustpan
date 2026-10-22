package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/cli"
)

// descriptionPath is the published description, from this package.
const descriptionPath = "../../api/apidesc/openapi.json"

// packagePath is the checked-in TypeScript package, from this package.
const packagePath = "../../../clients/typescript"

const syntheticPath = "../typescript/testdata/synthetic.json"

//nolint:revive // The three results have different meanings, and every caller names them.
func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()

	var out, errs bytes.Buffer

	code := cli.Run(args, &out, &errs)

	return code, out.String(), errs.String()
}

// TestCheckedInPackageIsWhatARunGives fails when someone changes the
// description, a template or a runtime file and does not run make
// clients-generate.
func TestCheckedInPackageIsWhatARunGives(t *testing.T) {
	t.Parallel()

	code, _, stderr := run(t, "-target", "typescript", "-description", descriptionPath, "-out", packagePath, "-check")
	if code != cli.ExitOK {
		t.Errorf("the checked-in package is stale (exit %d):\n%s", code, stderr)
	}
}

// readFile reads a file that the test wrote.
func readFile(t *testing.T, dir string, name ...string) string {
	t.Helper()

	raw, err := os.ReadFile(
		filepath.Join(append([]string{dir}, name...)...),
	)
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestWriteThenCheck(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "client")
	args := []string{"-target", "typescript", "-description", syntheticPath, "-out", dir}

	if code, stdout, stderr := run(t, args...); code != cli.ExitOK || !strings.Contains(stdout, "wrote 13 files") {
		t.Fatalf("write: exit %d\n%s\n%s", code, stdout, stderr)
	}

	if code, _, stderr := run(t, append(args, "-check")...); code != cli.ExitOK {
		t.Fatalf("check after write: exit %d\n%s", code, stderr)
	}

	if err := os.WriteFile(filepath.Join(dir, "src", "metadata.ts"), []byte("// by hand\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A file that lost its header is not generated, so the next write refuses it.
	if code, _, stderr := run(t, args...); code != cli.ExitFailed || !strings.Contains(stderr, "no generated header") {
		t.Errorf("write over a file by hand: exit %d\n%s", code, stderr)
	}
}

func TestCheckListsTheDifferences(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	args := []string{"-target", "typescript", "-description", syntheticPath, "-out", dir}

	if code, _, stderr := run(t, args...); code != cli.ExitOK {
		t.Fatalf("write: exit %d\n%s", code, stderr)
	}

	if err := os.Remove(filepath.Join(dir, "package.json")); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := run(t, append(args, "-check")...)
	if code != cli.ExitFailed || !strings.Contains(stderr, "missing: package.json") ||
		!strings.Contains(stderr, "make clients-generate") {
		t.Errorf("check: exit %d\n%s", code, stderr)
	}
}

func TestCheckWritesNothing(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "none")

	code, _, _ := run(t, "-target", "typescript", "-description", syntheticPath, "-out", dir, "-check")
	if code != cli.ExitFailed {
		t.Errorf("want exit %d, got %d", cli.ExitFailed, code)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("-check made the directory: %v", err)
	}
}

func TestClientVersionDefaultsToTheDescriptionVersion(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	args := []string{"-target", "typescript", "-description", syntheticPath, "-out", dir}
	if code, _, stderr := run(t, args...); code != cli.ExitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}

	if raw := readFile(t, dir, "package.json"); !strings.Contains(raw, `"version": "0.3.0"`) {
		t.Errorf("package.json:\n%s", raw)
	}

	dir2 := t.TempDir()

	code, _, _ := run(
		t,
		"-target",
		"typescript",
		"-description",
		syntheticPath,
		"-out",
		dir2,
		"-client-version",
		"0.3.7",
	)
	if code != cli.ExitOK {
		t.Fatalf("a patch of its own: exit %d", code)
	}

	if raw := readFile(t, dir2, "package.json"); !strings.Contains(raw, `"version": "0.3.7"`) {
		t.Errorf("package.json:\n%s", raw)
	}
}

func TestMetadataHoldsTheDigestOfTheDescription(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	code, _, stderr := run(
		t,
		"-target",
		"typescript",
		"-description",
		syntheticPath,
		"-out",
		dir,
		"-server-release",
		"abc123",
	)
	if code != cli.ExitOK {
		t.Fatalf("exit %d\n%s", code, stderr)
	}

	raw := readFile(t, dir, "src", "metadata.ts")
	if !strings.Contains(raw, `serverRelease: "abc123"`) || !strings.Contains(raw, "descriptionSha256: \"") {
		t.Errorf("metadata.ts:\n%s", raw)
	}
}

func TestRefusals(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	huge := filepath.Join(t.TempDir(), "huge.json")

	if err := os.WriteFile(huge, bytes.Repeat([]byte(" "), 9<<20), 0o600); err != nil {
		t.Fatal(err)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"openapi": "3.1.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"no argument", nil, cli.ExitUsage, "required"},
		{"unknown flag", []string{"-nope"}, cli.ExitUsage, ""},
		{"no target", []string{"-description", syntheticPath, "-out", out}, cli.ExitUsage, "required"},
		{
			"a stray argument",
			[]string{"-target", "typescript", "-description", syntheticPath, "-out", out, "x"},
			cli.ExitUsage,
			"required",
		},
		{
			"unknown target",
			[]string{"-target", "cobol", "-description", syntheticPath, "-out", out},
			cli.ExitUsage,
			"unknown target",
		},
		{
			"missing description",
			[]string{"-target", "typescript", "-description", filepath.Join(out, "none.json"), "-out", out},
			cli.ExitFailed,
			"open the description",
		},
		{
			"description over the limit",
			[]string{"-target", "typescript", "-description", huge, "-out", out},
			cli.ExitFailed,
			"longer than the limit",
		},
		{
			"description outside the subset",
			[]string{"-target", "typescript", "-description", bad, "-out", out},
			cli.ExitFailed,
			"read the description",
		},
		{
			"client version of another minor",
			[]string{"-target", "typescript", "-description", syntheticPath, "-out", out, "-client-version", "0.9.0"},
			cli.ExitFailed,
			"client version",
		},
		{
			"package without a scope",
			[]string{"-target", "typescript", "-description", syntheticPath, "-out", out, "-package", "client"},
			cli.ExitFailed,
			"scoped npm name",
		},
		{
			"release with a quote",
			[]string{"-target", "typescript", "-description", syntheticPath, "-out", out, "-server-release", `a"b`},
			cli.ExitFailed,
			"server release",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			code, _, stderr := run(t, tc.args...)
			if code != tc.wantCode || !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("want exit %d with %q, got exit %d:\n%s", tc.wantCode, tc.wantErr, code, stderr)
			}
		})
	}
}

func TestHelpExitsWithZero(t *testing.T) {
	t.Parallel()

	if code, _, stderr := run(t, "-h"); code != cli.ExitOK || !strings.Contains(stderr, "-target") {
		t.Errorf("exit %d\n%s", code, stderr)
	}
}
