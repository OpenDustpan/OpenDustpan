package apidesc

import (
	"bytes"
	_ "embed" // The checked-in document is part of the binary.
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// DocumentVersion is the version of the description: info.version. It is not
// the version of a build. It changes when the description changes. It is
// valid in SemVer and in PEP 440, so each SDK can carry it.
const DocumentVersion = "0.1.0"

// OpenAPIVersion is the version of the OpenAPI format. The document uses the
// part of 3.1.0 that maps one to one to 3.0.3: a nullable type is
// [X, "null"], a nullable reference is oneOf [ref, null], and a type has no
// other form with more than one member.
const OpenAPIVersion = "3.1.0"

// Names in components. A generator turns them into the names of types and
// parameters in an SDK.
const (
	errorSchema     = "ErrorResponse"
	bearerScheme    = "bearerAuth"
	idempotencyName = "IdempotencyKey"
	ifMatchName     = "IfMatch"
	ifMatchReqName  = "IfMatchRequired"
	etagName        = "ETag"
	actionsKey      = "x-dustpan-actions"
)

// Members and values that the writer uses more than once.
const (
	keyDescription = "description"
	keyType        = "type"
	keySchema      = "schema"
	keyName        = "name"
	keyRequired    = "required"
	keyContent     = "content"
	keyRef         = "$ref"
	typeString     = "string"
	typeObject     = "object"
	mediaJSON      = "application/json"
	refParameters  = "#/components/parameters/"
	refHeaders     = "#/components/headers/"
	refSchemas     = "#/components/schemas/"

	// maxIdempotencyKeyLength is the longest Idempotency-Key that the server
	// accepts.
	maxIdempotencyKeyLength = 255
)

//go:embed openapi.json
var checkedIn []byte

// Document returns the checked-in description: the bytes that the server
// serves on each build, whatever modules are on. The caller gets a copy.
func Document() []byte {
	return bytes.Clone(checkedIn)
}

// object is a JSON object. encoding/json writes the keys of a map in sorted
// order, so the bytes of a document depend on its content only.
type object = map[string]any

// Build writes the description of the operations as an OpenAPI document. It
// writes the document with encoding/json, sorts the keys and ends the text
// with one line feed, so the same operations always give the same bytes. It
// refuses an operation that Validate refuses and a path that the format
// cannot hold, and it writes nothing then.
func Build(ops []Operation) ([]byte, error) {
	if _, err := index(ops); err != nil {
		return nil, err
	}

	paths := object{}

	for _, op := range ops {
		if strings.Contains(op.Path, "{$}") || strings.Contains(op.Path, "...}") {
			return nil, fmt.Errorf(
				"%w: path %q holds a wildcard that OpenAPI cannot hold",
				ErrInvalidOperation,
				op.Path,
			)
		}

		item, ok := paths[op.Path].(object)
		if !ok {
			item = object{}
			paths[op.Path] = item
		}

		item[strings.ToLower(op.Method)] = operationObject(op)
	}

	doc := object{
		"openapi":    OpenAPIVersion,
		"info":       infoObject(),
		"servers":    []any{object{"url": "/"}},
		"tags":       tagList(ops),
		"security":   []any{object{bearerScheme: []any{}}},
		"paths":      paths,
		"components": componentsObject(),
	}

	var buf bytes.Buffer

	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")

	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("write the description: %w", err)
	}

	return buf.Bytes(), nil
}

// infoObject returns the info object. The version is the version of the
// description.
func infoObject() object {
	return object{
		"title":   "OpenDustpan REST API",
		"version": DocumentVersion,
		keyDescription: "The tool-neutral REST API of an OpenDustpan server. " +
			"The description covers the paths under " + Prefix + " only. " +
			"A body is a generic JSON object.",
	}
}

// componentsObject returns the parts that the operations share: the security
// scheme, the two request headers, the ETag answer header and the error shape.
func componentsObject() object {
	return object{
		"securitySchemes": object{
			bearerScheme: object{
				keyType: "http", "scheme": "bearer",
				keyDescription: "An access token or a session of the console.",
			},
		},
		"parameters": object{
			idempotencyName: headerParameter(
				"Idempotency-Key",
				"A string of 1 to 255 printable ASCII characters. "+
					"A request with the same key and the same body gets the first answer again.",
				object{keyType: typeString, "minLength": 1, "maxLength": maxIdempotencyKeyLength},
				false,
			),
			ifMatchName: headerParameter(
				"If-Match",
				"The ETag of the version that the request replaces. A version that does not match answers 412.",
				object{keyType: typeString},
				false,
			),
			ifMatchReqName: headerParameter(
				"If-Match",
				"The ETag of the version that the request replaces. The request needs it: without it the answer is 428. "+
					"A version that does not match answers 412.",
				object{keyType: typeString},
				true,
			),
		},
		"headers": object{
			etagName: object{
				keyDescription: "The version of the resource, in quotes.",
				keySchema:      object{keyType: typeString},
			},
		},
		"schemas": object{errorSchema: errorSchemaObject()},
	}
}

