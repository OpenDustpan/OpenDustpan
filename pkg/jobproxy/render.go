package jobproxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The variables of a path pattern.
const (
	VarUnitTool      = "unit.tool"
	VarUnitNamespace = "unit.namespace"
	VarUnitName      = "unit.name"
	VarOperation     = "operation"
	// VarLabelPrefix starts unit.labels.<key>.
	VarLabelPrefix = "unit.labels."
	// VarInputPrefix starts inputs.<name>.
	VarInputPrefix = "inputs."
	// VarProfilePrefix starts profile.<name>.<key>.
	VarProfilePrefix = "profile."
)

// Fixed reasons of a variable that Render refuses. A reason never holds the
// value.
const (
	reasonVarMissing = "the variable has no value"
	reasonVarSecret  = "the value is secret"
	reasonVarEmpty   = "the value is empty"
	reasonVarSegment = "the value is not exactly one canonical path segment"
	reasonVarWild    = "the value holds *, which a path pattern reads as a wildcard"
)

// Value is one value of a profile.
type Value struct {
	Text string
	// Secret marks a value that only the job proxy and the tool may see. A
	// path holds no secret value, so Render refuses it.
	Secret bool
}

// Vars are the values of the variables of a job, when it starts.
type Vars struct {
	Tool      string
	Namespace string
	Name      string
	Labels    map[string]string
	Operation Operation
	// Inputs holds the string answers of the Catalog request of the job.
	Inputs map[string]string
	// Profiles maps a profile name to its values by key.
	Profiles map[string]map[string]Value
}

// lookup returns the value of the variable name and true, or false when it
// has no value.
func (v Vars) lookup(name string) (Value, bool) {
	switch name {
	case VarUnitTool:
		return Value{Text: v.Tool, Secret: false}, true
	case VarUnitNamespace:
		return Value{Text: v.Namespace, Secret: false}, true
	case VarUnitName:
		return Value{Text: v.Name, Secret: false}, true
	case VarOperation:
		return Value{Text: string(v.Operation), Secret: false}, true
	}

	if key, ok := strings.CutPrefix(name, VarLabelPrefix); ok {
		text, found := v.Labels[key]

		return Value{Text: text, Secret: false}, found
	}

	if key, ok := strings.CutPrefix(name, VarInputPrefix); ok {
		text, found := v.Inputs[key]

		return Value{Text: text, Secret: false}, found
	}

	if rest, ok := strings.CutPrefix(name, VarProfilePrefix); ok {
		profile, key, _ := strings.Cut(rest, ".")
		value, found := v.Profiles[profile][key]

		return value, found
	}

	return Value{Text: "", Secret: false}, false
}

