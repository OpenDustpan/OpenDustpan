// Package render holds the renderers of the self-service: one adapter for
// OpenTofu and one for Pulumi, both for the selfservice.Renderer port.
//
// A render turns a blueprint version, a request document and a unit into
// the files that a tool runs. The files have two parts. The program text
// names only keys, and every key passes selfservice.ValidKey before it goes
// into the text. The values (the answers of the requester and the platform
// values of the blueprint) go only into a data file, through a JSON
// encoder, and never into the program text.
//
// The operation of a render comes from the document and the unit, because
// a render gets no record of the last applied revision:
//   - ensure absent gives selfservice.OperationDestroy,
//   - else, a unit with a stable unit ID gives selfservice.OperationUpdate,
//   - else, selfservice.OperationCreate.
//
// A render never gives selfservice.OperationNone: the server compares the
// canonical bytes of two documents for that.
package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// The file names of a render. Each render writes the program first and then
// the data file.
const (
	fileTofuProgram   = "main.tf"
	fileTofuValues    = "terraform.tfvars.json"
	filePulumiProgram = "Pulumi.yaml"
)

// maxFileBytes bounds one file of a render.
const maxFileBytes = 1 << 20

// Bounds of the text that a render copies from the program of a blueprint.
const (
	maxSourceBytes  = 512
	maxVersionBytes = 128
	maxTypeBytes    = 256
)

// setting is one value of a render: a key of the blueprint with the
// canonical JSON of its value.
type setting struct {
	// key is the key as the blueprint writes it.
	key string
	// norm is the key in the form of selfservice.NormalizeKey.
	norm string
	// value is the canonical JSON of the value. It is never null.
	value []byte
	// home names the input or the platform value that sets the key.
	home string
}

// collect checks in and returns the settings of its render, sorted by key.
// It is the shared part of both renderers. It returns ErrInvalid for an
// input that no render can use, ErrLimit for an input above a limit, and
// ErrOneHome for a key that two homes set. It returns no setting with an
// error.
func collect(ctx context.Context, in selfservice.RenderInput, tool string) (*selfservice.Blueprint, []setting, error) {
	b, err := precheck(ctx, in, tool)
	if err != nil {
		return nil, nil, err
	}

	settings, err := gather(b, in.Document)
	if err != nil {
		return nil, nil, err
	}

	if err := checkHomes(settings); err != nil {
		return nil, nil, err
	}

	return b, settings, nil
}

// precheck runs the checks of collect that need no value, and returns the
// blueprint of in.
func precheck(ctx context.Context, in selfservice.RenderInput, tool string) (*selfservice.Blueprint, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	if in.Version == nil || in.Document == nil {
		return nil, fmt.Errorf("%w: a render needs a blueprint version and a document", selfservice.ErrInvalid)
	}

	b := &in.Version.Blueprint
	if b.Tool != tool {
		return nil, fmt.Errorf(
			"%w: the blueprint is for the tool %q, this render is for %q",
			selfservice.ErrInvalid,
			b.Tool,
			tool,
		)
	}

	// The limits come first, before any work on the maps, so that an input
	// of 10,000 keys costs a length check and no more.
	if err := checkLimits(b, in.Document); err != nil {
		return nil, err
	}

	if err := checkKeys(b, tool); err != nil {
		return nil, err
	}

	if err := selfservice.CheckOneHome(b); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	if err := in.Document.Validate(); err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	for _, name := range sortedNames(in.Document.Inputs) {
		if _, ok := b.InputNamed(name); !ok {
			return nil, fmt.Errorf(
				"%w: the document answers %q, which is not an input of the blueprint",
				selfservice.ErrInvalid,
				name,
			)
		}
	}

	return b, nil
}

func checkLimits(b *selfservice.Blueprint, d *selfservice.Document) error {
	switch {
	case len(d.Inputs) > selfservice.MaxAnswers:
		return fmt.Errorf(
			"%w: %d answers, the limit is %d",
			selfservice.ErrLimit,
			len(d.Inputs),
			selfservice.MaxAnswers,
		)
	case len(b.Inputs) > selfservice.MaxInputs:
		return fmt.Errorf("%w: %d inputs, the limit is %d", selfservice.ErrLimit, len(b.Inputs), selfservice.MaxInputs)
	case len(b.Platform.Values) > selfservice.MaxStaticValues:
		return fmt.Errorf(
			"%w: %d platform values, the limit is %d",
			selfservice.ErrLimit,
			len(b.Platform.Values),
			selfservice.MaxStaticValues,
		)
	default:
		return nil
	}
}

// checkKeys refuses each key that selfservice.ValidKey refuses. Defense in
// depth: the publish checks the same keys, and this check stays so that a
// version that did not pass the publish can never put a key into the
// program text. It does not rely on the publish or on the document check.
func checkKeys(b *selfservice.Blueprint, tool string) error {
	for i := range b.Inputs {
		if key := b.Inputs[i].Maps; !selfservice.ValidKey(tool, key) {
			return fmt.Errorf(
				"%w: input %s maps to %q, which is not a key of %s",
				selfservice.ErrInvalid,
				b.Inputs[i].Name,
				key,
				tool,
			)
		}
	}

	for _, key := range sortedNames(b.Platform.Values) {
		if !selfservice.ValidKey(tool, key) {
			return fmt.Errorf("%w: the platform value %q is not a key of %s", selfservice.ErrInvalid, key, tool)
		}
	}

	return nil
}

