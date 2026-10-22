package apidesc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
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
	Actions     []actionShape            `json:"x-dustpan-actions"`
	Security    *[]map[string][]string   `json:"security"`
	Parameters  []parameterShape         `json:"parameters"`
	Responses   map[string]responseShape `json:"responses"`
}

// actionShape is one action with the kind of its object.
type actionShape struct {
	Action string `json:"action"`
	Object string `json:"object"`
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
	Ref         string       `json:"$ref"`
	Name        string       `json:"name"`
	In          string       `json:"in"`
	Required    bool         `json:"required"`
	Description string       `json:"description"`
	Schema      *schemaShape `json:"schema"`
}

// schemaShape is the schema of a query parameter: a scalar, or an array of a
// scalar.
type schemaShape struct {
	Type   string       `json:"type"`
	Format string       `json:"format"`
	Items  *schemaShape `json:"items"`
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
		ID: shape.OperationID, Method: upper, Path: path, Tags: shape.Tags, Actions: nil, ActionObjects: nil,
		Public: false, IdempotencyKey: false, IfMatch: false, IfMatchRequired: false, ETag: false,
		Query: nil, Status: 0, Bodies: nil,
	}

	readActions(&op, shape.Actions)

	if err := readSecurity(&op, shape.Security); err != nil {
		return Operation{}, err
	}

	if err := readParameters(&op, shape.Parameters); err != nil {
		return Operation{}, err
	}

	status, success, err := readSuccess(path, method, shape.Responses)
	if err != nil {
		return Operation{}, err
	}

	op.Status = status
	op.ETag = success.Headers["ETag"].Ref == "#/components/headers/"+etagName

	return op, nil
}

// readSuccess returns the success status and the success answer of an
// operation. The key is 2XX or a status from 200 to 299, and an operation
// has exactly one. The status of 2XX is zero.
func readSuccess(path, method string, responses map[string]responseShape) (int, responseShape, error) {
	var (
		status  int
		success responseShape
		found   bool
	)

	for key, response := range responses {
		if key == "default" {
			continue
		}

		code := 0

		if key != "2XX" {
			n, err := strconv.Atoi(key)
			if err != nil || n < http.StatusOK || n >= http.StatusMultipleChoices {
				return 0, responseShape{}, fmt.Errorf(
					"%w: %s %s has the response key %q", ErrInvalidDocument, strings.ToUpper(method), path, key,
				)
			}

			code = n
		}

		if found {
			return 0, responseShape{}, fmt.Errorf(
				"%w: %s %s has more than one success response", ErrInvalidDocument, strings.ToUpper(method), path,
			)
		}

		status, success, found = code, response, true
	}

	if !found {
		return 0, responseShape{}, fmt.Errorf(
			"%w: %s %s has no success response", ErrInvalidDocument, strings.ToUpper(method), path,
		)
	}

	return status, success, nil
}

// readActions sets the actions and their object kinds, in the order of the
// document. A public operation has an empty list and an empty map.
func readActions(op *Operation, actions []actionShape) {
	op.ActionObjects = make(map[string]string, len(actions))

	for _, a := range actions {
		op.Actions = append(op.Actions, a.Action)
		op.ActionObjects[a.Action] = a.Object
	}
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
		case param.Ref == "" && param.In == "query":
			p, err := readQuery(param)
			if err != nil {
				return fmt.Errorf("%w: %s %s: %w", ErrInvalidDocument, op.Method, op.Path, err)
			}

			op.Query = append(op.Query, p)
		default:
			return fmt.Errorf(
				"%w: %s %s has a parameter that Build never writes", ErrInvalidDocument, op.Method, op.Path,
			)
		}
	}

	return nil
}

// errQueryShape means that a query parameter has a schema that Build never
// writes.
var errQueryShape = errors.New("a query parameter has a schema that Build never writes")

// readQuery reads one query parameter. It refuses a schema that Build never
// writes: a type with no scalar, an array of an array, and a format other
// than the one of an integer.
func readQuery(param parameterShape) (QueryParam, error) {
	schema := param.Schema
	if schema == nil {
		return QueryParam{}, fmt.Errorf("%w: %q", errQueryShape, param.Name)
	}

	repeated := schema.Type == "array"
	if repeated {
		if schema.Format != "" || schema.Items == nil || schema.Items.Items != nil {
			return QueryParam{}, fmt.Errorf("%w: %q", errQueryShape, param.Name)
		}

		schema = schema.Items
	}

	wantFormat := ""
	if schema.Type == QueryInteger {
		wantFormat = "int64"
	}

	if schema.Format != wantFormat {
		return QueryParam{}, fmt.Errorf("%w: %q", errQueryShape, param.Name)
	}

	return QueryParam{
		Name: param.Name, Type: schema.Type, Repeated: repeated, Required: param.Required,
		Description: param.Description,
	}, nil
}
