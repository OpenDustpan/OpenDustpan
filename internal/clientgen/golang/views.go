package golang

import (
	"fmt"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen"
	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// addOperations builds the views of the operations.
func (d *data) addOperations(api *model.API) error {
	seen := map[string]string{"Do": transportOwner, "New": transportOwner}

	for i := range api.Operations {
		op := &api.Operations[i]

		ov, err := operationOf(op)
		if err != nil {
			return err
		}

		// The query type is a type of the package, so it shares the names of
		// the schemas.
		if ov.QueryType != "" {
			if err := claim(d.seen, ov.QueryType, "the query of operation "+op.ID); err != nil {
				return err
			}
		}

		if err := claim(seen, ov.Method, "operation "+op.ID); err != nil {
			return err
		}

		d.NeedsURL = d.NeedsURL || len(ov.Params) > 0 || len(ov.Query) > 0
		d.NeedsFmt = d.NeedsFmt || len(ov.Query) > 0
		d.Operations = append(d.Operations, ov)
	}

	return nil
}

// operationOf builds the view of one operation.
func operationOf(op *model.Operation) (operationView, error) {
	var ov operationView

	ov.ID, ov.Method, ov.HTTPMethod, ov.Doc = op.ID, exported(op.ID), methodConst(op.Method), docOf(op)
	ov.Public, ov.Idempotent, ov.IfMatch, ov.ETag = op.Public, op.IdempotencyKey, ifMatchConst(
		op.IfMatch,
	), op.Result.ETag

	if err := addParameters(&ov, op); err != nil {
		return operationView{}, err
	}

	if err := addBody(&ov, op); err != nil {
		return operationView{}, err
	}

	if err := addResult(&ov, op); err != nil {
		return operationView{}, err
	}

	codes := make([]string, len(op.Result.Statuses))
	for i, status := range op.Result.Statuses {
		codes[i] = strconv.Itoa(status)
	}

	ov.Statuses = strings.Join(codes, ", ")

	return ov, nil
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

// methodConst names the constant of net/http for a method.
func methodConst(m model.Method) string {
	return "http.Method" + string(m[:1]) + strings.ToLower(string(m[1:]))
}

// ifMatchConst names the constant of the transport for the use of If-Match.
func ifMatchConst(m model.IfMatch) string {
	names := [...]string{
		model.IfMatchNone:     "ifMatchNone",
		model.IfMatchOptional: "ifMatchOptional",
		model.IfMatchRequired: "ifMatchRequired",
	}

	if m < 0 || int(m) >= len(names) {
		return names[model.IfMatchNone]
	}

	return names[m]
}

// addParameters builds the path expression, the parameters of the method and
// the query struct.
func addParameters(ov *operationView, op *model.Operation) error {
	pathParams := make(map[string]model.Parameter)

	for i := range op.Parameters {
		if p := &op.Parameters[i]; p.In == model.InPath {
			pathParams[p.Name] = *p
		}
	}

	var (
		parts   []string
		literal string
		names   = map[string]string{}
	)

	for seg := range strings.SplitSeq(strings.TrimPrefix(op.Path, "/"), "/") {
		name, wildcard := wildcardName(seg)
		if !wildcard {
			literal += "/" + seg

			continue
		}

		p := pathParams[name]
		ident, err := camel(name)
		if err != nil {
			return err
		}

		// The adapter reads a path parameter from a string of the message, so
		// this template set writes string path parameters only.
		if p.Type.Kind != model.TypeString {
			return fmt.Errorf("%w: path parameter %s of %s is not a string", clientgen.ErrUnsupported, name, op.ID)
		}

		if err := claim(names, ident, "path parameter "+name); err != nil {
			return err
		}

		ov.Params = append(ov.Params, paramView{Ident: ident, Type: baseExpr(p.Type, ""), Wire: name})
		parts = append(parts, goString(literal+"/"), pathValue(ident))
		literal = ""
	}

	if literal != "" {
		parts = append(parts, goString(literal))
	}

	ov.PathExpr = strings.Join(parts, " + ")

	return addQuery(ov, op)
}

// wildcardName returns the name in a path segment like {name}.
func wildcardName(seg string) (string, bool) {
	inner, ok := strings.CutPrefix(seg, "{")
	if !ok {
		return "", false
	}

	return strings.CutSuffix(inner, "}")
}

// pathValue writes the escaped value of a path parameter.
func pathValue(ident string) string {
	return "url.PathEscape(" + ident + ")"
}

// addQuery builds the struct of the query parameters.
func addQuery(ov *operationView, op *model.Operation) error {
	seen := map[string]string{}

	for i := range op.Parameters {
		p := &op.Parameters[i]

		if p.In != model.InQuery {
			continue
		}

		field := exported(p.Name)
		if err := claim(seen, field, "query parameter "+p.Name); err != nil {
			return err
		}

		// Defense in depth: the check of the model refuses a type list that
		// no template can write, and this check keeps a type list out of the
		// query, where the adapter would send it as any JSON value.
		if clientgen.HoldsTypeList(p.Type) {
			return fmt.Errorf(
				"%w: query parameter %s of %s holds a type list, which the Go template set does not write",
				clientgen.ErrUnsupported, p.Name, op.ID,
			)
		}

		var qv queryView

		qv.Field, qv.Wire = field, p.Name

		switch {
		case p.Type.Kind == model.TypeArray:
			qv.Mode = "many"
		case !p.Required:
			qv.Mode = "ptr"
		default:
			qv.Mode = "value"
		}

		tag := p.Name + ",omitempty"
		ov.QueryFields = append(ov.QueryFields, fieldView{
			Name: field,
			Type: typeExpr(p.Type, parameterPresence(*p), ""),
			Tag:  "`json:" + goString(tag) + "`",
			Doc:  p.Description,
		})
		ov.Query = append(ov.Query, qv)
	}

	if len(ov.Query) > 0 {
		ov.QueryType = ov.Method + "Query"
	}

	return nil
}

// addBody builds the body parameter of the method.
func addBody(ov *operationView, op *model.Operation) error {
	if op.Body == nil {
		return nil
	}

	t := op.Body.Type
	ov.BodyRequired = op.Body.Required
	ov.MediaType = string(op.Body.MediaType)
	ov.BodyPointer = t.Kind == model.TypeRef && !t.Nullable

	if clientgen.HoldsTypeList(t) {
		return fmt.Errorf(
			"%w: operation %s has a body with a type list, which the Go template set does not write",
			clientgen.ErrUnsupported, op.ID,
		)
	}

	if ov.BodyPointer {
		ov.Body = "*" + t.Ref
		ov.AdapterBody = "client." + t.Ref

		return nil
	}

	if t.Kind == model.TypeRef {
		return fmt.Errorf("%w: operation %s has a body that can be null", clientgen.ErrUnsupported, op.ID)
	}

	ov.Body = baseExpr(t, "")
	ov.AdapterBody = baseExpr(t, "client.")

	return nil
}

// addResult builds the return type of the method.
func addResult(ov *operationView, op *model.Operation) error {
	t := op.Result.Type
	if t == nil {
		ov.ResultKind = "none"

		return nil
	}

	// Defense in depth: the check of the model refuses a type list that no
	// template can write, and this check keeps a type list out of the answer,
	// where the client would read it as any JSON value.
	if clientgen.HoldsTypeList(*t) {
		return fmt.Errorf(
			"%w: operation %s has an answer with a type list, which the Go template set does not write",
			clientgen.ErrUnsupported, op.ID,
		)
	}

	ov.Result = baseExpr(*t, "")
	ov.ResultReturn = ov.Result

	ov.ResultKind = "value"

	if t.Kind == model.TypeRef {
		ov.ResultKind = "pointer"
		ov.ResultReturn = "*" + ov.Result
	}

	if t.Kind == model.TypeArray || t.Kind == model.TypeMap || t.Kind == model.TypeAny {
		ov.ResultKind = "nilable"
	}

	return nil
}