// gather returns the settings of the answers and the platform values.
func gather(b *selfservice.Blueprint, d *selfservice.Document) ([]setting, error) {
	out := make([]setting, 0, len(b.Inputs)+len(b.Platform.Values))

	for i := range b.Inputs {
		in := &b.Inputs[i]

		raw, ok := d.Inputs[in.Name]
		if !ok {
			raw = in.Default
		}

		if len(raw) == 0 {
			continue
		}

		value, err := canonicalValue(raw)
		if err != nil {
			return nil, fmt.Errorf("input %s: %w", in.Name, err)
		}

		out = append(
			out,
			setting{
				key:   in.Maps,
				norm:  selfservice.NormalizeKey(b, in.Maps),
				value: value,
				home:  "the input " + in.Name,
			},
		)
	}

	for _, key := range sortedNames(b.Platform.Values) {
		value, err := canonicalValue(b.Platform.Values[key])
		if err != nil {
			return nil, fmt.Errorf("platform value %s: %w", key, err)
		}

		out = append(
			out,
			setting{
				key:   key,
				norm:  selfservice.NormalizeKey(b, key),
				value: value,
				home:  fmt.Sprintf("the platform value %q", key),
			},
		)
	}

	slices.SortFunc(out, func(a, c setting) int { return strings.Compare(a.key, c.key) })

	return out, nil
}

// canonicalValue returns the canonical JSON of raw. It refuses a value
// over the size of a blueprint document, a value that is not JSON and null.
func canonicalValue(raw json.RawMessage) ([]byte, error) {
	if len(raw) > selfservice.MaxBlueprintBytes {
		return nil, fmt.Errorf(
			"%w: a value of %d bytes, the limit is %d",
			selfservice.ErrLimit,
			len(raw),
			selfservice.MaxBlueprintBytes,
		)
	}

	out, err := selfservice.CanonicalJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", selfservice.ErrInvalid, err)
	}

	if bytes.Equal(out, []byte("null")) {
		return nil, fmt.Errorf("%w: a value is not null", selfservice.ErrInvalid)
	}

	return out, nil
}

// checkHomes returns ErrOneHome for two settings with one normalized key.
// Defense in depth: selfservice.CheckOneHome checks the same rule on the
// blueprint, and this check stays so that no file can set one key twice
// even if that check changes.
func checkHomes(settings []setting) error {
	seen := make(map[string]string, len(settings))

	for i := range settings {
		s := &settings[i]
		if have, ok := seen[s.norm]; ok {
			return fmt.Errorf("%w: key %q is set by %s and by %s", selfservice.ErrOneHome, s.key, have, s.home)
		}

		seen[s.norm] = s.home
	}

	return nil
}

// keysOf returns the normalized keys of settings, sorted.
func keysOf(settings []setting) []string {
	keys := make([]string, 0, len(settings))
	for i := range settings {
		keys = append(keys, settings[i].norm)
	}

	slices.Sort(keys)

	return keys
}

// operationFor returns the operation of a render: see the package comment.
func operationFor(in selfservice.RenderInput) selfservice.Operation {
	switch {
	case in.Document.Ensure == selfservice.EnsureAbsent:
		return selfservice.OperationDestroy
	case in.Unit.UnitID() != "":
		return selfservice.OperationUpdate
	default:
		return selfservice.OperationCreate
	}
}

// finish checks the files and returns the render.
func finish(
	in selfservice.RenderInput,
	files []selfservice.File,
	allowed []string,
	settings []setting,
) (selfservice.Rendered, error) {
	if err := checkFiles(files, allowed); err != nil {
		return selfservice.Rendered{}, err
	}

	return selfservice.Rendered{Operation: operationFor(in), Files: files, Keys: keysOf(settings)}, nil
}

// checkFiles refuses a file whose path is not one of allowed, a path that
// is not relative and clean, and a file over maxFileBytes. Defense in
// depth: the renderers write fixed names, and this check stays so that no
// change of a name can write outside the render directory. It does not rely
// on the check of the unit name.
func checkFiles(files []selfservice.File, allowed []string) error {
	seen := make(map[string]bool, len(files))

	for _, f := range files {
		switch {
		case !slices.Contains(allowed, f.Path):
			return fmt.Errorf("%w: the file %q is not one of the files of this render", selfservice.ErrInvalid, f.Path)
		case path.IsAbs(f.Path) || path.Clean(f.Path) != f.Path || strings.Contains(f.Path, "..") || strings.ContainsAny(f.Path, "/\\\x00"):
			return fmt.Errorf("%w: the file path %q is not a relative file name", selfservice.ErrInvalid, f.Path)
		case seen[f.Path]:
			return fmt.Errorf("%w: the file %q appears twice", selfservice.ErrInvalid, f.Path)
		case len(f.Content) > maxFileBytes:
			return fmt.Errorf(
				"%w: the file %q has %d bytes, the limit is %d",
				selfservice.ErrLimit,
				f.Path,
				len(f.Content),
				maxFileBytes,
			)
		default:
			seen[f.Path] = true
		}
	}

	return nil
}

// allowedText reports whether s is 1 to limit bytes of ASCII letters, digits
// and the bytes of extra. The renderers copy the source, the version, the
// package and the type of a program into the program text only when they
// pass this allowlist, so that the text can hold no quote, no backslash, no
// '$', no '{', no white space and no control byte.
func allowedText(s, extra string, limit int) bool {
	if s == "" || len(s) > limit {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') &&
			!strings.ContainsRune(extra, rune(c)) {
			return false
		}
	}

	return true
}

func sortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}

	slices.Sort(names)

	return names
}
