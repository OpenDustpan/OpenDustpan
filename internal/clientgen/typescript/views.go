package typescript

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// The TypeScript names that the views use more than once.
const (
	stringType  = "string"
	unknownType = "unknown"
	anyDecoder  = "anyValue"
	useNone     = "none"
)

// typeKind is the form of a named schema in types.ts.
type typeKind int

const (
	_ typeKind = iota // Zero means that nobody chose.
	// kindInterface is an object with named members.
	kindInterface
	// kindRecord is an object with free keys and no named member.
	kindRecord
	// kindClosedEnum is a string with a list of values that no other value joins.
	kindClosedEnum
	// kindOpenEnum is a string with a list of known values and room for new ones.
	kindOpenEnum
)

type typeView struct {
	Name string
	Doc  string
	Kind typeKind
	// Fields are the members of an interface.
	Fields []fieldView
	// Values are the string literals of an enum.
	Values []string
	// Record is the TypeScript type and the decoder of the values of a record.
	RecordType    string
	RecordDecoder string
}

func (t typeView) IsInterface() bool  { return t.Kind == kindInterface }
func (t typeView) IsRecord() bool     { return t.Kind == kindRecord }
func (t typeView) IsClosedEnum() bool { return t.Kind == kindClosedEnum }
func (t typeView) IsOpenEnum() bool   { return t.Kind == kindOpenEnum }

type fieldView struct {
	// Name is the property key, quoted when it needs quotes.
	Name     string
	Type     string
	Decoder  string
	Doc      string
	Optional bool
}

// addTypes builds the views of the schemas.
func (d *data) addTypes(api *model.API, seen map[string]string) error {
	for i := range api.Schemas {
		s := &api.Schemas[i]

		if err := claim(seen, s.Name, "schema "+s.Name, clientgen.ErrInvalidName); err != nil {
			return err
		}

		tv, err := typeOf(s)
		if err != nil {
			return err
		}

		d.Types = append(d.Types, tv)
	}

	return nil
}

// typeOf builds the view of one schema.
func typeOf(s *model.Schema) (typeView, error) {
	tv := typeView{
		Name: s.Name, Doc: s.Description, Kind: 0, Fields: nil, Values: nil, RecordType: "", RecordDecoder: "",
	}

	switch s.Kind {
	case model.KindEnum:
		tv.Kind = kindClosedEnum
		if s.Open {
			tv.Kind = kindOpenEnum
		}

		for _, v := range s.Values {
			tv.Values = append(tv.Values, tsString(v))
		}

		return tv, nil
	case model.KindObject:
		return objectOf(s, tv)
	default:
		return typeView{}, fmt.Errorf("%w: schema %s has an unknown kind", clientgen.ErrInvalidDescription, s.Name)
	}
}

// objectOf builds the view of an object schema.
func objectOf(s *model.Schema, tv typeView) (typeView, error) {
	if s.Extra != nil {
		if len(s.Fields) > 0 {
			return typeView{}, fmt.Errorf(
				"%w: schema %s has members and additional properties, which the TypeScript template set does not write",
				clientgen.ErrUnsupported, s.Name,
			)
		}

		tv.Kind = kindRecord

		var err error

		if tv.RecordType, err = typeExpr(*s.Extra); err != nil {
			return typeView{}, fmt.Errorf("schema %s: %w", s.Name, err)
		}

		if tv.RecordDecoder, err = decoderExpr(*s.Extra); err != nil {
			return typeView{}, fmt.Errorf("schema %s: %w", s.Name, err)
		}

		return tv, nil
	}

	tv.Kind = kindInterface

	for _, f := range s.Fields {
		typ, err := typeExpr(f.Type)
		if err != nil {
			return typeView{}, fmt.Errorf("schema %s, member %s: %w", s.Name, f.Name, err)
		}

		dec, err := decoderExpr(f.Type)
		if err != nil {
			return typeView{}, fmt.Errorf("schema %s, member %s: %w", s.Name, f.Name, err)
		}

		tv.Fields = append(tv.Fields, fieldView{
			Name: propName(f.Name), Type: typ, Decoder: dec, Doc: f.Description, Optional: !f.Required,
		})
	}

	return tv, nil
}

