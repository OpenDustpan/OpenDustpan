package typescript

import (
	"fmt"
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
		tv.RecordType, tv.RecordDecoder = typeExpr(*s.Extra), decoderExpr(*s.Extra)

		return tv, nil
	}

	tv.Kind = kindInterface

	for _, f := range s.Fields {
		tv.Fields = append(tv.Fields, fieldView{
			Name: propName(f.Name), Type: typeExpr(f.Type), Decoder: decoderExpr(f.Type), Doc: f.Description,
			Optional: !f.Required,
		})
	}

	return tv, nil
}

// typeExpr writes a model type as a TypeScript type.
func typeExpr(t model.Type) string {
	base := baseExpr(t)
	if t.Nullable && t.Kind != model.TypeAny {
		return base + " | null"
	}

	return base
}

// baseExpr writes the TypeScript type of a model type, without null.
func baseExpr(t model.Type) string {
	switch t.Kind {
	case model.TypeString:
		return stringType
	case model.TypeInteger, model.TypeNumber:
		return "number"
	case model.TypeBoolean:
		return "boolean"
	case model.TypeArray:
		return "ReadonlyArray<" + elemExpr(t) + ">"
	case model.TypeMap:
		return "Readonly<Record<string, " + elemExpr(t) + ">>"
	case model.TypeRef:
		return t.Ref
	case model.TypeAny:
		return unknownType
	}

	// An unknown kind has no type to write, so the type is the one that holds
	// any value.
	return unknownType
}

// elemExpr writes the type of the items of an array or the values of a map.
func elemExpr(t model.Type) string {
	if t.Elem == nil {
		return unknownType
	}

	return typeExpr(*t.Elem)
}

// decoderExpr writes the validator of a model type as an expression of the
// combinators of the runtime.
func decoderExpr(t model.Type) string {
	expr := baseDecoder(t)
	if t.Nullable && t.Kind != model.TypeAny {
		return "nullable(" + expr + ")"
	}

	return expr
}

// baseDecoder writes the validator of a model type, without null.
func baseDecoder(t model.Type) string {
	switch t.Kind {
	case model.TypeString:
		if t.Format == "date-time" {
			return "dateTime"
		}

		return stringType
	case model.TypeInteger:
		return "int64"
	case model.TypeNumber:
		return "number"
	case model.TypeBoolean:
		return "boolean"
	case model.TypeArray:
		return "array(" + elemDecoder(t) + ")"
	case model.TypeMap:
		return "record(" + elemDecoder(t) + ")"
	case model.TypeRef:
		return "ref(() => decode" + t.Ref + ")"
	case model.TypeAny:
		return anyDecoder
	}

	return anyDecoder
}

func elemDecoder(t model.Type) string {
	if t.Elem == nil {
		return "anyValue"
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

	Method       string
	Path         string
	PathParams   string
	QueryParams  string
	Security     string
	Idempotency  string
	IfMatch      string
	RequestBody  string
	BodyRequired bool
	Statuses     string

	// PathMembers, QueryMembers and Body build the input type.
	PathMembers  []memberView
	QueryMembers []memberView
	QueryNeeded  bool
	Body         *memberView
	// InputOptional says that every member of the input is optional, so a call
	// can leave it out.
	InputOptional bool
	TakesIfMatch  bool
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

	if op.Body != nil {
		ov.RequestBody = tsString(string(op.Body.MediaType))
		ov.BodyRequired = op.Body.Required
		ov.Body = &memberView{Name: "body", Type: typeExpr(op.Body.Type), Doc: "", Optional: !op.Body.Required}
	}

	ov.Result, ov.Decoder = "void", "undefined"
	if op.Result.Type != nil {
		ov.Result, ov.Decoder = typeExpr(*op.Result.Type), decoderExpr(*op.Result.Type)
	}

	codes := make([]string, len(op.Result.Statuses))
	for i, status := range op.Result.Statuses {
		codes[i] = strconv.Itoa(status)
	}

	ov.Statuses = strings.Join(codes, ", ")
	ov.InputOptional = len(ov.PathMembers) == 0 && !ov.QueryNeeded && (ov.Body == nil || ov.Body.Optional)

	return ov, nil
}

// addParameters builds the path and query members, and the lists of names.
func addParameters(ov *operationView, op *model.Operation) error {
	byName := make(map[string]model.Parameter)

	for _, p := range op.Parameters {
		if p.In == model.InPath {
			byName[p.Name] = p
		}
	}

	var pathNames, queryNames []string

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

	for _, p := range op.Parameters {
		if p.In != model.InQuery {
			continue
		}

		queryNames = append(queryNames, tsString(p.Name))
		ov.QueryNeeded = ov.QueryNeeded || p.Required
		ov.QueryMembers = append(ov.QueryMembers, memberView{
			Name: propName(p.Name), Type: queryType(p.Type), Doc: p.Description, Optional: !p.Required,
		})
	}

	ov.PathParams, ov.QueryParams = "["+strings.Join(pathNames, ", ")+"]", "["+strings.Join(queryNames, ", ")+"]"

	return nil
}

// queryType writes the TypeScript type of a query parameter. The runtime sends
// a number or a boolean as text.
func queryType(t model.Type) string {
	if t.Kind == model.TypeArray && t.Elem != nil {
		return "ReadonlyArray<" + baseExpr(*t.Elem) + ">"
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
