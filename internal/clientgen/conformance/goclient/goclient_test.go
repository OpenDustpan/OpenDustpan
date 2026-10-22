package goclient_test

import (
	"bytes"
	"os"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen/conformance/goclient"
)

// descriptionPath is the published description, from this package.
const descriptionPath = "../../../api/apidesc/openapi.json"

func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(descriptionPath)
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	opts := goclient.Options{
		Description: raw, ServerRelease: "test", Dir: "",
		Operations: []string{"getCapabilities", "createProfile", "getNotificationSink"},
	}

	first, err := goclient.Generate(opts)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	opts.Operations = []string{"createProfile", "getNotificationSink", "getCapabilities", "getCapabilities"}

	second, err := goclient.Generate(opts)
	if err != nil {
		t.Fatalf("generate again: %v", err)
	}

	if first.ClientVersion != "0.2.0" {
		t.Errorf("the client takes the major and minor version of the description: %s", first.ClientVersion)
	}

	if len(first.Files) != len(second.Files) {
		t.Fatalf("%d and %d files", len(first.Files), len(second.Files))
	}

	for i := range first.Files {
		a, b := first.Files[i], second.Files[i]
		if a.Path != b.Path || !bytes.Equal(a.Content, b.Content) {
			t.Errorf("%s differs: the order of the operation IDs must not change the output", a.Path)
		}
	}
}

// TestBuildSyntheticClient builds a client for a description that has what the
// published one has not yet: query parameters, a list that pages, a body of
// each media type, a no-content answer, lists and maps as answers, open and
// closed enums, nullable members and text that tries to add code. The go tool
// compiles and vets the output, with the hand-written transport.
func TestBuildSyntheticClient(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/synthetic.json")
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	built, err := goclient.Build(t.Context(), goclient.Options{
		Description: raw, Operations: nil, ServerRelease: "test", Dir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if built.ClientVersion != "0.3.0" || built.DescriptionVersion != "0.3.0" {
		t.Errorf("versions: %+v", built)
	}

	if _, statErr := os.Stat(built.Adapter); statErr != nil {
		t.Errorf("the adapter is not built: %v", statErr)
	}
}

func TestBuildRefusesADescriptionWithAConstructOutsideTheSubset(t *testing.T) {
	t.Parallel()

	_, err := goclient.Build(t.Context(), goclient.Options{
		Description: []byte(`{"openapi": "3.1.0"}`), Operations: nil, ServerRelease: "", Dir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("want an error")
	}
}
