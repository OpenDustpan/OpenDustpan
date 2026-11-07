package render_test

import (
	"encoding/json"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservice/render"
)

// The seeds below use byte escapes only, so that no tool turns an escape
// into a raw character.
const (
	seedLineSeparators = "\xe2\x80\xa8\xe2\x80\xa9\xc2\x85" // U+2028, U+2029, U+0085
	seedReplacement    = "\xef\xbf\xbd"                     // U+FFFD
	seedByteOrderMark  = "\xef\xbb\xbf"                     // U+FEFF
	seedNoncharacterFE = "\xef\xbf\xbe"                     // U+FFFE
	seedNoncharacterFF = "\xef\xbf\xbf"                     // U+FFFF
	seedEmoji          = "\xf0\x9f\x98\x80"                 // U+1F600
	seedLoneSurrogate  = "\xed\xa0\x80"                     // an encoded surrogate, not valid UTF-8
)

// FuzzTofuValuePath checks that any string answer gives a variables file
// that decodes back to it, and a program text that does not depend on it.
func FuzzTofuValuePath(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "}\nresource \"x\" \"y\" {}", "${x}", "%{x}", "\"", "\\", seedLineSeparators, "\xc3\xa9\x00",
		"\xff\xfe", seedNoncharacterFE,
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

// FuzzPulumiValuePath checks the escaper of the stack settings, which is
// the one place where the render writes escapes by hand. For any string
// answer, Pulumi.yaml does not change, and the stack settings are ASCII,
// decode with a YAML parser to the answer, and hold no alias, no anchor and
// no tag outside the plain data tags. An answer that the render refuses
// with ErrInvalid (a noncharacter) is skipped; any other error fails.
func FuzzPulumiValuePath(f *testing.F) {
	for _, seed := range []string{
		"", "\n", "plain", "\x7f", "\x00", "\x05", seedEmoji, seedReplacement, seedByteOrderMark, seedLineSeparators,
		seedLoneSurrogate, "\xff\xfe", seedNoncharacterFE, seedNoncharacterFF, "&a *a !!x ---",
		"!!python/object:os.system", "&a [*a]", "}\nresource \"null_resource\" \"x\" {}\n", "${evil}", "\" # \\",
		"a: b\nc: d", "---\n...\n", "yes", "~",
	} {
		f.Add(seed)
	}

	r := render.NewPulumi()
	base := pulumiInput(f)
	plain := renderFiles(f, r, base)

	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > selfservice.MaxStringAnswer/6 {
			t.Skip("over the string limit after escapes")
		}

		answer := str(t, s)

		got, err := r.Render(t.Context(), variant(base, func(in *selfservice.RenderInput) {
			in.Document.Inputs["size"] = answer
		}))
		if errors.Is(err, selfservice.ErrInvalid) {
			t.Skip("the render refuses this answer")
		}

		if err != nil {
			t.Fatalf("Render: %v", err)
		}

		files := make(map[string]string, len(got.Files))
		for _, file := range got.Files {
			files[file.Path] = string(file.Content)
		}

		if files["Pulumi.yaml"] != plain["Pulumi.yaml"] {
			t.Fatalf("the program text changed with the answer %q", s)
		}

		checkStackSettings(t, files["Pulumi.orders-site.yaml"], answerOf(t, answer))
	})
}
