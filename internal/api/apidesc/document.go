package apidesc

import (
	"bytes"
	_ "embed" // The checked-in document is part of the binary.
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

// DocumentVersion is the version of the description: info.version. It is not
// the version of a build. It changes when the description changes, and the
// test of openapi.pin fails when it does not. It is valid in SemVer and in
// PEP 440, so each SDK can carry it.
const DocumentVersion = "0.23.0"

// OpenAPIVersion is the version of the OpenAPI format. The document uses the
// part of 3.1.0 that maps one to one to 3.0.3: a nullable type is
// [X, "null"], a nullable reference is oneOf [ref, null], and a type has no
// other form with more than one member.
const OpenAPIVersion = "3.1.0"

// Names in components. A generator turns them into the names of types and
// parameters in an SDK.
const (
	errorSchema     = "ErrorResponse"
	errorCodeSchema = "ErrorCode"
	bearerScheme    = "bearerAuth"
	cookieScheme    = "sessionCookie"
	csrfScheme      = "csrfHeader"
	idempotencyName = "IdempotencyKey"
	keyCode         = "code"
	keyMessage      = "message"
	keyErrorCode    = "errorCode"
	keyErrorArgs    = "errorArgs"
	ifMatchName     = "IfMatch"
	ifMatchReqName  = "IfMatchRequired"
	etagName        = "ETag"
	actionsKey      = "x-dustpan-actions"
)

// Members and values that the writer uses more than once.
const (
	keyDescription  = "description"
	keyType         = "type"
	keySchema       = "schema"
	keyName         = "name"
	keyRequired     = "required"
	keyContent      = "content"
	keyRef          = "$ref"
	typeString      = "string"
	typeObject      = "object"
	mediaJSON       = "application/json"
	mediaMergePatch = "application/merge-patch+json"
	refParameters   = "#/components/parameters/"
	refHeaders      = "#/components/headers/"
	refSchemas      = "#/components/schemas/"

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

// Option changes what Build writes.
type Option func(*buildConfig)

// buildConfig is what the options of Build set.
type buildConfig struct {
	errorCodes    []string
	errorVariants []ErrorVariant
}

// WithErrorCodes states the codes of the errorCode member of the error shape.
// The description then holds the schema ErrorCode with these codes, and
// errorCode is an open enum of it: a client keeps a code that it does not
// know. The caller passes the codes because this package cannot import the
// catalog of codes. Without this option, errorCode is a plain string.
func WithErrorCodes(codes []string) Option {
	return func(c *buildConfig) { c.errorCodes = slices.Clone(codes) }
}

// Build writes the description of the operations as an OpenAPI document. It
// writes the document with encoding/json, sorts the keys and ends the text
// with one line feed, so the same operations always give the same bytes. It
// refuses an operation that Validate refuses and a path that the format
// cannot hold, and it writes nothing then.
func Build(ops []Operation, opts ...Option) ([]byte, error) {
	var cfg buildConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	codes, err := checkedErrorCodes(cfg.errorCodes)
	if err != nil {
		return nil, err
	}

	if _, indexErr := index(ops); indexErr != nil {
		return nil, indexErr
	}

	schemas, err := bodySchemas(ops)
	if err != nil {
		return nil, err
	}

	variants, err := checkedErrorVariants(cfg.errorVariants, codes, schemas)
	if err != nil {
		return nil, err
	}

	paths := object{}

	for i := range ops {
		op := &ops[i]

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

		item[strings.ToLower(op.Method)] = operationObject(*op, failureSchema(variants))
	}

	doc := object{
		"openapi": OpenAPIVersion,
		"info":    infoObject(),
		"servers": []any{object{"url": "/"}},
		"tags":    tagList(ops),
		"security": []any{
			object{bearerScheme: []any{}},
			object{cookieScheme: []any{}, csrfScheme: []any{}},
		},
		"paths":      paths,
		"components": componentsObject(schemas, codes, variants),
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
			"A body has the schema of the Go type that the route names.",
	}
}

// bodySchemas checks the bodies of the operations and returns the schemas of
// the Go types that they name, by name.
func bodySchemas(ops []Operation) (object, error) {
	var bodies []Body

	for i := range ops {
		op := &ops[i]

		if err := op.ValidateBodies(); err != nil {
			return nil, err
		}

		b := op.bodies()
		bodies = append(bodies, b.Request, b.Response)
	}

	r := newReflector()
	if err := r.reflectBodies(bodies); err != nil {
		return nil, err
	}

	return r.schemas, nil
}

// componentsObject returns the parts that the operations share: the security
// scheme, the two request headers, the ETag answer header, the error shape and
// the schemas of the bodies.
func componentsObject(bodies object, codes []string, variants []ErrorVariant) object {
	return object{
		"securitySchemes": object{
			bearerScheme: object{
				keyType: "http", "scheme": "bearer",
				keyDescription: "An access token.",
			},
			cookieScheme: object{
				keyType: "apiKey", "in": "cookie", keyName: SessionCookie,
				keyDescription: "The session of the console. A request that uses it also sends the csrfHeader scheme.",
			},
			csrfScheme: object{
				keyType: "apiKey", "in": "header", keyName: CSRFHeader,
				keyDescription: "The CSRF value of the session of the console. A request with the session cookie " +
					"needs it when its method is not GET, HEAD or OPTIONS. Without it, or with a wrong value, " +
					"the answer is 403 with the errorCode session.csrf. The server ignores it on the other methods.",
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
		"schemas": withError(bodies, codes, variants),
	}
}

// withError returns the schemas of the bodies and the error shape. A body
// never takes the name of the error shape: the reflector refuses it.
func withError(bodies object, codes []string, variants []ErrorVariant) object {
	out := object{errorSchema: errorSchemaObject(codes)}
	maps.Copy(out, errorVariantSchemas(variants))
	if len(codes) > 0 {
		values := make([]any, len(codes))
		for i, code := range codes {
			values[i] = code
		}

		out[errorCodeSchema] = object{keyType: typeString, keyEnum: values}
	}

	maps.Copy(out, bodies)

	return out
}

// headerParameter returns a header parameter of components.parameters.
func headerParameter(name, description string, schema object, required bool) object {
	return object{
		keyName: name, "in": "header", keyRequired: required, keyDescription: description, keySchema: schema,
	}
}

// errorSchemaObject returns the schema of an error answer: the shape of the
// tool-neutral API.
func errorSchemaObject(codes []string) object {
	withCodes := len(codes) > 0

	errorCode := object{
		keyType:        typeString,
		keyDescription: "A stable code for a program. A 403 for a missing permission, an address outside the IP allowlist, a missing CSRF header or a request from another site has one: access.denied, access.ip-not-allowed, session.csrf or session.other-site.",
	}
	if withCodes {
		// The open enum form has no member beside anyOf, so the text sits on the error shape.
		errorCode = object{
			keyAnyOf: []any{object{keyRef: refSchemas + errorCodeSchema}, object{keyType: typeString}},
		}
	}

	out := object{
		keyType:     typeObject,
		keyRequired: []any{keyCode, keyMessage},
		"properties": object{
			keyCode:      object{keyType: typeInteger, keyDescription: "The HTTP status."},
			keyMessage:   object{keyType: typeString, keyDescription: "A text for a person."},
			keyErrorCode: errorCode,
			keyErrorArgs: object{
				keyType:                typeObject,
				keyDescription:         "The arguments of the text of the error.",
				"additionalProperties": true,
			},
		},
	}
	if withCodes {
		out[keyDescription] = "An error answer. The schema ErrorCode lists the codes that exist today. " +
			"A client must accept a code that it does not know, because the list only grows."
	}

	return out
}

// tagList returns the tags of the operations, each once, in sorted order.
func tagList(ops []Operation) []any {
	var names []string

	for i := range ops {
		op := &ops[i]

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

// failureSchema returns the name of the schema of the default answer of each
// operation: the error union when the description holds variants, and the
// error shape when it does not.
func failureSchema(variants []ErrorVariant) string {
	if len(variants) > 0 {
		return ErrorAnswerSchema
	}

	return errorSchema
}

// operationObject returns the operation object of one operation. failure is
// the name of the schema of its default answer.
func operationObject(op Operation, failure string) object {
	params := pathParameters(op.Path)

	for _, p := range sortedQuery(op.Query) {
		params = append(params, queryParameter(p))
	}

	if op.IdempotencyKey {
		params = append(params, object{keyRef: refParameters + idempotencyName})
	}

	if op.IfMatch {
		params = append(params, object{keyRef: refParameters + ifMatchName})
	}

	if op.IfMatchRequired {
		params = append(params, object{keyRef: refParameters + ifMatchReqName})
	}

	success := object{keyDescription: "The request succeeded."}
	if response := op.bodies().Response; response.kind == bodyTyped {
		success[keyContent] = object{mediaJSON: object{keySchema: response.ref()}}
	}

	if op.ETag {
		success["headers"] = object{"ETag": object{keyRef: refHeaders + etagName}}
	}

	responses := object{
		"default": object{
			keyDescription: "The request failed.",
			keyContent:     object{mediaJSON: object{keySchema: object{keyRef: refSchemas + failure}}},
		},
	}

	for _, status := range op.Statuses {
		responses[strconv.Itoa(status)] = success
	}

	out := object{
		"operationId": op.ID,
		"tags":        stringsAsAny(op.Tags),
		actionsKey:    actionList(op),
		"responses":   responses,
	}

	if len(params) > 0 {
		out["parameters"] = params
	}

	if body := requestBody(op); body != nil {
		out["requestBody"] = body
	}

	if op.Public {
		// An empty list switches the default off for this operation.
		out["security"] = []any{}
	}

	return out
}

// requestBody returns the body of the request of a method that sends one, or
// nil when the route says that it has none. A PATCH sends a merge patch.
func requestBody(op Operation) object {
	request := op.bodies().Request
	if request.kind != bodyTyped {
		return nil
	}

	media := mediaJSON
	if op.Method == http.MethodPatch {
		media = mediaMergePatch
	}

	return object{keyRequired: true, keyContent: object{media: object{keySchema: request.ref()}}}
}

// actionList returns the actions of the operation, each with the kind of its
// object, in the order of the operation.
func actionList(op Operation) []any {
	out := make([]any, len(op.Actions))
	for i, action := range op.Actions {
		out[i] = object{"action": action, "object": op.ActionObjects[action]}
	}

	return out
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

// checkedErrorCodes returns the codes sorted. It refuses a code that is not
// in the form of an enum value and a code that is listed twice.
func checkedErrorCodes(codes []string) ([]string, error) {
	out := slices.Clone(codes)
	slices.Sort(out)

	for i, code := range out {
		if !enumValue.MatchString(code) {
			return nil, fmt.Errorf("%w: error code %q is not in the form of an enum value", ErrInvalidBody, code)
		}

		if i > 0 && out[i-1] == code {
			return nil, fmt.Errorf("%w: error code %q is listed twice", ErrInvalidBody, code)
		}
	}

	return out, nil
}
