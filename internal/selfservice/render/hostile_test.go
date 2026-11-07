package render_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/render"
	"opendustpan.org/opendustpan/pkg/identity"
)

// hostileAnswers are answers that, as program text, would change the
// program. As data, they must change nothing.
func hostileAnswers(tb testing.TB) []json.RawMessage {
	tb.Helper()

	values := []string{
		"}\nresource \"null_resource\" \"x\" {}\n",
		"!!python/object:os.system",
		"&a [*a]",
		"*a",
		"${evil}",
		"%{ if true }x%{ endif }",
		"a: b\nc: d",
		"- x\n- y",
		"\" # \\",
		"---\n...\n",
		// U+2028, U+2029, U+0085, DEL, U+1F600, a visible letter, NUL and ESC.
		"\xe2\x80\xa8\xe2\x80\xa9\xc2\x85\x7f\xf0\x9f\x98\x80\xc3\xa9\x00\x1b",
		"",
		"null",
		"~",
		"yes",
		"0x1F",
	}

	out := make([]json.RawMessage, 0, len(values))
	for _, v := range values {
		out = append(out, str(tb, v))
	}

	return out
}

// TestTofuAnswersStayData is the encoding layer of OpenTofu: a hostile
// answer leaves the program text as it is with a plain answer, and the
// variables file reads back to the answer.
func TestTofuAnswersStayData(t *testing.T) {
	t.Parallel()

	r := render.NewTofu()
	base := tofuInput(t)
	plain := renderFiles(
		t,
		r,
		variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = raw(`"plain"`) }),
	)

	for _, answer := range hostileAnswers(t) {
		t.Run(string(answer), func(t *testing.T) {
			t.Parallel()

			files := renderFiles(
				t,
				r,
				variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = answer }),
			)
			if files["main.tf"] != plain["main.tf"] {
				t.Fatalf("the program text changed with the answer:\n%s", files["main.tf"])
			}

			var values map[string]any
			if err := json.Unmarshal([]byte(files["terraform.tfvars.json"]), &values); err != nil {
				t.Fatalf("the variables file is not JSON: %v", err)
			}

			if got, want := values["instance_size"], answerOf(t, answer); got != want {
				t.Fatalf("instance_size = %#v, want %#v", got, want)
			}

			if len(values) != 4 {
				t.Fatalf("the variables file has %d keys, want 4", len(values))
			}
		})
	}
}

// TestPulumiAnswersStayData is the encoding layer of Pulumi: a hostile
// answer leaves the program text as it is with a plain answer, and the
// stack settings read back to the answer, with no anchor and no tag.
func TestPulumiAnswersStayData(t *testing.T) {
	t.Parallel()

	r := render.NewPulumi()
	base := pulumiInput(t)
	plain := renderFiles(
		t,
		r,
		variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = raw(`"plain"`) }),
	)

	for _, answer := range hostileAnswers(t) {
		t.Run(string(answer), func(t *testing.T) {
			t.Parallel()

			files := renderFiles(
				t,
				r,
				variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = answer }),
			)
			if files["Pulumi.yaml"] != plain["Pulumi.yaml"] {
				t.Fatalf("the program text changed with the answer:\n%s", files["Pulumi.yaml"])
			}

			checkStackSettings(t, files["Pulumi.orders-site.yaml"], answerOf(t, answer))
		})
	}
}

// checkStackSettings checks that settings is ASCII YAML with four config
// entries, that webapp:size reads back to want, and that no node is an
// alias, an anchor or a tagged value of a type that is not plain data.
func checkStackSettings(t *testing.T, settings string, want any) {
	t.Helper()

	for i := range len(settings) {
		if c := settings[i]; c >= 0x7f || (c < 0x20 && c != '\n') {
			t.Fatalf("the stack settings are not plain ASCII: %q", settings)
		}
	}

	var doc struct {
		Config map[string]any `yaml:"config"`
	}
	if err := yaml.Unmarshal([]byte(settings), &doc); err != nil {
		t.Fatalf("the stack settings are not YAML: %v\n%s", err, settings)
	}

	if got := doc.Config["webapp:size"]; got != want {
		t.Fatalf("webapp:size = %#v, want %#v", got, want)
	}

	if len(doc.Config) != 4 {
		t.Fatalf("the stack settings have %d keys, want 4", len(doc.Config))
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(settings), &node); err != nil {
		t.Fatal(err)
	}

	checkNoYAMLMagic(t, &node)
}

func checkNoYAMLMagic(t *testing.T, n *yaml.Node) {
	t.Helper()

	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		t.Fatalf("the stack settings hold an alias or an anchor at line %d", n.Line)
	}

	switch n.Tag {
	case "", "!!map", "!!seq", "!!str", "!!int", "!!float", "!!bool":
	default:
		t.Fatalf("the stack settings hold the tag %q at line %d", n.Tag, n.Line)
	}

	for _, c := range n.Content {
		checkNoYAMLMagic(t, c)
	}
}

