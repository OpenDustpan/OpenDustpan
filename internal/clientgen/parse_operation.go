package clientgen

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// pathPrefix starts the path of each operation of the description.
const pathPrefix = "/api/dustpan/v1/"

// operationKeys returns the keys that an operation holds in the subset.
func operationKeys() []string {
	return []string{
		"operationId", "tags", "summary", keyDescription, "parameters", "requestBody", "responses",
		"security", "x-dustpan-actions",
	}
}

// operationSet collects what a walk over the paths finds.
type operationSet struct {
	ops  []model.Operation
	seen []string
}

// operations reads the selected operations in the order of their IDs. With no
// selection it reads all of them.
func (st *parseState) operations(selected []string) ([]model.Operation, error) {
	paths, err := objectAt(st.doc, "", "paths")
	if err != nil {
		return nil, err
	}

	set := new(operationSet)

	for _, path := range sortedKeys(paths) {
		err = st.pathItem(path, paths[path], selected, set)
		if err != nil {
			return nil, err
		}
	}

	for _, id := range selected {
		if !slices.Contains(set.seen, id) {
			return nil, fmt.Errorf("%w: the description has no operation %q", ErrInvalidDescription, id)
		}
	}

	slices.SortFunc(set.ops, func(a, b model.Operation) int { return strings.Compare(a.ID, b.ID) })

	return set.ops, nil
}

// pathItem reads the operations of one path.
func (st *parseState) pathItem(path string, node any, selected []string, set *operationSet) error {
	ptr := ptrJoin("/paths", path)

	item, err := asObject(ptr, node)
	if err != nil {
		return err
	}

	err = checkKeys(ptr, item, "get", "put", "post", "patch", "delete")
	if err != nil {
		return err
	}

	for _, method := range sortedKeys(item) {
		op, id, opErr := st.operationIfSelected(ptrJoin(ptr, method), path, method, item[method], selected)
		if opErr != nil {
			return opErr
		}

		if slices.Contains(set.seen, id) {
			return invalid(ptrJoin(ptr, method), "the operation ID "+id+" is in the description two times")
		}

		set.seen = append(set.seen, id)

		if op != nil {
			set.ops = append(set.ops, *op)
		}
	}

	return nil
}

// operationIfSelected reads the ID of an operation, and the operation when it
// is selected.
func (st *parseState) operationIfSelected(
	ptr, path, method string, node any, selected []string,
) (*model.Operation, string, error) {
	o, err := asObject(ptr, node)
	if err != nil {
		return nil, "", err
	}

	id, err := stringAt(o, ptr, "operationId")
	if err != nil {
		return nil, "", err
	}

	if !operationIDName.MatchString(id) {
		return nil, "", invalidName(ptrJoin(ptr, "operationId"), "operation ID", id)
	}

	if len(selected) > 0 && !slices.Contains(selected, id) {
		return nil, id, nil
	}

	op, err := st.operation(ptr, path, strings.ToUpper(method), id, o)
	if err != nil {
		return nil, "", err
	}

	return &op, id, nil
}

// operation reads one operation.
func (st *parseState) operation(ptr, path, method, id string, o object) (model.Operation, error) {
	var op model.Operation

	op.ID, op.Method, op.Path = id, model.Method(method), path

	if err := checkKeys(ptr, o, operationKeys()...); err != nil {
		return model.Operation{}, err
	}

	wildcards, err := pathWildcards(ptr, path)
	if err != nil {
		return model.Operation{}, err
	}

	if op.Tags, err = stringList(o, ptr, "tags"); err != nil {
		return model.Operation{}, err
	}

	if op.Summary, err = optionalString(o, ptr, "summary"); err != nil {
		return model.Operation{}, err
	}

	if op.Description, err = optionalString(o, ptr, keyDescription); err != nil {
		return model.Operation{}, err
	}

	err = st.access(ptr, o, &op)
	if err != nil {
		return model.Operation{}, err
	}

	err = st.operationParameters(ptr, o, wildcards, &op)
	if err != nil {
		return model.Operation{}, err
	}

	err = st.operationBody(ptr, o, &op)
	if err != nil {
		return model.Operation{}, err
	}

	err = st.operationResult(ptr, o, &op)
	if err != nil {
		return model.Operation{}, err
	}

	err = checkPage(ptr, &op, st.schemas)

	return op, err
}

