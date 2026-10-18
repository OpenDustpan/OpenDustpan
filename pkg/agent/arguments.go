package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
)

// Arguments are the arguments of one tool call, decoded, with numbers as
// json.Number.
type Arguments map[string]any

// String returns the string argument name, or "" when it is not set.
func (a Arguments) String(name string) string {
	if s, ok := a[name].(string); ok {
		return s
	}

	return ""
}

// Bool returns the boolean argument name, or false when it is not set.
func (a Arguments) Bool(name string) bool {
	if b, ok := a[name].(bool); ok {
		return b
	}

	return false
}

// Int returns the integer argument name, or def when it is not set.
// ValidateArguments already checked its bounds.
func (a Arguments) Int(name string, def int) int {
	n, ok := a[name].(json.Number)
	if !ok {
		return def
	}

	v, err := n.Int64()
	if err != nil {
		return def
	}

	return int(v)
}

// ValidateArguments decodes raw and checks it against the parameters of
// spec: a JSON object, no parameter that the tool does not have, each
// required parameter, the type of each, a string at most Max bytes long and
// in Enum when Enum is set, an integer between 1 and Max, an array of at
// most Max items. read_state also refuses secretValues with compareTo. It
// returns ErrInvalidArguments, with the name of the parameter, and never
// the value.
func ValidateArguments(spec ToolSpec, raw json.RawMessage) (Arguments, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var args Arguments
	if err := dec.Decode(&args); err != nil || args == nil {
		return nil, fmt.Errorf("%w: the arguments must be a JSON object", ErrInvalidArguments)
	}

	if err := checkParams(spec, args); err != nil {
		return nil, err
	}

	if spec.Name == ToolReadState && args.Bool("secretValues") && args.String("compareTo") != "" {
		return nil, fmt.Errorf("%w: secretValues cannot be used with compareTo", ErrInvalidArguments)
	}

	return args, nil
}

// checkParams checks args against the parameters of spec.
func checkParams(spec ToolSpec, args Arguments) error {
	for name := range args {
		if !slices.ContainsFunc(spec.Params, func(p Param) bool { return p.Name == name }) {
			return fmt.Errorf("%w: unknown parameter %q", ErrInvalidArguments, name)
		}
	}

	for _, p := range spec.Params {
		v, ok := args[p.Name]

		switch {
		case !ok && p.Required:
			return fmt.Errorf("%w: parameter %q is required", ErrInvalidArguments, p.Name)
		case !ok:
			continue
		default:
		}

		if err := checkArgument(p, v); err != nil {
			return fmt.Errorf("%w: parameter %q %w", ErrInvalidArguments, p.Name, err)
		}
	}

	return nil
}

// argumentError is the cause of a bad argument. Its text follows the name
// of the parameter.
type argumentError string

func (e argumentError) Error() string { return string(e) }

func checkArgument(p Param, v any) error {
	switch p.Type {
	case ParamString:
		return checkString(p, v)
	case ParamInteger:
		return checkInteger(p, v)
	case ParamBoolean:
		if _, ok := v.(bool); !ok {
			return argumentError("must be true or false")
		}

		return nil
	case ParamArray:
		if a, ok := v.([]any); !ok || len(a) > p.Max {
			return argumentError(fmt.Sprintf("must be an array of at most %d items", p.Max))
		}

		return nil
	default:
		return argumentError(fmt.Sprintf("has the unknown type %q", p.Type))
	}
}

func checkInteger(p Param, v any) error {
	n, ok := v.(json.Number)
	if !ok {
		return argumentError("must be an integer")
	}

	if i, err := n.Int64(); err != nil || i < 1 || i > int64(p.Max) {
		return argumentError(fmt.Sprintf("must be an integer from 1 to %d", p.Max))
	}

	return nil
}

func checkString(p Param, v any) error {
	s, ok := v.(string)

	switch {
	case !ok:
		return argumentError("must be a string")
	case len(s) > p.Max:
		return argumentError(fmt.Sprintf("is longer than %d bytes", p.Max))
	case len(p.Enum) > 0 && !slices.Contains(p.Enum, s):
		return argumentError(fmt.Sprintf("must be one of %v", p.Enum))
	default:
		return nil
	}
}

// ArgsDigest returns the SHA-256, in hex, of the arguments raw in canonical
// JSON: object keys sorted, no space, numbers as written. Two calls with the
// same arguments in another key order or spacing have one digest. It
// returns ErrInvalidArguments for raw that is not JSON.
func ArgsDigest(raw json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var doc any
	if err := dec.Decode(&doc); err != nil {
		return "", fmt.Errorf("%w: the arguments are not JSON", ErrInvalidArguments)
	}

	canonical, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("digest the arguments: %w", err)
	}

	sum := sha256.Sum256(canonical)

	return hex.EncodeToString(sum[:]), nil
}