// headerParameter returns a header parameter of components.parameters.
func headerParameter(name, description string, schema object, required bool) object {
	return object{
		keyName: name, "in": "header", keyRequired: required, keyDescription: description, keySchema: schema,
	}
}

// errorSchemaObject returns the schema of an error answer: the shape of the
// tool-neutral API.
func errorSchemaObject() object {
	return object{
		keyType:     typeObject,
		keyRequired: []any{"code", "message"},
		"properties": object{
			"code":      object{keyType: "integer", keyDescription: "The HTTP status."},
			"message":   object{keyType: typeString, keyDescription: "A text for a person."},
			"errorCode": object{keyType: typeString, keyDescription: "A stable code for a program."},
			"errorArgs": object{
				keyType:                typeObject,
				keyDescription:         "The arguments of the text of the error.",
				"additionalProperties": true,
			},
		},
	}
}

// tagList returns the tags of the operations, each once, in sorted order.
func tagList(ops []Operation) []any {
	var names []string

	for _, op := range ops {
		for _, tag := range op.Tags {
			if !slices.Contains(names, tag) {
				names = append(names, tag)
			}
		}
	}

	slices.Sort(names)

	out := make([]any, len(names))
	for i, name := range names {
		out[i] = object{keyName: name}
	}

	return out
}

// jsonBody is the generic body: a JSON object with any member.
func jsonBody() object {
	return object{keyType: typeObject, "additionalProperties": true}
}

// operationObject returns the operation object of one operation.
func operationObject(op Operation) object {
	params := pathParameters(op.Path)

	if op.IdempotencyKey {
		params = append(params, object{keyRef: refParameters + idempotencyName})
	}

	if op.IfMatch {
		params = append(params, object{keyRef: refParameters + ifMatchName})
	}

	if op.IfMatchRequired {
		params = append(params, object{keyRef: refParameters + ifMatchReqName})
	}

	success := object{
		keyDescription: "The request succeeded.",
		keyContent:     object{mediaJSON: object{keySchema: jsonBody()}},
	}

	if op.ETag {
		success["headers"] = object{"ETag": object{keyRef: refHeaders + etagName}}
	}

	out := object{
		"operationId": op.ID,
		"tags":        stringsAsAny(op.Tags),
		actionsKey:    stringsAsAny(op.Actions),
		"responses": object{
			"2XX": success,
			"default": object{
				keyDescription: "The request failed.",
				keyContent:     object{mediaJSON: object{keySchema: object{keyRef: refSchemas + errorSchema}}},
			},
		},
	}

	if len(params) > 0 {
		out["parameters"] = params
	}

	if body := requestBody(op.Method); body != nil {
		out["requestBody"] = body
	}

	if op.Public {
		// An empty list switches the default off for this operation.
		out["security"] = []any{}
	}

	return out
}

// requestBody returns the generic body of a method that sends one, or nil. A
// PATCH sends a merge patch.
func requestBody(method string) object {
	switch method {
	case "POST", "PUT":
		return object{keyContent: object{mediaJSON: object{keySchema: jsonBody()}}}
	case "PATCH":
		return object{keyContent: object{"application/merge-patch+json": object{keySchema: jsonBody()}}}
	default:
		return nil
	}
}

// pathParameters returns one path parameter for each wildcard of the path, in
// the order of the path.
func pathParameters(path string) []any {
	var out []any

	for seg := range strings.SplitSeq(path, "/") {
		name, ok := strings.CutPrefix(seg, "{")
		if !ok {
			continue
		}

		name = strings.TrimSuffix(name, "}")

		out = append(out, object{
			keyName: name, "in": "path", keyRequired: true, keySchema: object{keyType: typeString},
		})
	}

	return out
}

// stringsAsAny returns a copy of a list as a JSON array. A nil list gives an
// empty array, never null.
func stringsAsAny(list []string) []any {
	out := make([]any, len(list))
	for i, s := range list {
		out[i] = s
	}

	return out
}
