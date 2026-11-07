package render_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"os"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func testdata(tb testing.TB, name string) []byte {
	tb.Helper()

	data, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		tb.Fatal(err)
	}

	return data
}

func golden(tb testing.TB, name string) string {
	tb.Helper()

	return string(testdata(tb, name))
}

func testNamespace(tb testing.TB) namespace.Name {
	tb.Helper()

	ns, err := namespace.ParseName("payments")
	if err != nil {
		tb.Fatal(err)
	}

	return ns
}

// unit returns the unit reference of tool and name, with the unit ID id
// when id is not empty.
func unit(tb testing.TB, tool, name, id string) identity.Object {
	tb.Helper()

	o, err := identity.UnitObject(tool, testNamespace(tb), name)
	if err != nil {
		tb.Fatal(err)
	}

	if id != "" {
		o = o.WithUnitID(id)
	}

	return o
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// str returns s as a JSON string.
func str(tb testing.TB, s string) json.RawMessage {
	tb.Helper()

	out, err := json.Marshal(s)
	if err != nil {
		tb.Fatal(err)
	}

	return out
}

// loadInput reads the blueprint and the request of fixture ("tofu" or
// "pulumi") through the parsers of the package selfservice, and returns
// the input of a render for the unit of tool and name.
func loadInput(tb testing.TB, fixture, tool, name string) selfservice.RenderInput {
	tb.Helper()

	bp, err := selfservice.ParseBlueprint(testdata(tb, fixture+".blueprint.json"))
	if err != nil {
		tb.Fatal(err)
	}

	doc, err := selfservice.ParseDocument(testdata(tb, fixture+".request.json"))
	if err != nil {
		tb.Fatal(err)
	}

	var v selfservice.Version

	v.Namespace = testNamespace(tb)
	v.Number = doc.Blueprint.Version
	v.Blueprint = bp

	return selfservice.RenderInput{Version: &v, Document: &doc, Unit: unit(tb, tool, name, "")}
}

func tofuInput(tb testing.TB) selfservice.RenderInput {
	tb.Helper()

	return loadInput(tb, "tofu", selfservice.ToolOpenTofu, "orders-db")
}

func pulumiInput(tb testing.TB) selfservice.RenderInput {
	tb.Helper()

	return loadInput(tb, "pulumi", selfservice.ToolPulumi, "orders-site")
}

// variant returns a copy of in that edit changes. The copy shares no map
// and no slice with in.
func variant(in selfservice.RenderInput, edit func(*selfservice.RenderInput)) selfservice.RenderInput {
	v := *in.Version
	d := *in.Document

	v.Blueprint.Inputs = slices.Clone(in.Version.Blueprint.Inputs)
	v.Blueprint.Platform.Values = maps.Clone(in.Version.Blueprint.Platform.Values)
	d.Inputs = maps.Clone(in.Document.Inputs)

	out := selfservice.RenderInput{Version: &v, Document: &d, Unit: in.Unit}
	edit(&out)

	return out
}

func renderFiles(tb testing.TB, r selfservice.Renderer, in selfservice.RenderInput) map[string]string {
	tb.Helper()

	got, err := r.Render(tb.Context(), in)
	if err != nil {
		tb.Fatalf("Render: %v", err)
	}

	out := make(map[string]string, len(got.Files))
	for _, f := range got.Files {
		out[f.Path] = string(f.Content)
	}

	return out
}

func answerOf(tb testing.TB, r json.RawMessage) any {
	tb.Helper()

	var v any
	if err := json.Unmarshal(r, &v); err != nil {
		tb.Fatal(err)
	}

	return v
}
