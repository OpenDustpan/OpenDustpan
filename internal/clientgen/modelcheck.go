package clientgen

import (
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/internal/clientgen/model"
)

// CheckModel checks every name of the model against its allowlist.
//
// Defense in depth: the parser checks the same names. This check stays so that
// a model that something else built cannot put a name into source that the
// parser would refuse.
func CheckModel(api *model.API) error {
	for i := range api.Operations {
		if err := checkOperation(&api.Operations[i]); err != nil {
			return err
		}
	}

	for i := range api.Schemas {
		if err := checkSchema(&api.Schemas[i]); err != nil {
			return err
		}
	}

	return nil
}

// checkOperation checks the names, the method and the types of an operation.
func checkOperation(op *model.Operation) error {
	if err := CheckName(NameOperation, op.ID); err != nil {
		return err
	}

	if err := checkOperationTypes(op); err != nil {
		return err
	}

	for i := range op.Parameters {
		p := &op.Parameters[i]

		kind := NameQueryParameter
		if p.In == model.InPath {
			kind = NamePathParameter
		}

		if err := CheckName(kind, p.Name); err != nil {
			return err
		}
	}

	return nil
}

// checkSchema checks the names and the types of a schema.
func checkSchema(s *model.Schema) error {
	if err := CheckName(NameSchema, s.Name); err != nil {
		return err
	}

	for _, f := range s.Fields {
		if err := CheckName(NameField, f.Name); err != nil {
			return err
		}

		if err := checkType(f.Type); err != nil {
			return err
		}
	}

	for _, v := range s.Values {
		if err := CheckName(NameEnumValue, v); err != nil {
			return err
		}
	}

	return nil
}

// checkOperationTypes checks the method and the types of an operation.
func checkOperationTypes(op *model.Operation) error {
	switch op.Method {
	case model.MethodGet, model.MethodHead, model.MethodPost, model.MethodPut, model.MethodPatch, model.MethodDelete:
	default:
		return fmt.Errorf("%w: method %q of %s", ErrInvalidName, op.Method, op.ID)
	}

	var types []model.Type

	for i := range op.Parameters {
		p := &op.Parameters[i]

		types = append(types, p.Type)
	}

	if op.Body != nil {
		types = append(types, op.Body.Type)
	}

	if op.Result.Type != nil {
		types = append(types, *op.Result.Type)
	}

	for _, t := range types {
		if err := checkType(t); err != nil {
			return err
		}
	}

	return nil
}

// checkType checks the schema names inside a type.
func checkType(t model.Type) error {
	if t.Kind == model.TypeRef {
		if err := CheckName(NameSchema, t.Ref); err != nil {
			return err
		}
	}

	if t.Elem != nil {
		return checkType(*t.Elem)
	}

	return nil
}

// CheckToken checks a version, a release or another token before a template
// set puts it into source: 1 to 64 characters of letters, digits and . + - _.
func CheckToken(what, v string) error {
	if v == "" || len(v) > 64 ||
		strings.Trim(v, "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.+-_") != "" {
		return fmt.Errorf("%w: %s %q", ErrInvalidName, what, v)
	}

	return nil
}