// typeExpr writes a model type as a TypeScript type. It refuses a kind or a
// format that the template set cannot check, so that a type never gets a
// weaker check than the description asks for.
func typeExpr(t model.Type) (string, error) {
	base, err := baseExpr(t)
	if err != nil {
		return "", err
	}

	if t.Nullable && t.Kind != model.TypeAny {
		return base + " | null", nil
	}

	return base, nil
}

// knownFormats lists the formats that the validator of a kind checks. An
// unknown kind has none, so that nothing passes for it.
func knownFormats(kind model.TypeKind) []string {
	switch kind {
	case model.TypeString:
		return []string{"", "date-time"}
	case model.TypeInteger:
		return []string{"", "int64"}
	case model.TypeNumber:
		return []string{"", "float", "double"}
	case model.TypeBoolean, model.TypeArray, model.TypeMap, model.TypeRef, model.TypeAny:
		return []string{""}
	default:
		return nil
	}
}

// checkFormat refuses a format that the validator of the kind does not check.
func checkFormat(t model.Type) error {
	if !slices.Contains(knownFormats(t.Kind), t.Format) {
		return fmt.Errorf(
			"%w: the TypeScript template set does not check the format %q of a type of kind %d",
			clientgen.ErrUnsupported, t.Format, t.Kind,
		)
	}

	return nil
}

// baseExpr writes the TypeScript type of a model type, without null.
func baseExpr(t model.Type) (string, error) {
	if err := checkFormat(t); err != nil {
		return "", err
	}

	switch t.Kind {
	case model.TypeString:
		return stringType, nil
	case model.TypeInteger, model.TypeNumber:
		return "number", nil
	case model.TypeBoolean:
		return "boolean", nil
	case model.TypeArray:
		elem, err := elemExpr(t)

		return "ReadonlyArray<" + elem + ">", err
	case model.TypeMap:
		elem, err := elemExpr(t)

		return "Readonly<Record<string, " + elem + ">>", err
	case model.TypeRef:
		return t.Ref, nil
	case model.TypeAny:
		return unknownType, nil
	}

	return "", fmt.Errorf("%w: the TypeScript template set has no type for kind %d", clientgen.ErrUnsupported, t.Kind)
}

// elemExpr writes the type of the items of an array or the values of a map.
func elemExpr(t model.Type) (string, error) {
	if t.Elem == nil {
		return "", fmt.Errorf("%w: an array or a map has no item type", clientgen.ErrUnsupported)
	}

	return typeExpr(*t.Elem)
}

// decoderExpr writes the validator of a model type as an expression of the
// combinators of the runtime.
func decoderExpr(t model.Type) (string, error) {
	expr, err := baseDecoder(t)
	if err != nil {
		return "", err
	}

	if t.Nullable && t.Kind != model.TypeAny {
		return "nullable(" + expr + ")", nil
	}

	return expr, nil
}

// baseDecoder writes the validator of a model type, without null.
func baseDecoder(t model.Type) (string, error) {
	if err := checkFormat(t); err != nil {
		return "", err
	}

	switch t.Kind {
	case model.TypeString:
		if t.Format == "date-time" {
			return "dateTime", nil
		}

		return stringType, nil
	case model.TypeInteger:
		return "int64", nil
	case model.TypeNumber:
		return "number", nil
	case model.TypeBoolean:
		return "boolean", nil
	case model.TypeArray:
		elem, err := elemDecoder(t)

		return "array(" + elem + ")", err
	case model.TypeMap:
		elem, err := elemDecoder(t)

		return "record(" + elem + ")", err
	case model.TypeRef:
		return "ref(() => decode" + t.Ref + ")", nil
	case model.TypeAny:
		// The description says explicitly that the value is any JSON value.
		return anyDecoder, nil
	}

	return "", fmt.Errorf(
		"%w: the TypeScript template set has no validator for kind %d",
		clientgen.ErrUnsupported,
		t.Kind,
	)
}