// pathWildcards checks the path of an operation and returns the names of its
// wildcards in order.
func pathWildcards(ptr, path string) ([]string, error) {
	rest, ok := strings.CutPrefix(path, pathPrefix)
	if !ok {
		return nil, invalid(ptr, "the path must start with "+pathPrefix)
	}

	var names []string

	for seg := range strings.SplitSeq(rest, "/") {
		switch {
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}"):
			name := seg[1 : len(seg)-1]

			if strings.HasSuffix(name, "...") || name == "$" {
				return nil, unsupported(ptr, "path wildcard "+seg)
			}

			if !pathParamName.MatchString(name) {
				return nil, invalidName(ptr, "path wildcard", name)
			}

			names = append(names, name)
		case pathLiteral.MatchString(seg):
		default:
			return nil, invalidName(ptr, "path segment", seg)
		}
	}

	return names, nil
}

// access reads the actions, and whether the operation is public.
func (st *parseState) access(ptr string, o object, op *model.Operation) error {
	if sec, ok := o["security"]; ok {
		items, err := asList(ptrJoin(ptr, "security"), sec)
		if err != nil {
			return err
		}

		if len(items) > 0 {
			return unsupported(ptrJoin(ptr, "security"), "security requirement on an operation")
		}

		op.Public = true
	}

	aptr := ptrJoin(ptr, "x-dustpan-actions")

	raw, ok := o["x-dustpan-actions"]
	if !ok {
		return invalid(aptr, "the key is needed")
	}

	items, err := asList(aptr, raw)
	if err != nil {
		return err
	}

	for i, item := range items {
		action, aErr := actionOf(ptrJoin(aptr, strconv.Itoa(i)), item)
		if aErr != nil {
			return aErr
		}

		op.Actions = append(op.Actions, action)
	}

	if op.Public != (len(op.Actions) == 0) {
		return invalid(aptr, "a public operation has no action and another operation has one or more")
	}

	return nil
}

// actionOf reads {action, object}.
func actionOf(ptr string, item any) (string, error) {
	o, err := asObject(ptr, item)
	if err != nil {
		return "", err
	}

	err = checkKeys(ptr, o, "action", "object")
	if err != nil {
		return "", err
	}

	action, err := stringAt(o, ptr, "action")
	if err != nil {
		return "", err
	}

	if !actionName.MatchString(action) {
		return "", invalidName(ptrJoin(ptr, "action"), "action", action)
	}

	kind, err := stringAt(o, ptr, "object")
	if err != nil {
		return "", err
	}

	if !slices.Contains(objectKinds(), kind) {
		return "", invalid(ptrJoin(ptr, "object"), "object kind "+kind+" is not in the list")
	}

	return action, nil
}

// operationParameters reads the parameters, and the flags for the shared
// headers.
func (st *parseState) operationParameters(ptr string, o object, wildcards []string, op *model.Operation) error {
	var path, query []model.Parameter

	if raw, ok := o["parameters"]; ok {
		items, err := asList(ptrJoin(ptr, "parameters"), raw)
		if err != nil {
			return err
		}

		for i, item := range items {
			pptr := ptrJoin(ptrJoin(ptr, "parameters"), strconv.Itoa(i))

			p, shared, pErr := st.parameter(pptr, item, op)
			if pErr != nil {
				return pErr
			}

			switch {
			case shared:
			case p.In == model.InPath:
				path = append(path, p)
			default:
				query = append(query, p)
			}
		}
	}

	ordered, err := orderPathParameters(ptr, wildcards, path)
	if err != nil {
		return err
	}

	slices.SortFunc(query, func(a, b model.Parameter) int { return strings.Compare(a.Name, b.Name) })

	for i := 1; i < len(query); i++ {
		if query[i].Name == query[i-1].Name {
			return invalid(ptrJoin(ptr, "parameters"), "query parameter "+query[i].Name+" is in the list two times")
		}
	}

	op.Parameters = slices.Concat(ordered, query)

	return nil
}

