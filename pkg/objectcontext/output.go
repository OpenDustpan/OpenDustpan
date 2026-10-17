package objectcontext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/graph"
)

// The names of the outputs that give context to a unit.
const (
	// OutputName is the reserved output of a unit for context. Its value is
	// an Output, as a JSON object or as a string that holds one.
	OutputName = "opendustpan"
	// ReadmeOutputName is the output that the Pulumi CLI shows as the readme
	// of a stack. Its value is Markdown text. The server accepts it from a
	// unit of any tool.
	ReadmeOutputName = "readme"
)

// Bounds of the reserved output.
const (
	// OutputSchemaVersion is the newest schema version of the reserved
	// output that this server reads.
	OutputSchemaVersion = 1
	// MaxOutputBytes bounds the encoded reserved output.
	MaxOutputBytes = 64 << 10
	// MaxOutputResources bounds the resources that one output describes.
	MaxOutputResources = 200
)

// Output is the value of the reserved output of a unit. The team of the unit
// writes it in its code. The fields of Fields describe the unit. Resources
// describes resources of the same unit, by the key of each resource in the
// state: the URN or the resource instance address. Every field except
// SchemaVersion is optional.
type Output struct {
	Fields

	Resources     map[string]Fields `json:"resources,omitempty"`
	SchemaVersion int               `json:"schemaVersion"`
}

// ParseOutput reads the value of the reserved output. raw is the JSON of the
// output value: an object, or a string that holds the JSON of one. It
// refuses an unknown field, so a typing error shows at the first read. It
// returns ErrOutputInvalid and ErrOutputVersion.
func ParseOutput(raw []byte) (Output, error) {
	var zero Output

	if len(raw) > MaxOutputBytes {
		return zero, fmt.Errorf("%w: more than %d bytes", ErrOutputInvalid, MaxOutputBytes)
	}

	out, err := decodeOutput(raw)
	if err != nil {
		return zero, err
	}

	if err := out.validate(); err != nil {
		return zero, err
	}

	return out, nil
}

// decodeOutput decodes raw, a JSON object or a JSON string that holds one,
// into an Output. It refuses an unknown field and a second JSON value. It
// returns ErrOutputInvalid.
func decodeOutput(raw []byte) (Output, error) {
	var out Output

	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return out, fmt.Errorf("%w: not a JSON string", ErrOutputInvalid)
		}

		raw = []byte(inner)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		return out, fmt.Errorf("%w: not a JSON object with the known fields", ErrOutputInvalid)
	}

	if dec.More() {
		return out, fmt.Errorf("%w: more than one JSON value", ErrOutputInvalid)
	}

	return out, nil
}

// ParseReadme reads the value of the output "readme": a JSON string of
// Markdown text of at most MaxRunbookBytes. It returns ErrOutputInvalid.
func ParseReadme(raw []byte) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%w: the readme output is not a string", ErrOutputInvalid)
	}

	if !validMultilineText(s, MaxRunbookBytes) {
		return "", fmt.Errorf("%w: the readme output is longer than %d bytes or holds control characters",
			ErrOutputInvalid, MaxRunbookBytes)
	}

	return s, nil
}

// Entries returns the output entries of unit u from o: one entry for the unit,
// with readme, and one entry for each resource, ordered by resource key. item
// is the ID of the state item and at its time.
func (o Output) Entries(u graph.Unit, readme, item string, at time.Time) []Entry {
	out := make([]Entry, 0, 1+len(o.Resources))
	out = append(out, Entry{
		Object: UnitRef(u), Source: SourceOutput, Fields: o.Fields, Readme: readme,
		Item: item, UpdatedAt: at,
		Warning:   "",
		UpdatedBy: "",
	})

	keys := make([]string, 0, len(o.Resources))
	for k := range o.Resources {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		out = append(out, Entry{
			Object: ResourceRef(u, k), Source: SourceOutput, Fields: o.Resources[k],
			Item: item, UpdatedAt: at,
			Readme:    "",
			Warning:   "",
			UpdatedBy: "",
		})
	}

	return out
}

// validate reports whether o has a known schema version and valid fields,
// resource keys and resource fields. It returns ErrOutputInvalid and
// ErrOutputVersion.
func (o Output) validate() error {
	switch {
	case o.SchemaVersion < 1:
		return fmt.Errorf("%w: schemaVersion must be 1 or more", ErrOutputInvalid)
	case o.SchemaVersion > OutputSchemaVersion:
		return fmt.Errorf("%w: %d", ErrOutputVersion, o.SchemaVersion)
	case len(o.Resources) > MaxOutputResources:
		return fmt.Errorf("%w: more than %d resources", ErrOutputInvalid, MaxOutputResources)
	}

	if err := o.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrOutputInvalid, err)
	}

	for key, f := range o.Resources {
		if !validText(key, MaxResourceKeyBytes) || key == "" {
			return fmt.Errorf("%w: resource key", ErrOutputInvalid)
		}

		if err := f.Validate(); err != nil {
			return fmt.Errorf("%w: resource %q: %w", ErrOutputInvalid, key, err)
		}
	}

	return nil
}
