package render

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/selfservice"
)

// pulumiNameExtra, pulumiVersionExtra and pulumiTypeExtra are the bytes,
// besides ASCII letters and digits, that a package, a version and a type
// token can hold in the text of the program.
const (
	pulumiNameExtra    = "._-"
	pulumiVersionExtra = "._+-"
	pulumiTypeExtra    = "._:/-"
)

// Pulumi is the renderer of the Pulumi CLI. It holds no state.
type Pulumi struct{}

var _ selfservice.Renderer = Pulumi{}

// NewPulumi returns the renderer of the Pulumi CLI: a Pulumi YAML program of
// one component resource in Pulumi.yaml, and the stack settings in
// Pulumi.<unit name>.yaml.
//
// The program declares each key of the blueprint that has a value and no
// namespace of another package as a config entry of the project, and gives
// it to the component as ${<key>}. A key with the namespace of another
// package, such as a provider setting, goes only into the stack settings.
// The values go only into the stack settings. A JSON encoder writes each
// value as a JSON scalar, list or object on one line, with every byte
// outside ASCII as an escape, so the file holds no anchor, no tag and no
// alias. The unit name must pass selfservice.ValidName, because it is part
// of the name of the stack settings file. The operation of the render is the
// one that the package comment gives.
func NewPulumi() Pulumi {
	return Pulumi{}
}

// Tool returns the tool name of the Pulumi CLI.
func (Pulumi) Tool() string {
	return selfservice.ToolPulumi
}

// Render returns Pulumi.yaml and the stack settings file for in. It returns
// ErrInvalid for a blueprint of another tool, a program that is not one
// component, a unit that is not a unit of Pulumi or has a name that
// selfservice.ValidName refuses, a key that selfservice.ValidKey refuses, a
// project key whose value is null, an answer that is not an input of the
// blueprint and a package, version or type with a byte outside the
// allowlist. It returns ErrLimit for an input above a limit and ErrOneHome
// for a key that two homes set. An error comes with no file.
func (Pulumi) Render(ctx context.Context, in selfservice.RenderInput) (selfservice.Rendered, error) {
	b, settings, err := collect(ctx, in, selfservice.ToolPulumi)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	component, err := pulumiComponent(&b.Program)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	settingsFile, err := pulumiSettingsPath(in)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	if !selfservice.ValidName(b.Name) {
		return selfservice.Rendered{}, fmt.Errorf("%w: blueprint name %q", selfservice.ErrInvalid, b.Name)
	}

	stack, err := pulumiSettings(b, settings)
	if err != nil {
		return selfservice.Rendered{}, err
	}

	files := []selfservice.File{
		{Path: filePulumiProgram, Content: []byte(pulumiProgram(b, component, settings))},
		{Path: settingsFile, Content: []byte(stack)},
	}

	return finish(in, files, []string{filePulumiProgram, settingsFile}, settings)
}

// pulumiComponent returns the component of p, when p is one component with
// text that the allowlists accept.
func pulumiComponent(p *selfservice.Program) (*selfservice.Component, error) {
	c := p.Component

	switch {
	case c == nil || p.Module != nil:
		return nil, fmt.Errorf("%w: a program of Pulumi names one component and no module", selfservice.ErrInvalid)
	case !allowedText(c.Package, pulumiNameExtra, maxVersionBytes):
		return nil, fmt.Errorf(
			"%w: the package %q has a byte that a package cannot have",
			selfservice.ErrInvalid,
			c.Package,
		)
	case !allowedText(c.Version, pulumiVersionExtra, maxVersionBytes):
		return nil, fmt.Errorf(
			"%w: the version %q has a byte that a version cannot have",
			selfservice.ErrInvalid,
			c.Version,
		)
	case !allowedText(c.Type, pulumiTypeExtra, maxTypeBytes):
		return nil, fmt.Errorf("%w: the type %q has a byte that a type cannot have", selfservice.ErrInvalid, c.Type)
	default:
		return c, nil
	}
}

// pulumiSettingsPath returns the path of the stack settings file of the
// unit of in. Defense in depth: the file name holds the unit name, so the
// name passes selfservice.ValidName here, and checkFiles checks the path
// again. It does not rely on the unit check of the core.
func pulumiSettingsPath(in selfservice.RenderInput) (string, error) {
	switch name := in.Unit.UnitName(); {
	case !in.Unit.IsUnit() || in.Unit.Tool() != selfservice.ToolPulumi:
		return "", fmt.Errorf("%w: a render of Pulumi needs a unit of Pulumi", selfservice.ErrInvalid)
	case !selfservice.ValidName(name):
		return "", fmt.Errorf("%w: the unit name %q cannot be part of a file name", selfservice.ErrInvalid, name)
	default:
		return "Pulumi." + name + ".yaml", nil
	}
}