// labelKeyPattern is the shape of a label key: 1 to 128 letters, digits
// and . _ : / -, the label key rule of the inventory. The package keeps its
// own copy, so the launcher does not link the inventory. A key with / cannot
// be in a template, because / ends the segment of the template.
var labelKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,128}$`)

// validVariable reports whether name is a known variable.
func validVariable(name string) bool {
	switch name {
	case VarUnitTool, VarUnitNamespace, VarUnitName, VarOperation:
		return true
	}

	if key, ok := strings.CutPrefix(name, VarLabelPrefix); ok {
		return labelKeyPattern.MatchString(key)
	}

	if key, ok := strings.CutPrefix(name, VarInputPrefix); ok {
		return namePattern.MatchString(key)
	}

	if rest, ok := strings.CutPrefix(name, VarProfilePrefix); ok {
		profile, key, found := strings.Cut(rest, ".")

		return found && namePattern.MatchString(profile) && namePattern.MatchString(key)
	}

	return false
}

// RenderedRule is one proxy rule with its templates filled.
type RenderedRule struct {
	ID                string      `json:"id"`
	Operations        []Operation `json:"operations"`
	Host              string      `json:"host"`
	Port              int         `json:"port"`
	Mode              Mode        `json:"mode"`
	Methods           []string    `json:"methods"`
	Paths             []string    `json:"paths"`
	Credential        string      `json:"credential"`
	ClientCertificate string      `json:"clientCertificate"`
	ServerTrust       string      `json:"serverTrust"`
}

// Rendered is a proxy rule set with its templates filled, as the job proxy
// of one job applies it. Its zero value allows no request.
type Rendered struct {
	rules []renderedRule
}

// renderedRule is a rendered proxy rule with its parsed path patterns.
type renderedRule struct {
	RenderedRule

	patterns [][]segment
}

// Rules returns a copy of the rendered proxy rules, in order.
func (r Rendered) Rules() []RenderedRule {
	out := make([]RenderedRule, 0, len(r.rules))

	for i := range r.rules {
		c := r.rules[i].RenderedRule
		c.Operations = append([]Operation(nil), c.Operations...)
		c.Methods = append([]string(nil), c.Methods...)
		c.Paths = append([]string(nil), c.Paths...)
		out = append(out, c)
	}

	return out
}

// Digest returns the lowercase hex SHA-256 of the canonical JSON form of
// the rendered proxy rules. The job spec holds it, so each phase of the job
// runs the same proxy rules. The form has no map, so the digest does not
// depend on the order of a map of Vars.
func (r Rendered) Digest() string {
	// Marshal of a struct of strings, ints and lists of strings cannot fail.
	data, err := json.Marshal(struct {
		Rules []RenderedRule `json:"rules"`
	}{Rules: r.Rules()})
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

// Render fills the templates of each path pattern of set with vars. It
// returns ErrInvalid when set fails Validate, and ErrRender when a variable
// has no value, its value is secret, empty, holds "*" or is not exactly one
// canonical path segment. The error names the proxy rule and the variable,
// never the value.
func Render(set RuleSet, vars Vars) (Rendered, error) {
	// Defense in depth: Parse checks each proxy rule already; this check
	// stays so that a proxy rule set that a caller builds by hand cannot skip it.
	if err := set.Validate(); err != nil {
		return Rendered{}, err
	}

	out := Rendered{rules: make([]renderedRule, 0, len(set.Rules))}

	for i := range set.Rules {
		rr, err := renderRule(&set.Rules[i], vars)
		if err != nil {
			return Rendered{}, err
		}

		out.rules = append(out.rules, rr)
	}

	return out, nil
}

// renderRule renders one proxy rule.
func renderRule(r *Rule, vars Vars) (renderedRule, error) {
	base := RenderedRule{
		ID:                r.ID,
		Operations:        append([]Operation(nil), r.Operations...),
		Host:              r.Host,
		Port:              r.Port,
		Mode:              r.Mode,
		Methods:           append([]string(nil), r.Methods...),
		Paths:             make([]string, 0, len(r.Paths)),
		Credential:        r.Credential,
		ClientCertificate: r.ClientCertificate,
		ServerTrust:       r.ServerTrust,
	}

	rr := renderedRule{
		RenderedRule: base,
		patterns:     make([][]segment, 0, len(r.Paths)),
	}

	for _, p := range r.Paths {
		segs, reason := parsePattern(p)
		if reason != "" {
			return renderedRule{}, fmt.Errorf("%w: proxy rule %s: %s", ErrInvalid, r.ID, reason)
		}

		filled, err := fillSegments(r.ID, segs, vars)
		if err != nil {
			return renderedRule{}, err
		}

		rr.patterns = append(rr.patterns, filled)
		rr.Paths = append(rr.Paths, patternString(filled))
	}

	return rr, nil
}

// fillSegments replaces each variable segment with its value as a literal.
func fillSegments(id string, segs []segment, vars Vars) ([]segment, error) {
	out := make([]segment, 0, len(segs))

	for _, s := range segs {
		if s.kind != segmentVariable {
			out = append(out, s)

			continue
		}

		if reason := checkValue(s.text, vars); reason != "" {
			return nil, fmt.Errorf("%w: proxy rule %s, variable %s: %s", ErrRender, id, s.text, reason)
		}

		v, _ := vars.lookup(s.text)
		out = append(out, segment{kind: segmentLiteral, text: v.Text})
	}

	return out, nil
}

// checkValue returns the fixed reason that the value of the variable name
// cannot fill a path segment, or "".
func checkValue(name string, vars Vars) string {
	v, ok := vars.lookup(name)

	switch {
	case !ok:
		return reasonVarMissing
	case v.Secret:
		return reasonVarSecret
	case v.Text == "":
		return reasonVarEmpty
	case strings.Contains(v.Text, "*"):
		return reasonVarWild
	case !canonicalSegment(v.Text):
		return reasonVarSegment
	default:
		return ""
	}
}

// patternString writes a rendered path pattern. A rendered literal holds no
// "*" and no "{", so the text has one reading.
func patternString(segs []segment) string {
	if len(segs) == 0 {
		return "/"
	}

	var b strings.Builder

	for _, s := range segs {
		b.WriteByte('/')

		switch s.kind {
		case segmentOne:
			b.WriteString("*")
		case segmentRest:
			b.WriteString("**")
		case segmentLiteral, segmentVariable:
			b.WriteString(s.text)
		default:
			// Each kind is above.
		}
	}

	return b.String()
}
