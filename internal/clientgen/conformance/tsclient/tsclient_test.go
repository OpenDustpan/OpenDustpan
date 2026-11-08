package tsclient_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance/tsclient"
)

// descriptionPath is the published description, from this package.
const descriptionPath = "../../../api/apidesc/openapi.json"

func TestGenerateIsDeterministicAndFollowsTheDescription(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(descriptionPath)
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	opts := tsclient.Options{Description: raw, ServerRelease: "test", Dir: ""}

	first, version, err := tsclient.Generate(opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	second, _, err := tsclient.Generate(opts)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("%d and %d files", len(first), len(second))
	}

	for i := range first {
		if first[i].Path != second[i].Path || !bytes.Equal(first[i].Content, second[i].Content) {
			t.Errorf("%s differs between two runs", first[i].Path)
		}

		if first[i].Path == "package.json" && !strings.Contains(string(first[i].Content), `"version": "`+version+`"`) {
			t.Errorf("package.json does not hold the description version %s", version)
		}
	}
}

// TestBuildTypeListClient builds the package of a description that has a type
// list, compiles it strictly, and runs a program against the compiled package:
// each JSON type of the list comes back from the validator, and every other
// type is refused.
func TestBuildTypeListClient(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("tsc"); err != nil {
		t.Skip("tsc is not on the PATH")
	}

	raw, err := os.ReadFile("../testdata/typelist.json")
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	dir := t.TempDir()

	built, err := tsclient.Build(t.Context(), tsclient.Options{Description: raw, ServerRelease: "test", Dir: dir})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	check := filepath.Join(dir, "typelist-check.mjs")
	if err = os.WriteFile(check, []byte(typeListCheck), 0o600); err != nil {
		t.Fatalf("write the check: %v", err)
	}

	cmd := exec.CommandContext(t.Context(), built.Command, check) //nolint:gosec // Both names come from this test.
	cmd.Dir = dir
	cmd.Env = tsclient.Environ()

	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		t.Fatalf("run the check: %v\n%s", runErr, out)
	}

	if built.ClientVersion != "0.1.0" || built.DescriptionVersion != "0.1.0" {
		t.Errorf("versions: %+v", built)
	}
}

// typeListCheck is a program that runs against the compiled package of the
// type list description. It checks the validator of a type list: each JSON
// type of the list comes back, and every other type is refused. It checks the
// list with integer too: a number comes back only when it holds an integer.
const typeListCheck = `import { decodeWidget } from "./dist/index.js";

for (const value of ["text", 1, 1.5, true]) {
  const widget = decodeWidget({ value, state: "open" }, "$");
  if (widget.value !== value) {
    fail("decodeWidget gave " + JSON.stringify(widget.value) + " for " + JSON.stringify(value));
  }
}

const withInteger = decodeWidget({ value: "text", count: 2, amount: 1.5, state: "open" }, "$");
if (withInteger.count !== 2) {
  fail("decodeWidget gave " + JSON.stringify(withInteger.count) + " for the list with integer");
}
if (withInteger.amount !== 1.5) {
  fail("decodeWidget gave " + JSON.stringify(withInteger.amount) + " for the list with number");
}

let refusedFraction = false;
try {
  decodeWidget({ value: "text", count: 1.5, state: "open" }, "$");
} catch {
  refusedFraction = true;
}
if (!refusedFraction) {
  fail("decodeWidget accepted 1.5 for the list with integer");
}

for (const value of [null, {}, []]) {
  let refused = false;
  try {
    decodeWidget({ value, state: "open" }, "$");
  } catch {
    refused = true;
  }
  if (!refused) {
    fail("decodeWidget accepted " + JSON.stringify(value));
  }
}

function fail(text) {
  process.stderr.write(text + "\n");
  process.exit(1);
}
`

func TestBuildRefusesADescriptionOutsideTheSubset(t *testing.T) {
	t.Parallel()

	_, err := tsclient.Build(t.Context(), tsclient.Options{
		Description: []byte(`{"openapi": "3.1.0"}`), ServerRelease: "", Dir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("want an error")
	}
}

// TestBuildSyntheticClient builds a client for a description that has what the
// published one has not yet: query parameters, bodies of each media type, a
// no-content answer, lists and maps as answers, open and closed enums,
// nullable members, a reserved word as a member name and text that tries to
// end a comment. The TypeScript compiler checks the output, strictly, with the
// hand-written adapter beside it.
func TestBuildSyntheticClient(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("tsc"); err != nil {
		t.Skip("tsc is not on the PATH")
	}

	raw, err := os.ReadFile("../../typescript/testdata/synthetic.json")
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "client")

	built, err := tsclient.Build(t.Context(), tsclient.Options{Description: raw, ServerRelease: "test", Dir: dir})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if built.ClientVersion != "0.3.0" || built.DescriptionVersion != "0.3.0" {
		t.Errorf("versions: %+v", built)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "dist", "index.js")); statErr != nil {
		t.Errorf("the package is not compiled: %v", statErr)
	}
}