func elemDecoder(t model.Type) (string, error) {
	if t.Elem == nil {
		return "", fmt.Errorf("%w: an array or a map has no item type", clientgen.ErrUnsupported)
	}

	return decoderExpr(*t.Elem)
}

type memberView struct {
	// Name is the property key, quoted when it needs quotes.
	Name     string
	Type     string
	Doc      string
	Optional bool
}

type operationView struct {
	ID    string
	Doc   string
	Input string
	// Result is the TypeScript type of the body of the answer, void for none.
	Result string
	// Decoder is the validator of the body, or undefined.
	Decoder string

	Method      string
	Path        string
	PathParams  string
	QueryParams string
	// RequiredQuery are the names of the query parameters that a call must give.
	RequiredQuery string
	Security      string
	Idempotency   string
	IfMatch       string
	RequestBody   string
	BodyRequired  bool
	Statuses      string

	// PathMembers, QueryMembers and Body build the input type.
	PathMembers  []memberView
	QueryMembers []memberView
	QueryNeeded  bool
	Body         *memberView
	// InputOptional says that every member of the input is optional, so a call
	// can leave it out.
	InputOptional bool
	TakesIfMatch  bool
	// IfMatchNeeded says that a call must give If-Match.
	IfMatchNeeded bool
	TakesKey      bool
}

// reservedMethods are the names that an operation must not take, because the
// client class uses them.
func reservedMethods() []string { return []string{"constructor", "transport"} }

// addOperations builds the views of the operations.
func (d *data) addOperations(api *model.API, seen map[string]string) error {
	methods := make(map[string]string)

	for _, name := range reservedMethods() {
		methods[name] = "the client class"
	}

	for i := range api.Operations {
		op := &api.Operations[i]

		if err := claim(methods, op.ID, "operation "+op.ID, clientgen.ErrInvalidName); err != nil {
			return err
		}

		ov, err := operationOf(op)
		if err != nil {
			return err
		}

		claimErr := claim(seen, ov.Input, "the input of operation "+op.ID, clientgen.ErrInvalidName)
		if claimErr != nil {
			return claimErr
		}

		d.Operations = append(d.Operations, ov)
	}

	return nil
}

// operationOf builds the view of one operation.
func operationOf(op *model.Operation) (operationView, error) {
	var ov operationView

	ov.ID, ov.Doc, ov.Input = op.ID, docOf(op), exported(op.ID)+"Input"
	ov.Method, ov.Path = tsString(string(op.Method)), tsString(op.Path)
	ov.Security, ov.Idempotency, ov.IfMatch = "caller", useNone, ifMatchOf(op.IfMatch)

	if op.Public {
		ov.Security = "public"
	}

	if op.IdempotencyKey {
		ov.Idempotency = "optional"
	}

	ov.Security, ov.Idempotency, ov.IfMatch = tsString(ov.Security), tsString(ov.Idempotency), tsString(ov.IfMatch)
	ov.TakesIfMatch, ov.TakesKey = op.IfMatch != model.IfMatchNone, op.IdempotencyKey
	ov.RequestBody = "undefined"

	if err := addParameters(&ov, op); err != nil {
		return operationView{}, err
	}

	if err := addBodyAndResult(&ov, op); err != nil {
		return operationView{}, err
	}

	codes := make([]string, len(op.Result.Statuses))
	for i, status := range op.Result.Statuses {
		codes[i] = strconv.Itoa(status)
	}

	ov.Statuses = strings.Join(codes, ", ")
	ov.IfMatchNeeded = op.IfMatch == model.IfMatchRequired
	ov.InputOptional = len(ov.PathMembers) == 0 && !ov.QueryNeeded && !ov.IfMatchNeeded &&
		(ov.Body == nil || ov.Body.Optional)

	return ov, nil
}

