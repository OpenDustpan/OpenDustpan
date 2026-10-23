package catalog_test

import (
	"reflect"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/catalog"
)

// TestEnvHasNoMethods checks that an expression can call no method of the
// server: Env and each named type in it have no method, on the value or on
// the pointer.
func TestEnvHasNoMethods(t *testing.T) {
	t.Parallel()

	seen := map[reflect.Type]bool{}

	var walk func(rt reflect.Type)

	walk = func(rt reflect.Type) {
		if seen[rt] {
			return
		}

		seen[rt] = true

		if n := rt.NumMethod() + reflect.PointerTo(rt).NumMethod(); n > 0 && rt.Kind() != reflect.Interface {
			t.Fatalf("%s has %d methods, want none", rt, n)
		}

		if rt.Kind() == reflect.Struct {
			for f := range rt.Fields() {
				walk(f.Type)
			}
		}

		if k := rt.Kind(); k == reflect.Map || k == reflect.Slice || k == reflect.Array || k == reflect.Pointer {
			walk(rt.Elem())
		}
	}

	walk(reflect.TypeFor[catalog.Env]())
}

func TestExprBuiltins(t *testing.T) {
	t.Parallel()

	got := catalog.ExprBuiltins()
	if !slices.IsSorted(got) {
		t.Fatalf("ExprBuiltins() is not sorted: %v", got)
	}

	for _, refused := range []string{
		"now", "date", "timezone", "duration", "keys", "values", "toPairs", "join", "repeat", "replace", "string",
		"toJSON", "reduce",
	} {
		if slices.Contains(got, refused) {
			t.Fatalf("ExprBuiltins() holds %s", refused)
		}
	}
}
