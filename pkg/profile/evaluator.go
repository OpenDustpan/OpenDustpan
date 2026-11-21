package profile

import (
	"context"
	"encoding/json"
	"reflect"
	"time"
)

// Evaluation is the computed value of one expression of the own tree of an
// open.
type Evaluation struct {
	// Value is the computed value, of any JSON type.
	Value any
	// Kind is the kind of Value: string, number, boolean, list, map or null.
	Kind string
	// Paths are the paths under values that the expression read, each as
	// segments. An expression that reads no value reads none.
	Paths [][]string
}

// Evaluator computes one `${ }` value of the own tree of an open from the
// merged values. The environment holds values only: the map of the merged
// plain values by top-level key. A name other than values fails with
// ErrReference. The open carries the secret and never-store marks of each
// path that Paths names onto the computed value.
type Evaluator interface {
	// Eval computes source against values. It returns ErrReference for a
	// name other than values, and an error that names the failure for an
	// expression that does not compute. An expression never panics: a panic
	// is an error.
	Eval(ctx context.Context, source string, values map[string]any) (Evaluation, error)
}

// EvalKinds are the kinds of Evaluation.Kind.
const (
	EvalString  = "string"
	EvalNumber  = "number"
	EvalBoolean = "boolean"
	EvalList    = "list"
	EvalMap     = "map"
	EvalNull    = "null"
)

// EvalKindOf returns the kind of v: the kind of an Evaluation.Value. It
// describes the sealed JSON shape of the value: a time is a string, a slice
// is a list, and a map or a struct is a map.
func EvalKindOf(v any) string {
	switch t := v.(type) {
	case nil:
		return EvalNull
	case string:
		return EvalString
	case bool:
		return EvalBoolean
	case time.Time:
		return EvalString
	case json.Number:
		return EvalNumber
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return EvalNumber
	case []any:
		return EvalList
	case map[string]any:
		return EvalMap
	default:
		return evalKindOfReflect(t)
	}
}

// evalKindOfReflect maps any other Go value to its JSON shape: a slice or an
// array is a list, a map or a struct is a map, and anything else is a string.
// It uses conditions instead of a switch so that the exhaustive check does
// not demand a branch for every reflect kind that answers the same kind.
func evalKindOfReflect(v any) string {
	kind := reflect.TypeOf(v).Kind()
	if kind == reflect.Slice || kind == reflect.Array {
		return EvalList
	}

	if kind == reflect.Map || kind == reflect.Struct {
		return EvalMap
	}

	return EvalString
}
