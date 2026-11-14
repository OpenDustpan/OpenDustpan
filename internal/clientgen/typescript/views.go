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
	numberType  = "number"
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
	// kindUnion is a choice between objects, by the value of one member.
	kindUnion
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
	// Discriminator is the member of a union that selects the variant.
	Discriminator string
	// Variants are the choices of a union, in the order of their values.
	Variants []variantView
	// Base is the name of the schema for a value that no variant names.
	Base string
}

// variantView is one choice of a union.
type variantView struct {
	// Value is the TypeScript string literal of the discriminator value.
	Value string
	// Name is the name of the schema of the choice.
	Name string
}

func (t typeView) IsInterface() bool  { return t.Kind == kindInterface }
func (t typeView) IsRecord() bool     { return t.Kind == kindRecord }
func (t typeView) IsClosedEnum() bool { return t.Kind == kindClosedEnum }
func (t typeView) IsOpenEnum() bool   { return t.Kind == kindOpenEnum }
func (t typeView) IsUnion() bool      { return t.Kind == kindUnion }

type fieldView struct {
	// Name is the property key, quoted when it needs quotes.
	Name     string
	Type     string
	Decoder  string
	Doc      string
	Optional bool
}

// addTypes builds the views of the schemas. Each schema claims its name, the
// name of its decoder, and, for an enum, the name of its values, so that no
// two emitted identifiers clash.
func (d *data) addTypes(api *model.API, seen map[string]string) error {
	for i := range api.Schemas {
		s := &api.Schemas[i]

		if err := claim(seen, s.Name, "schema "+s.Name, clientgen.ErrInvalidName); err != nil {
			return err
		}

		// Defense in depth: the schema name check already refuses some names that
		// would clash with a decoder. This claim stays so that a future name rule
		// cannot allow a name that the decoder of another schema would take.
		if err := claim(seen, "decode"+s.Name, "the decoder of schema "+s.Name, clientgen.ErrInvalidName); err != nil {
			return err
		}

		if s.Kind == model.KindEnum {
			valuesErr := claim(seen, s.Name+"Values", "the values of schema "+s.Name, clientgen.ErrInvalidName)
			if valuesErr != nil {
				return valuesErr
			}
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
		Discriminator: "", Variants: nil, Base: "",
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
	case model.KindUnion:
		tv.Kind = kindUnion
		tv.Discriminator = tsString(s.Discriminator)
		tv.Base = s.Base

		for _, v := range s.Variants {
			tv.Variants = append(tv.Variants, variantView{Value: tsString(v.Value), Name: v.Schema})
		}

		return tv, nil
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

	for i := range s.Fields {
		f := &s.Fields[i]
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

// typeExpr writes a model type as a TypeScript type.
func typeExpr(t model.Type) (string, error) {
	w, err := walk(t)

	return w.Type, err
}

// decoderExpr writes the validator of a model type as an expression of the
// combinators of the runtime.
func decoderExpr(t model.Type) (string, error) {
	w, err := walk(t)

	return w.Decoder, err
}

// walked is the TypeScript type of a model type and its validator.
type walked struct {
	Type    string
	Decoder string
}

// walk writes the type and the validator of a model type in one walk, so that
// a type never gets a weaker check than the description asks for: the one
// walk refuses a kind or a format that the template set cannot check, and the
// type and the validator cannot disagree.
func walk(t model.Type) (walked, error) {
	w, err := walkBase(t)
	if err != nil {
		return walked{}, err
	}

	if t.Nullable && t.Kind != model.TypeAny {
		return walked{Type: w.Type + " | null", Decoder: "nullable(" + w.Decoder + ")"}, nil
	}

	return w, nil
}

// knownFormats lists the formats that the validator of a kind checks. The
// second result is false for a kind that the template set does not know; the
// walk refuses that kind in its own check.
func knownFormats(kind model.TypeKind) ([]string, bool) {
	switch kind {
	case model.TypeString:
		return []string{"", "date-time"}, true
	case model.TypeInteger:
		return []string{"", "int64"}, true
	case model.TypeNumber:
		return []string{"", "float", "double"}, true
	case model.TypeBoolean, model.TypeArray, model.TypeMap, model.TypeRef, model.TypeAny, model.TypeUnion:
		return []string{""}, true
	default:
		return nil, false
	}
}

// checkFormat refuses a format that the validator of a known kind does not
// check.
func checkFormat(t model.Type) error {
	formats, known := knownFormats(t.Kind)
	if known && !slices.Contains(formats, t.Format) {
		return fmt.Errorf(
			"%w: the TypeScript template set does not check the format %q of a type of kind %d",
			clientgen.ErrUnsupported, t.Format, t.Kind,
		)
	}

	return nil
}

// walkBase walks a model type, without null.
func walkBase(t model.Type) (walked, error) {
	if err := checkFormat(t); err != nil {
		return walked{}, err
	}

	switch t.Kind {
	case model.TypeString:
		return stringWalk(t), nil
	case model.TypeInteger:
		return walked{numberType, "int64"}, nil
	case model.TypeNumber:
		return walked{numberType, numberType}, nil
	case model.TypeBoolean:
		return walked{"boolean", "boolean"}, nil
	case model.TypeArray:
		elem, err := walkElem(t)

		return walked{"ReadonlyArray<" + elem.Type + ">", "array(" + elem.Decoder + ")"}, err
	case model.TypeMap:
		elem, err := walkElem(t)

		return walked{"Readonly<Record<string, " + elem.Type + ">>", "record(" + elem.Decoder + ")"}, err
	case model.TypeRef:
		return walked{t.Ref, "ref(() => decode" + t.Ref + ")"}, nil
	case model.TypeAny:
		// The description says explicitly that the value is any JSON value.
		return walked{unknownType, anyDecoder}, nil
	case model.TypeUnion:
		return unionWalk(t)
	}

	return walked{}, fmt.Errorf(
		"%w: the TypeScript template set has no type for kind %d", clientgen.ErrUnsupported, t.Kind,
	)
}

// stringWalk writes the type and the validator of a string: a literal, a date
// and time, or any string.
func stringWalk(t model.Type) walked {
	if t.Const != "" {
		return walked{tsString(t.Const), "literal(" + tsString(t.Const) + ")"}
	}

	if t.Format == "date-time" {
		return walked{stringType, "dateTime"}
	}

	return walked{stringType, stringType}
}

// typeListMinMembers is the smallest number of members of a type list.
const typeListMinMembers = 2

// unionWalk writes the type and the validator of a type list: a union of the
// listed types, in the order of the list, and a validator that tries each of
// them.
func unionWalk(t model.Type) (walked, error) {
	// Defense in depth: the check of the model refuses a type list with fewer
	// than two members. This check stays so that the walk cannot write a union
	// of no member.
	if len(t.Members) < typeListMinMembers {
		return walked{}, fmt.Errorf(
			"%w: the TypeScript template set needs two or more members of a type list, not %d",
			clientgen.ErrUnsupported, len(t.Members),
		)
	}

	types := make([]string, 0, len(t.Members))
	decoders := make([]string, 0, len(t.Members))
	texts := make([]string, 0, len(t.Members))

	for _, m := range t.Members {
		w, err := walkBase(primitive(m))
		if err != nil {
			return walked{}, err
		}

		types = append(types, w.Type)
		decoders = append(decoders, w.Decoder)
		texts = append(texts, "a "+w.Type)
	}

	union := strings.Join(types, " | ")

	return walked{
		Type:    union,
		Decoder: "oneOf<" + union + ">(" + tsString(joinText(texts)) + ", " + strings.Join(decoders, ", ") + ")",
	}, nil
}

// primitive makes a type of one primitive kind, with the fields that a member
// of a type list does not have empty.
func primitive(kind model.TypeKind) model.Type {
	return model.Type{Kind: kind, Format: "", Elem: nil, Ref: "", Const: "", Nullable: false, Members: nil}
}

// joinText joins the names of the accepted types of a type list, as in
// "a string, a number or a boolean".
func joinText(texts []string) string {
	if len(texts) < typeListMinMembers {
		return strings.Join(texts, "")
	}

	return strings.Join(texts[:len(texts)-1], ", ") + " or " + texts[len(texts)-1]
}

// walkElem walks the items of an array or the values of a map.
func walkElem(t model.Type) (walked, error) {
	if t.Elem == nil {
		return walked{}, fmt.Errorf("%w: an array or a map has no item type", clientgen.ErrUnsupported)
	}

	return walk(*t.Elem)
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

	for i := range op.Parameters {
		if p := &op.Parameters[i]; p.In == model.InPath {
			byName[p.Name] = *p
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

	for i := range op.Parameters {
		p := &op.Parameters[i]

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

// baseTypeExpr writes the TypeScript type of a model type, without null.
func baseTypeExpr(t model.Type) (string, error) {
	w, err := walkBase(t)

	return w.Type, err
}

// queryType writes the TypeScript type of a query parameter. The runtime sends
// a number or a boolean as text.
func queryType(t model.Type) (string, error) {
	if t.Kind == model.TypeArray && t.Elem != nil {
		elem, err := baseTypeExpr(*t.Elem)

		return "ReadonlyArray<" + elem + ">", err
	}

	return baseTypeExpr(t)
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
