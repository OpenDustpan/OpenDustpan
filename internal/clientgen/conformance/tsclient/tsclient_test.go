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