// orderPathParameters puts the path parameters in the order of the path, and
// checks that every wildcard has exactly one parameter.
func orderPathParameters(ptr string, wildcards []string, path []model.Parameter) ([]model.Parameter, error) {
	if len(path) != len(wildcards) {
		return nil, invalid(ptrJoin(ptr, "parameters"), "the path parameters and the wildcards of the path differ")
	}

	ordered := make([]model.Parameter, 0, len(path))

	for _, w := range wildcards {
		i := slices.IndexFunc(path, func(p model.Parameter) bool { return p.Name == w })
		if i < 0 {
			return nil, invalid(ptrJoin(ptr, "parameters"), "the wildcard "+w+" has no parameter")
		}

		ordered = append(ordered, path[i])
	}

	return ordered, nil
}

// parameter reads one parameter. For a $ref to a shared parameter it sets the
// flag of the operation and returns shared.
func (st *parseState) parameter(ptr string, item any, op *model.Operation) (model.Parameter, bool, error) {
	var p model.Parameter

	o, err := asObject(ptr, item)
	if err != nil {
		return p, false, err
	}

	if ref, ok := o["$ref"]; ok {
		return p, true, sharedFlag(ptr, o, ref, op)
	}

	if err := checkKeys(ptr, o, "name", "in", "required", keyDescription, "schema"); err != nil {
		return p, false, err
	}

	return st.ownParameter(ptr, o)
}

// sharedFlag sets the flag of the operation for a shared parameter.
func sharedFlag(ptr string, o object, ref any, op *model.Operation) error {
	if len(o) > 1 {
		return unsupported(ptr, "$ref beside other keys")
	}

	rptr := ptrJoin(ptr, "$ref")

	s, ok := ref.(string)
	if !ok {
		return invalid(rptr, "a string is needed")
	}

	name, found := strings.CutPrefix(s, "#/components/parameters/")
	if !found {
		return unsupported(rptr, "$ref outside the shared parameters")
	}

	header, ifMatch, known := sharedParameter(name)
	if !known {
		return unsupported(rptr, "shared parameter "+name)
	}

	if header == "Idempotency-Key" {
		op.IdempotencyKey = true
	} else {
		op.IfMatch = ifMatch
	}

	return nil
}

// ownParameter reads a path parameter or a query parameter.
func (st *parseState) ownParameter(ptr string, o object) (model.Parameter, bool, error) {
	var p model.Parameter

	name, err := stringAt(o, ptr, "name")
	if err != nil {
		return p, false, err
	}

	in, err := stringAt(o, ptr, "in")
	if err != nil {
		return p, false, err
	}

	if p.Required, err = optionalBool(o, ptr, "required"); err != nil {
		return p, false, err
	}

	if p.Description, err = optionalString(o, ptr, keyDescription); err != nil {
		return p, false, err
	}

	p.Name, p.In = name, model.In(in)

	err = checkParameterName(ptr, p)
	if err != nil {
		return p, false, err
	}

	schema, ok := o["schema"]
	if !ok {
		return p, false, invalid(ptrJoin(ptr, "schema"), "the key is needed")
	}

	if p.Type, err = st.typeOf(ptrJoin(ptr, "schema"), schema); err != nil {
		return p, false, err
	}

	return p, false, checkParameterType(ptr, p.Type)
}

// checkParameterName checks the place and the name of a parameter.
func checkParameterName(ptr string, p model.Parameter) error {
	switch p.In {
	case model.InPath:
		if !pathParamName.MatchString(p.Name) {
			return invalidName(ptr, "path parameter", p.Name)
		}

		if !p.Required {
			return invalid(ptr, "a path parameter is required")
		}
	case model.InQuery:
		if !fieldName.MatchString(p.Name) {
			return invalidName(ptr, "query parameter", p.Name)
		}
	case model.InHeader:
		return unsupported(ptr, "header parameter "+p.Name)
	default:
		return unsupported(ptrJoin(ptr, "in"), "parameter place "+string(p.In))
	}

	return nil
}

// checkParameterType allows a scalar, or an array of a scalar.
func checkParameterType(ptr string, t model.Type) error {
	if t.Kind == model.TypeArray && t.Elem != nil {
		t = *t.Elem
	}

	switch t.Kind {
	case model.TypeString, model.TypeInteger, model.TypeNumber, model.TypeBoolean:
		return nil
	case model.TypeArray, model.TypeMap, model.TypeRef, model.TypeAny, model.TypeUnion:
		return unsupported(ptrJoin(ptr, "schema"), "parameter that is not a scalar or a list of scalars")
	default:
		return unsupported(ptrJoin(ptr, "schema"), "parameter type")
	}
}
