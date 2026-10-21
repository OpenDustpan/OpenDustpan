package apidesc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalidDocument means that a document is not one that Build writes: it
// has a method, a reference or a security entry that Read does not know, or
// another version of the format or of the description.
var ErrInvalidDocument = errors.New("apidesc: document is not valid")

// documentShape is the part of the document that Read needs.
type documentShape struct {
	OpenAPI string                                `json:"openapi"`
	Info    infoShape                             `json:"info"`
	Paths   map[string]map[string]json.RawMessage `json:"paths"`
}

// infoShape is the part of the info object that Read needs.
type infoShape struct {
	Version string `json:"version"`
}

// operationShape is the part of an operation object that Read needs.
//
//nolint:tagliatelle // OpenAPI fixes the names of these members.
type operationShape struct {
	OperationID string                   `json:"operationId"`
	Tags        []string                 `json:"tags"`
	Actions     []string                 `json:"x-dustpan-actions"`
	Security    *[]map[string][]string   `json:"security"`
	Parameters  []parameterShape         `json:"parameters"`
	Responses   map[string]responseShape `json:"responses"`
}

// responseShape is the part of a response object that Read needs.
type responseShape struct {
	Headers map[string]refShape `json:"headers"`
}

// refShape is a reference to a shared component.
type refShape struct {
	Ref string `json:"$ref"`
}

// parameterShape is a parameter: a path parameter or a reference to a shared
// parameter.
type parameterShape struct {
	Ref  string `json:"$ref"`
	Name string `json:"name"`
	In   string `json:"in"`
}

// Read returns the operations of a document that Build wrote. It fails
// closed: a document with a method that is not on the allowlist, a reference
// or a security entry that Build never writes, or a version of the format or
// of the description that differs from this package, gives an error and no
// operations.
//
// Read does not check the operations. Diff does, through Validate.
func Read(doc []byte) ([]Operation, error) {
	var shape documentShape

	dec := json.NewDecoder(bytes.NewReader(doc))
	if err := dec.Decode(&shape); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidDocument, err)
	}

	if shape.OpenAPI != OpenAPIVersion {
		return nil, fmt.Errorf("%w: openapi is %q, want %q", ErrInvalidDocument, shape.OpenAPI, OpenAPIVersion)
	}

	if shape.Info.Version != DocumentVersion {
		return nil, fmt.Errorf(
			"%w: info.version is %q, want %q", ErrInvalidDocument, shape.Info.Version, DocumentVersion,
		)
	}

	var ops []Operation

	for path, item := range shape.Paths {
		for method, raw := range item {
			op, err := readOperation(path, method, raw)
			if err != nil {
				return nil, err
			}

			ops = append(ops, op)
		}
	}

	slices.SortFunc(ops, func(a, b Operation) int { return strings.Compare(a.Key(), b.Key()) })

	return ops, nil
}

// readOperation reads one operation object.
func readOperation(path, method string, raw json.RawMessage) (Operation, error) {
	upper := strings.ToUpper(method)
	if method != strings.ToLower(method) || !allowedMethod(upper) {
		return Operation{}, fmt.Errorf("%w: %q on %q is not a method", ErrInvalidDocument, method, path)
	}

	var shape operationShape

	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&shape); err != nil {
		return Operation{}, fmt.Errorf("%w: %s %s: %w", ErrInvalidDocument, upper, path, err)
	}

	op := Operation{
		ID: shape.OperationID, Method: upper, Path: path, Tags: shape.Tags, Actions: shape.Actions,
		Public: false, IdempotencyKey: false, IfMatch: false, IfMatchRequired: false, ETag: false,
	}

	if err := readSecurity(&op, shape.Security); err != nil {
		return Operation{}, err
	}

	if err := readParameters(&op, shape.Parameters); err != nil {
		return Operation{}, err
	}

	op.ETag = shape.Responses["2XX"].Headers["ETag"].Ref == "#/components/headers/"+etagName

	return op, nil
}

// readSecurity sets the public flag. No security member means the default of
// the document, the bearer scheme. An empty list means a public operation.
func readSecurity(op *Operation, security *[]map[string][]string) error {
	switch {
	case security == nil:
		return nil
	case len(*security) == 0:
		op.Public = true

		return nil
	default:
		return fmt.Errorf(
			"%w: %s %s has a security entry that Build never writes",
			ErrInvalidDocument,
			op.Method,
			op.Path,
		)
	}
}

// readParameters sets the header flags from the references to the shared
// parameters. It refuses a reference that Build never writes.
func readParameters(op *Operation, params []parameterShape) error {
	for _, param := range params {
		switch {
		case param.Ref == "#/components/parameters/"+idempotencyName:
			op.IdempotencyKey = true
		case param.Ref == "#/components/parameters/"+ifMatchName:
			op.IfMatch = true
		case param.Ref == "#/components/parameters/"+ifMatchReqName:
			op.IfMatchRequired = true
		case param.Ref == "" && param.In == "path":
		default:
			return fmt.Errorf(
				"%w: %s %s has a parameter that Build never writes", ErrInvalidDocument, op.Method, op.Path,
			)
		}
	}

	return nil
}