// limitInputs returns inputs that are over a limit of the port.
func limitInputs(tb testing.TB, base selfservice.RenderInput) map[string]selfservice.RenderInput {
	tb.Helper()

	return map[string]selfservice.RenderInput{
		"10,000 answers": variant(base, func(in *selfservice.RenderInput) {
			for i := range 10000 {
				in.Document.Inputs[fmt.Sprintf("k%d", i)] = raw(`"v"`)
			}
		}),
		"string over the limit": variant(base, func(in *selfservice.RenderInput) {
			in.Document.Inputs["size"] = str(tb, strings.Repeat("x", selfservice.MaxStringAnswer+1))
		}),
		"platform values over the limit": variant(base, func(in *selfservice.RenderInput) {
			for i := range selfservice.MaxStaticValues + 1 {
				in.Version.Blueprint.Platform.Values[fmt.Sprintf("k%d", i)] = raw(`1`)
			}
		}),
		"inputs over the limit": variant(base, func(in *selfservice.RenderInput) {
			in.Version.Blueprint.Inputs = make([]selfservice.Input, selfservice.MaxInputs+1)
		}),
	}
}

// TestLimits is the limit layer: an input over a limit is refused with
// ErrLimit and no file.
func TestLimits(t *testing.T) {
	t.Parallel()

	tools := map[string]struct {
		renderer selfservice.Renderer
		base     selfservice.RenderInput
	}{
		"opentofu": {renderer: render.NewTofu(), base: tofuInput(t)},
		"pulumi":   {renderer: render.NewPulumi(), base: pulumiInput(t)},
	}

	for tool, tc := range tools {
		for what, in := range limitInputs(t, tc.base) {
			t.Run(tool+"/"+what, func(t *testing.T) {
				t.Parallel()

				got, err := tc.renderer.Render(t.Context(), in)
				if !errors.Is(err, selfservice.ErrLimit) {
					t.Fatalf("Render error = %v, want ErrLimit", err)
				}

				if len(got.Files) != 0 {
					t.Fatalf("a refused render gave %d files", len(got.Files))
				}
			})
		}
	}
}

// TestLimitComesFirst checks that the limit is the first check that looks
// at the maps. Each value of the input is one that a later step refuses with
// ErrInvalid, so a render that read one value before the limit check would
// return ErrInvalid. A render that returns ErrLimit read no value, so a
// hostile document costs the render a length check and no memory.
func TestLimitComesFirst(t *testing.T) {
	t.Parallel()

	for tool, tc := range map[string]struct {
		renderer selfservice.Renderer
		base     selfservice.RenderInput
	}{
		"opentofu": {renderer: render.NewTofu(), base: tofuInput(t)},
		"pulumi":   {renderer: render.NewPulumi(), base: pulumiInput(t)},
	} {
		in := variant(tc.base, func(in *selfservice.RenderInput) {
			for i := range 10000 {
				in.Document.Inputs[fmt.Sprintf("not a name %d", i)] = raw(`{`)
			}

			in.Version.Blueprint.Inputs[0].Maps = `a"; resource "x" "y" {}`
		})

		_, err := tc.renderer.Render(t.Context(), in)
		if !errors.Is(err, selfservice.ErrLimit) {
			t.Errorf("%s: Render error = %v, want ErrLimit", tool, err)
		}
	}
}

// TestPulumiUnitNames sends hostile unit names to the renderer. The name is
// part of the name of the stack settings file.
func TestPulumiUnitNames(t *testing.T) {
	t.Parallel()

	r := render.NewPulumi()
	base := pulumiInput(t)
	names := []string{
		"../x",
		"..",
		"a/b",
		"databases/orders",
		"a\x00b",
		"-a",
		"Upper",
		"a_b",
		"a.b",
		"a b",
		"a\nb",
		"1abc",
		"a\\b",
		"é",
		strings.Repeat("a", selfservice.MaxNameBytes+1),
	}

	tried := 0

	for _, name := range names {
		o, err := identity.UnitObject(selfservice.ToolPulumi, testNamespace(t), name)
		if err != nil {
			continue // The core refuses this name first.
		}

		tried++

		got, err := r.Render(t.Context(), variant(base, func(in *selfservice.RenderInput) { in.Unit = o }))
		if !errors.Is(err, selfservice.ErrInvalid) {
			t.Errorf("unit name %q: error = %v, want ErrInvalid", name, err)
		}

		if len(got.Files) != 0 {
			t.Errorf("unit name %q: a refused render gave %d files", name, len(got.Files))
		}
	}

	if tried < len(names)/2 {
		t.Fatalf("the core refused %d of %d names; the renderer saw too few", len(names)-tried, len(names))
	}
}