// addBodyAndResult builds the request body member, and the type and the
// validator of the answer.
func addBodyAndResult(ov *operationView, op *model.Operation) error {
	if op.Body != nil {
		ov.RequestBody = tsString(string(op.Body.MediaType))
		ov.BodyRequired = op.Body.Required

		typ, err := typeExpr(op.Body.Type)
		if err != nil {
			return fmt.Errorf("operation %s, body: %w", op.ID, err)
		}

		ov.Body = &memberView{Name: "body", Type: typ, Doc: "", Optional: !op.Body.Required}
	}

	ov.Result, ov.Decoder = "void", "undefined"

	if op.Result.Type != nil {
		var err error

		if ov.Result, err = typeExpr(*op.Result.Type); err != nil {
			return fmt.Errorf("operation %s, answer: %w", op.ID, err)
		}

		if ov.Decoder, err = decoderExpr(*op.Result.Type); err != nil {
			return fmt.Errorf("operation %s, answer: %w", op.ID, err)
		}
	}

	return nil
}

// addParameters builds the path and query members, and the lists of names.
func addParameters(ov *operationView, op *model.Operation) error {
	byName := make(map[string]model.Parameter)

	for _, p := range op.Parameters {
		if p.In == model.InPath {
			byName[p.Name] = p
		}
	}

	var pathNames []string

	for seg := range strings.SplitSeq(strings.TrimPrefix(op.Path, "/"), "/") {
		inner, ok := strings.CutPrefix(seg, "{")
		if !ok {
			continue
		}

		name, ok := strings.CutSuffix(inner, "}")
		if !ok {
			continue
		}

		p, found := byName[name]
		if !found {
			return fmt.Errorf(
				"%w: operation %s has no parameter for {%s}", clientgen.ErrInvalidDescription, op.ID, name,
			)
		}

		pathNames = append(pathNames, tsString(name))
		ov.PathMembers = append(ov.PathMembers, memberView{
			Name: propName(name), Type: stringType, Doc: p.Description, Optional: false,
		})
	}

	query, err := addQuery(ov, op)
	if err != nil {
		return err
	}

	ov.PathParams, ov.QueryParams = "["+strings.Join(pathNames, ", ")+"]", "["+strings.Join(query.all, ", ")+"]"
	ov.RequiredQuery = "[" + strings.Join(query.required, ", ") + "]"

	return nil
}

// queryNames are the quoted names of the query parameters of an operation.
type queryNames struct {
	all, required []string
}

// addQuery builds the query members. It returns the quoted names of all query
// parameters and of the required ones.
func addQuery(ov *operationView, op *model.Operation) (queryNames, error) {
	var names queryNames

	for _, p := range op.Parameters {
		if p.In != model.InQuery {
			continue
		}

		typ, typeErr := queryType(p.Type)
		if typeErr != nil {
			return queryNames{
				all:      nil,
				required: nil,
			}, fmt.Errorf(
				"operation %s, query parameter %s: %w",
				op.ID,
				p.Name,
				typeErr,
			)
		}

		names.all = append(names.all, tsString(p.Name))
		ov.QueryNeeded = ov.QueryNeeded || p.Required
		ov.QueryMembers = append(ov.QueryMembers, memberView{
			Name: propName(p.Name), Type: typ, Doc: p.Description, Optional: !p.Required,
		})
		if p.Required {
			names.required = append(names.required, tsString(p.Name))
		}
	}

	return names, nil
}

// queryType writes the TypeScript type of a query parameter. The runtime sends
// a number or a boolean as text.
func queryType(t model.Type) (string, error) {
	if t.Kind == model.TypeArray && t.Elem != nil {
		elem, err := baseExpr(*t.Elem)

		return "ReadonlyArray<" + elem + ">", err
	}

	return baseExpr(t)
}

// ifMatchOf names the use of If-Match.
func ifMatchOf(m model.IfMatch) string {
	switch m {
	case model.IfMatchOptional:
		return "optional"
	case model.IfMatchRequired:
		return "required"
	case model.IfMatchNone:
		return useNone
	}

	return useNone
}

// docOf joins the summary and the description of an operation.
func docOf(op *model.Operation) string {
	switch {
	case op.Summary != "" && op.Description != "":
		return op.Summary + "\n" + op.Description
	case op.Summary != "":
		return op.Summary
	default:
		return op.Description
	}
}
