package clientgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// MaxDescriptionBytes is the largest description that the parser reads
// (8 MiB).
const MaxDescriptionBytes = 8388608

// The names that the parser accepts, as the allowlists of the subset.
var (
	operationIDName = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	schemaName      = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	fieldName       = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	pathParamName   = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)
	pathLiteral     = regexp.MustCompile(`^[a-z0-9]+([.-][a-z0-9]+)*$`)
	enumValueName   = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*([._:-][a-zA-Z0-9]+)*$`)
	actionName      = regexp.MustCompile(`^[a-z][a-z0-9]*:[a-z][a-z0-9-]*$`)
)

// objectKinds returns the values that x-dustpan-actions names as the object
// of an action.
func objectKinds() []string {
	return []string{"namespace", "unit", "collection", "caller", "server"}
}

// sharedParameter maps the name of a shared parameter component to the
// header that it stands for.
func sharedParameter(name string) (string, model.IfMatch, bool) {
	switch name {
	case "IdempotencyKey":
		return "Idempotency-Key", model.IfMatchNone, true
	case "IfMatch":
		return "If-Match", model.IfMatchOptional, true
	case "IfMatchRequired":
		return "If-Match", model.IfMatchRequired, true
	default:
		return "", model.IfMatchNone, false
	}
}

// errorSchemaName is the schema of each error answer.
const errorSchemaName = "ErrorResponse"

// descriptionParser is the implementation of Parser for the subset of
// OpenAPI that the description uses.
type descriptionParser struct {
	selected []string
}

// NewParser returns the parser of the subset of OpenAPI 3.1 that the
// description uses. It never reads a construct outside that subset: it returns
// ErrUnsupported, wrapped, with the JSON pointer of the construct.
//
// With operation IDs, the parser reads only those operations and the schemas
// that they reach, so a client can cover a part of the API. An ID that the
// description does not hold is ErrInvalidDescription. Without IDs, the parser
// reads every operation.
//
//nolint:ireturn // The port is the contract; callers do not depend on the type.
func NewParser(operationIDs ...string) Parser {
	selected := slices.Clone(operationIDs)
	slices.Sort(selected)

	return &descriptionParser{selected: slices.Compact(selected)}
}

// parseState holds the schemas that the walk found, by name.
type parseState struct {
	doc      object
	schemas  map[string]*model.Schema
	visiting map[string]bool
	// openUse and closedUse record the enum schemas that a field uses as an
	// open enum and as a closed one. An enum in both sets is an error.
	openUse   map[string]bool
	closedUse map[string]bool
}

// Parse implements Parser.
func (p *descriptionParser) Parse(raw []byte) (*model.API, error) {
	if len(raw) > MaxDescriptionBytes {
		return nil, fmt.Errorf("%w: the description has %d bytes and the limit is %d",
			ErrInvalidDescription, len(raw), MaxDescriptionBytes)
	}

	doc, err := decodeDocument(raw)
	if err != nil {
		return nil, err
	}

	st := &parseState{
		doc: doc, schemas: make(map[string]*model.Schema), visiting: make(map[string]bool),
		openUse: make(map[string]bool), closedUse: make(map[string]bool),
	}

	return st.api(p.selected)
}

// decodeDocument decodes the one JSON value of a description. Numbers stay
// exact, so that the parser reads an integer limit without a rounding step.
func decodeDocument(raw []byte) (object, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("%w: JSON: %w", ErrInvalidDescription, err)
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: data follows the document", ErrInvalidDescription)
	}

	return asObject("", v)
}

// rootKeys are the keys of the document.
func rootKeys() []string {
	return []string{"openapi", "info", "servers", "tags", "paths", "components", "security"}
}

// api builds the model from the document.
func (st *parseState) api(selected []string) (*model.API, error) {
	if err := checkKeys("", st.doc, rootKeys()...); err != nil {
		return nil, err
	}

	if v, err := stringAt(st.doc, "", "openapi"); err != nil || v != "3.1.0" {
		return nil, fmt.Errorf("%w: openapi must be 3.1.0", ErrInvalidDescription)
	}

	api := new(model.API)
	api.Error = errorSchemaName

	if err := st.info(api); err != nil {
		return nil, err
	}

	if err := st.checkEnvelope(); err != nil {
		return nil, err
	}

	var err error
	if api.Tags, err = st.tags(); err != nil {
		return nil, err
	}

	if api.Operations, err = st.operations(selected); err != nil {
		return nil, err
	}

	err = st.errorSchema()
	if err != nil {
		return nil, err
	}

	if api.Schemas, err = st.sortedSchemas(); err != nil {
		return nil, err
	}

	return api, nil
}

// info reads the title and the version of the description.
func (st *parseState) info(api *model.API) error {
	info, err := objectAt(st.doc, "", "info")
	if err != nil {
		return err
	}

	err = checkKeys("/info", info, "title", "version", "description")
	if err != nil {
		return err
	}

	if api.Title, err = stringAt(info, "/info", "title"); err != nil {
		return err
	}

	if api.Version, err = stringAt(info, "/info", "version"); err != nil {
		return err
	}

	if api.Version == "" {
		return fmt.Errorf("%w: /info/version is empty", ErrInvalidDescription)
	}

	return nil
}

// checkEnvelope checks the parts of the document that the model does not hold:
// servers, the top-level security list and the components other than schemas.
func (st *parseState) checkEnvelope() error {
	if servers, ok := st.doc["servers"]; ok {
		items, err := asList("/servers", servers)
		if err != nil {
			return err
		}

		for i, item := range items {
			ptr := ptrJoin("/servers", strconv.Itoa(i))

			o, oErr := asObject(ptr, item)
			if oErr != nil {
				return oErr
			}

			if err := checkKeys(ptr, o, "url", "description"); err != nil {
				return err
			}
		}
	}

	if sec, ok := st.doc["security"]; ok {
		if _, err := asList("/security", sec); err != nil {
			return err
		}
	}

	return st.checkComponents()
}

// checkComponents checks the keys of the components and the shared parts.
func (st *parseState) checkComponents() error {
	comps, err := objectAt(st.doc, "", "components")
	if err != nil {
		return err
	}

	err = checkKeys("/components", comps, "schemas", "parameters", "headers", "securitySchemes")
	if err != nil {
		return err
	}

	params, err := optionalObjectAt(comps, "/components", "parameters")
	if err != nil {
		return err
	}

	for _, name := range sortedKeys(params) {
		if _, _, ok := sharedParameter(name); !ok {
			return unsupported(ptrJoin("/components/parameters", name), "shared parameter")
		}
	}

	headers, err := optionalObjectAt(comps, "/components", "headers")
	if err != nil {
		return err
	}

	for _, name := range sortedKeys(headers) {
		if name != "ETag" {
			return unsupported(ptrJoin("/components/headers", name), "shared header")
		}
	}

	return nil
}

// tags reads the tags in the order of their names.
func (st *parseState) tags() ([]model.Tag, error) {
	raw, ok := st.doc["tags"]
	if !ok {
		return nil, nil
	}

	items, err := asList("/tags", raw)
	if err != nil {
		return nil, err
	}

	tags := make([]model.Tag, 0, len(items))

	for i, item := range items {
		ptr := ptrJoin("/tags", strconv.Itoa(i))

		o, oErr := asObject(ptr, item)
		if oErr != nil {
			return nil, oErr
		}

		err = checkKeys(ptr, o, "name", "description")
		if err != nil {
			return nil, err
		}

		var tag model.Tag
		if tag.Name, err = stringAt(o, ptr, "name"); err != nil {
			return nil, err
		}

		if tag.Description, err = optionalString(o, ptr, "description"); err != nil {
			return nil, err
		}

		tags = append(tags, tag)
	}

	slices.SortFunc(tags, func(a, b model.Tag) int { return strings.Compare(a.Name, b.Name) })

	return tags, nil
}

// errorSchema reads the schema of the error answer, so that it is in the model
// whatever the operations reach.
func (st *parseState) errorSchema() error {
	if err := st.namedSchema(errorSchemaName); err != nil {
		return err
	}

	err := checkErrorShape(ptrJoin("/components/schemas", errorSchemaName), st.schemas[errorSchemaName], st)
	if err != nil {
		return err
	}

	return st.errorAnswer()
}

// errorAnswer reads the union of the error schemas by errorCode, when the
// description holds it. Its base must be the error schema, because every
// transport reads that shape.
func (st *parseState) errorAnswer() error {
	comps, err := objectAt(st.doc, "", "components")
	if err != nil {
		return err
	}

	all, err := objectAt(comps, "/components", "schemas")
	if err != nil {
		return err
	}

	if _, ok := all[errorAnswerName]; !ok {
		return nil
	}

	if err := st.useSchema(ptrJoin("/components/schemas", errorAnswerName), errorAnswerName); err != nil {
		return err
	}

	u := st.schemas[errorAnswerName]
	if u == nil || u.Kind != model.KindUnion || u.Base != errorSchemaName {
		return invalid(
			ptrJoin("/components/schemas", errorAnswerName),
			"the error union must be a union with the error schema as its base",
		)
	}

	return nil
}

// checkErrorShape checks that the error schema has the members that every
// transport reads: code (integer, required), message (string, required),
// errorCode (a string or an open enum, optional) and errorArgs (an object with free keys,
// optional). A client decodes the error answer in the same way for each
// operation, so a description with another shape is refused here and not at
// the compile or run time of a client.
func checkErrorShape(ptr string, s *model.Schema, st *parseState) error {
	if s == nil || s.Kind != model.KindObject || s.Extra != nil {
		return invalid(ptr, "the error schema must be an object with no additional properties")
	}

	want := errorMembers()
	if len(s.Fields) != len(want) {
		return invalid(ptr, "the error schema must have the members code, message, errorCode and errorArgs")
	}

	for i := range s.Fields {
		f := &s.Fields[i]
		fptr := ptrJoin(ptrJoin(ptr, "properties"), f.Name)

		w, ok := want[f.Name]
		if !ok {
			return invalid(fptr, "the error schema has a member that is not code, message, errorCode or errorArgs")
		}

		if err := checkErrorMember(fptr, *f, w, st); err != nil {
			return err
		}
	}

	return nil
}

// errorMember is what a transport needs of one member of the error schema.
type errorMember struct {
	kind     model.TypeKind
	required bool
}

// errorMembers returns the members of the error schema, by wire name.
func errorMembers() map[string]errorMember {
	return map[string]errorMember{
		"code":      {model.TypeInteger, true},
		"message":   {model.TypeString, true},
		"errorCode": {model.TypeString, false},
		"errorArgs": {model.TypeMap, false},
	}
}

// checkErrorMember checks the type and the presence of one member.
func checkErrorMember(ptr string, f model.Field, w errorMember, st *parseState) error {
	if f.Required != w.required {
		return invalid(ptr, "the member of the error schema has the wrong required setting")
	}

	t := f.Type

	if f.Name == "errorCode" && t.Kind == model.TypeRef {
		return st.checkErrorCodeRef(ptr, t)
	}

	if t.Kind != w.kind || t.Nullable || t.Format != "" {
		return invalid(ptr, "the member of the error schema has the wrong type")
	}

	if w.kind == model.TypeMap && (t.Elem == nil || t.Elem.Kind != model.TypeAny) {
		return invalid(ptr, "errorArgs must be an object with additionalProperties true")
	}

	return nil
}

// sortedSchemas returns the schemas found, in the order of their names.
func (st *parseState) sortedSchemas() ([]model.Schema, error) {
	names := make([]string, 0, len(st.schemas))
	for name := range st.schemas {
		names = append(names, name)
	}

	slices.Sort(names)

	out := make([]model.Schema, 0, len(names))

	for _, name := range names {
		if st.openUse[name] && st.closedUse[name] {
			return nil, invalid(
				ptrJoin("/components/schemas", name),
				"the enum is open in one field and closed in another",
			)
		}

		schema := st.schemas[name]
		if schema == nil {
			return nil, invalid(ptrJoin("/components/schemas", name), "the schema was not read")
		}

		s := *schema
		s.Open = st.openUse[name]
		out = append(out, s)
	}

	return out, nil
}

// checkErrorCodeRef checks that errorCode, when it is a reference, is an open
// enum of strings. The wire value is still a string, so a transport reads it
// as one.
func (st *parseState) checkErrorCodeRef(ptr string, t model.Type) error {
	e := st.schemas[t.Ref]
	if e == nil || e.Kind != model.KindEnum || t.Nullable || !st.openUse[t.Ref] {
		return invalid(ptr, "errorCode must be a string or an open enum")
	}

	return nil
}
