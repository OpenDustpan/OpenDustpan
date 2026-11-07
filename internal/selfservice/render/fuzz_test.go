package render_test

import (
	"encoding/json"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/render"
)

// FuzzTofuValuePath checks that any string answer gives a variables file
// that decodes back to it, and a program text that does not depend on it.
func FuzzTofuValuePath(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "}\nresource \"x\" \"y\" {}", "${x}", "%{x}", "\"", "\\", " ", "é\x00", "\xff\xfe", "￾",
	} {
		f.Add(seed)
	}

	r := render.NewTofu()
	base := tofuInput(f)
	plain := renderFiles(f, r, base)

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > selfservice.MaxStringAnswer/6 {
			t.Skip("over the string limit after escapes")
		}

		answer := str(t, s)

		files := renderFiles(
			t,
			r,
			variant(base, func(in *selfservice.RenderInput) { in.Document.Inputs["size"] = answer }),
		)
		if files["main.tf"] != plain["main.tf"] {
			t.Fatalf("the program text changed with the answer %q", s)
		}

		var values map[string]any
		if err := json.Unmarshal([]byte(files["terraform.tfvars.json"]), &values); err != nil {
			t.Fatalf("the variables file is not JSON: %v", err)
		}

		if got, want := values["instance_size"], answerOf(t, answer); got != want {
			t.Fatalf("instance_size = %#v, want %#v", got, want)
		}
	})
}
