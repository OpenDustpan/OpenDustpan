package render

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// tofuSourceExtra and tofuVersionExtra are the bytes, besides ASCII letters
// and digits, that a module source and a module version can hold in the
// text of a root module. They hold no quote, no backslash, no '$', no '%',
// no '{' and no white space, so the text cannot start a template sequence.
const (
	tofuSourceExtra  = "._~/:@+=?&,#-"
	tofuVersionExtra = "._+-"
)

// Tofu is the renderer of OpenTofu. It holds no state.
type Tofu struct{}

var _ selfservice.Renderer = Tofu{}

// NewTofu returns the renderer of OpenTofu: a root module of one module call
// in main.tf, and its values in terraform.tfvars.json.
//
// The module call names a variable var.<key> for each key of the blueprint
// that has a value, with one variable block for each. The values go only
// into terraform.tfvars.json, written by a JSON encoder. The blueprint must
// name a module from a registry source: a module from git has no render.
// The operation of the render is the one that the package comment gives.
func NewTofu() Tofu {
	return Tofu{}
}

// Tool returns the tool name of OpenTofu.
func (Tofu) Tool() string {
	return selfservice.ToolOpenTofu
}

// Render returns main.tf and terraform.tfvars.json for in. It returns
// ErrInvalid for a blueprint of another tool, a program that is not one
// module from a registry source, a key that selfservice.ValidKey refuses,
// an answer that is not an input of the blueprint and a module source or
// version with a byte outside the allowlist. It returns ErrLimit for an
// input above a limit and ErrOneHome for a key that two homes set. An error
// comes with no file.
func (Tofu) Render(ctx context.Context, in selfservice.RenderInput) (selfservice.Rendered, error) {
	b, settings, err := collect(ctx, in, selfservice.ToolOpenTofu)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	module, err := tofuModule(&b.Program)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	values := make(map[string]json.RawMessage, len(settings))
	for i := range settings {
		values[settings[i].key] = settings[i].value
	}

	// A JSON encoder writes the values. It sorts the keys of the map.
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return selfservice.Rendered{}, fmt.Errorf("%w: %w", selfservice.ErrInvalid, err)
	}

	files := []selfservice.File{
		{Path: fileTofuProgram, Content: []byte(tofuProgram(module, settings))},
		{Path: fileTofuValues, Content: append(data, '\n')},
	}

	return finish(in, files, []string{fileTofuProgram, fileTofuValues}, settings)
}

// tofuModule returns the module of p, when p is one module from a registry
// source with text that the allowlists accept.
func tofuModule(p *selfservice.Program) (*selfservice.Module, error) {
	m := p.Module

	switch {
	case m == nil || p.Component != nil:
		return nil, fmt.Errorf("%w: a program of OpenTofu names one module and no component", selfservice.ErrInvalid)
	case m.Source == "":
		return nil, fmt.Errorf(
			"%w: this render reads a module from a registry source, not from git",
			selfservice.ErrInvalid,
		)
	case !allowedText(m.Source, tofuSourceExtra, maxSourceBytes):
		return nil, fmt.Errorf(
			"%w: the module source %q has a byte that a source cannot have",
			selfservice.ErrInvalid,
			m.Source,
		)
	case !allowedText(m.Version, tofuVersionExtra, maxVersionBytes):
		return nil, fmt.Errorf(
			"%w: the module version %q has a byte that a version cannot have",
			selfservice.ErrInvalid,
			m.Version,
		)
	default:
		return m, nil
	}
}

// tofuProgram returns the text of the root module. It holds only the
// source and the version, which passed an allowlist, and keys, which passed
// selfservice.ValidKey, and fixed text.
func tofuProgram(m *selfservice.Module, settings []setting) string {
	var sb strings.Builder

	sb.WriteString("module \"main\" {\n")
	fmt.Fprintf(&sb, "  source  = %q\n", m.Source)
	fmt.Fprintf(&sb, "  version = %q\n", m.Version)

	if len(settings) > 0 {
		sb.WriteString("\n")
	}

	for i := range settings {
		fmt.Fprintf(&sb, "  %s = var.%s\n", settings[i].key, settings[i].key)
	}

	sb.WriteString("}\n")

	for i := range settings {
		fmt.Fprintf(&sb, "\nvariable %q {}\n", settings[i].key)
	}

	return sb.String()
}