// projectKey returns the key of s as the program names it, and false when
// the key has the namespace of another package.
func projectKey(b *selfservice.Blueprint, key string) (string, bool) {
	ns, name, found := strings.Cut(key, ":")
	if !found {
		return key, true
	}

	if ns == b.Name {
		return name, true
	}

	return "", false
}

// pulumiProgram returns the text of Pulumi.yaml. It holds the name of the
// blueprint, the package, the version and the type, which passed an
// allowlist, keys, which passed selfservice.ValidKey, and fixed text. Each
// string is a double-quoted JSON string.
func pulumiProgram(b *selfservice.Blueprint, c *selfservice.Component, settings []setting) string {
	var config, properties strings.Builder

	for i := range settings {
		key, ok := projectKey(b, settings[i].key)
		if !ok {
			continue
		}

		fmt.Fprintf(&config, "  %q:\n%s", key, pulumiConfigType(settings[i].value))
		fmt.Fprintf(&properties, "      %q: %q\n", key, "${"+key+"}")
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "name: %q\nruntime: yaml\n", b.Name)
	fmt.Fprintf(&sb, "packages:\n  %q:\n    source: %q\n    version: %q\n", c.Package, c.Package, c.Version)

	if config.Len() == 0 {
		sb.WriteString("config: {}\n")
	} else {
		sb.WriteString("config:\n" + config.String())
	}

	fmt.Fprintf(&sb, "resources:\n  main:\n    type: %q\n", c.Type)

	if properties.Len() == 0 {
		sb.WriteString("    properties: {}\n")
	} else {
		sb.WriteString("    properties:\n" + properties.String())
	}

	return sb.String()
}

// pulumiConfigType returns the type lines of a config entry for the
// canonical JSON value.
func pulumiConfigType(value []byte) string {
	switch value[0] {
	case '"':
		return "    type: string\n"
	case 't', 'f':
		return "    type: boolean\n"
	case '{':
		return "    type: object\n"
	case '[':
		var items []any
		if json.Unmarshal(value, &items) == nil && allStrings(items) {
			return "    type: array\n    items:\n      type: string\n"
		}

		return "    type: array\n"
	default:
		if bytes.ContainsAny(value, ".eE") {
			return "    type: number\n"
		}

		return "    type: integer\n"
	}
}

func allStrings(items []any) bool {
	for _, item := range items {
		if _, ok := item.(string); !ok {
			return false
		}
	}

	return true
}

// pulumiSettings returns the text of the stack settings file: one config
// entry for each setting, with the key in the form <namespace>:<name>. The
// key of the project gets the name of the blueprint as its namespace.
func pulumiSettings(b *selfservice.Blueprint, settings []setting) (string, error) {
	if len(settings) == 0 {
		return "config: {}\n", nil
	}

	var sb strings.Builder

	sb.WriteString("config:\n")

	for i := range settings {
		key := settings[i].key
		if !strings.Contains(key, ":") {
			key = b.Name + ":" + key
		}

		value, err := asciiJSON(settings[i].value)
		if err != nil {
			return "", fmt.Errorf("key %q: %w", settings[i].key, err)
		}

		// The key passed selfservice.ValidKey, so it holds only ASCII
		// letters, digits, '_', '-' and ':', and %q writes it as it is.
		fmt.Fprintf(&sb, "  %q: %s\n", key, value)
	}

	return sb.String(), nil
}

// asciiJSON returns the JSON text data as a YAML flow value, with each rune
// above 0x7e written as a YAML escape (\u with four digits, or \U with
// eight digits above U+FFFF), so the text is ASCII and every YAML parser
// reads the same string. A non-ASCII byte occurs only inside a JSON string.
// It returns ErrInvalid for a character that a YAML parser refuses.
func asciiJSON(data []byte) (string, error) {
	var sb strings.Builder

	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		i += size

		switch {
		case isNoncharacter(r) || (r == utf8.RuneError && size == 1):
			// A YAML parser refuses these characters, also as an escape.
			// Fail closed: the render has no form for the value.
			return "", fmt.Errorf(
				"%w: a value has the character U+%04X, which stack settings cannot hold",
				selfservice.ErrInvalid,
				r,
			)
		case r < asciiEnd:
			sb.WriteRune(r)
		case r >= firstAstral:
			// YAML reads an escape with eight digits, and no surrogate pair.
			fmt.Fprintf(&sb, `\U%08x`, r)
		default:
			fmt.Fprintf(&sb, `\u%04x`, r)
		}
	}

	return sb.String(), nil
}

// The limits of the escapes of asciiJSON: the first rune that gets an
// escape, and the first rune that needs eight digits.
const (
	asciiEnd    = 0x7f
	firstAstral = 0x10000
)

// isNoncharacter reports whether r is a Unicode noncharacter.
func isNoncharacter(r rune) bool {
	const lastOfPlane, firstInBlock, lastInBlock = 0xfffe, 0xfdd0, 0xfdef

	return (r >= firstInBlock && r <= lastInBlock) || r&lastOfPlane == lastOfPlane
}
