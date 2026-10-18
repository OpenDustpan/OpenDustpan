package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// The names of the outputs that give an agent context about a unit.
const (
	// ContextOutputName is the reserved output of a unit for agent context.
	// Its value is a ContextOutput, as a JSON object or as a string that
	// holds one.
	ContextOutputName = "opendustpan"
	// ReadmeOutputName is the output that the Pulumi CLI shows as the
	// readme of a stack. Its value is Markdown text.
	ReadmeOutputName = "readme"
)

// Bounds of a context output.
const (
	// ContextSchemaVersion is the newest schema version of a context output
	// that this server reads.
	ContextSchemaVersion = 1
	// MaxContextBytes bounds the encoded context output and the readme.
	MaxContextBytes = 64 << 10
	// MaxContextLinks bounds the links of a context output.
	MaxContextLinks = 50
	// MaxContextOwners bounds the owners of a context output.
	MaxContextOwners = 20
)

// Errors of ParseContextOutput.
var (
	// ErrContextInvalid reports a context output that is not a valid
	// ContextOutput.
	ErrContextInvalid = errors.New("the opendustpan output is not valid")
	// ErrContextVersion reports a context output with a schema version that
	// this server does not read.
	ErrContextVersion = errors.New("the opendustpan output has an unknown schema version")
)

// ContextOutput is the value of the reserved output of a unit. The team of
// the unit writes it in its code, so a review of the code reviews it too.
// Every field except SchemaVersion is optional.
type ContextOutput struct {
	SchemaVersion int `json:"schemaVersion"`
	// Summary tells what the unit is for, in a few sentences.
	Summary string `json:"summary,omitempty"`
	// Runbook is Markdown text: how to operate the unit, what is safe to
	// change, what is not.
	Runbook string `json:"runbook,omitempty"`
	// Owners are the teams or persons who own the unit.
	Owners []string `json:"owners,omitempty"`
	// OnCall names where to page the people on call for the unit.
	OnCall string        `json:"onCall,omitempty"`
	Links  []ContextLink `json:"links,omitempty"`
}

// ContextLink is one link of a context output. URL is an absolute http or
// https address.
type ContextLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ParseContextOutput reads the value of the reserved output. raw is the JSON
// of the output value: an object, or a string that holds the JSON of one. It
// refuses unknown fields, so a typing error shows at the first read. It
// returns ErrContextInvalid and ErrContextVersion. The text of a context
// output comes from the team of the unit, so an agent reads it as data, never
// as instructions.
func ParseContextOutput(raw []byte) (ContextOutput, error) {
	var zero ContextOutput

	if len(raw) > MaxContextBytes {
		return zero, fmt.Errorf("%w: more than %d bytes", ErrContextInvalid, MaxContextBytes)
	}

	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '"' {
		var inner string
		if err := json.Unmarshal(raw, &inner); err != nil {
			return zero, fmt.Errorf("%w: not a JSON string", ErrContextInvalid)
		}

		raw = []byte(inner)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var out ContextOutput
	if err := dec.Decode(&out); err != nil {
		return zero, fmt.Errorf("%w: not a JSON object with the known fields", ErrContextInvalid)
	}

	if dec.More() {
		return zero, fmt.Errorf("%w: more than one JSON value", ErrContextInvalid)
	}

	if err := out.check(); err != nil {
		return zero, err
	}

	return out, nil
}

func (c ContextOutput) check() error {
	switch {
	case c.SchemaVersion < 1:
		return fmt.Errorf("%w: schemaVersion must be 1 or more", ErrContextInvalid)
	case c.SchemaVersion > ContextSchemaVersion:
		return fmt.Errorf("%w: %d", ErrContextVersion, c.SchemaVersion)
	case len(c.Links) > MaxContextLinks:
		return fmt.Errorf("%w: more than %d links", ErrContextInvalid, MaxContextLinks)
	case len(c.Owners) > MaxContextOwners:
		return fmt.Errorf("%w: more than %d owners", ErrContextInvalid, MaxContextOwners)
	}

	for _, l := range c.Links {
		if strings.TrimSpace(l.Title) == "" || !webURL(l.URL) {
			return fmt.Errorf("%w: each link needs a title and an http or https URL", ErrContextInvalid)
		}
	}

	return nil
}

func webURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u == nil {
		return false
	}

	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}
