package goclient_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/internal/clientgen"
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

	wantVersion, err := clientgen.ClientVersion(first.DescriptionVersion, 0)
	if err != nil {
		t.Fatalf("client version: %v", err)
	}

	if first.ClientVersion != wantVersion {
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

// TestBuildTypeListClient builds a client for a description that has a type
// list, and runs a program against it: each JSON type of the list comes back
// from the named type, and every other type is refused.
func TestBuildTypeListClient(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../testdata/typelist.json")
	if err != nil {
		t.Fatalf("read the description: %v", err)
	}

	dir := t.TempDir()

	built, err := goclient.Build(t.Context(), goclient.Options{
		Description: raw, Operations: nil, ServerRelease: "test", Dir: dir,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	check := filepath.Join(dir, "cmd", "typelist-check", "main.go")
	if err = os.MkdirAll(filepath.Dir(check), 0o750); err != nil {
		t.Fatalf("make the directory of the check: %v", err)
	}

	if err = os.WriteFile(check, []byte(typeListCheck), 0o600); err != nil {
		t.Fatalf("write the check: %v", err)
	}

	if _, err = goclient.Run(t.Context(), dir, "run", "./cmd/typelist-check"); err != nil {
		t.Fatalf("run the check: %v", err)
	}

	if built.ClientVersion != "0.1.0" || built.DescriptionVersion != "0.1.0" {
		t.Errorf("versions: %+v", built)
	}
}

// typeListCheck is a program that runs against the client of the type list
// description. It checks the named type of a type list: each JSON type of the
// list comes back from the value and from the bytes, and every other type is
// refused. It checks the list with integer too: a number comes back only when
// it holds an integer.
const typeListCheck = `package main

import (
	"encoding/json"
	"fmt"
	"os"

	client "opendustpan.org/client"
)

func main() {
	for _, in := range []string{"\"text\"", "1", "1.5", "true"} {
		var v client.WidgetValue

		if err := json.Unmarshal([]byte(in), &v); err != nil {
			fail("unmarshal " + in + ": " + err.Error())
		}

		out, err := json.Marshal(v)
		if err != nil {
			fail("marshal " + in + ": " + err.Error())
		}

		if string(out) != in {
			fail(in + " came back as " + string(out))
		}
	}

	for _, in := range []string{"2", "1e2", "\"text\""} {
		var v client.WidgetCount

		if err := json.Unmarshal([]byte(in), &v); err != nil {
			fail("unmarshal " + in + " into the list with integer: " + err.Error())
		}
	}

	for _, in := range []string{"1.5", "1e-2"} {
		var v client.WidgetCount

		if err := json.Unmarshal([]byte(in), &v); err == nil {
			fail("unmarshal " + in + " into the list with integer: want an error")
		}
	}

	var amount client.WidgetAmount

	if err := json.Unmarshal([]byte("1.5"), &amount); err != nil {
		fail("unmarshal 1.5 into the list with number: " + err.Error())
	}

	for _, in := range []string{"null", "{}", "[]"} {
		var v client.WidgetValue

		if err := json.Unmarshal([]byte(in), &v); err == nil {
			fail("unmarshal " + in + ": want an error")
		}
	}
}

func fail(text string) {
	fmt.Fprintln(os.Stderr, text)
	os.Exit(1)
}
`

func TestBuildRefusesADescriptionWithAConstructOutsideTheSubset(t *testing.T) {
	t.Parallel()

	_, err := goclient.Build(t.Context(), goclient.Options{
		Description: []byte(`{"openapi": "3.1.0"}`), Operations: nil, ServerRelease: "", Dir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("want an error")
	}
}
