package dustpan

import (
	"reflect"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRouteHookFailsOnAnUnknownShape proves that the hook of the route test
// stops, and names the cause, when a Route type has a shape that it does not
// know: a row type, a list of actions in a field that is on no list, a scope
// value, a missing Method.
func TestRouteHookFailsOnAnUnknownShape(t *testing.T) {
	t.Parallel()

	type unknownRow struct {
		Method string
		Path   string
	}

	type documentedRow struct {
		Documented []identity.Action
	}

	type enforcedRow struct {
		Reads      []identity.Action
		Writes     []identity.Action
		Actions    []identity.Action
		Documented []identity.Action
	}

	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "a row type that the hook does not know",
			run: func() error {
				_, err := declarationOf("test", reflect.ValueOf(unknownRow{Method: "GET", Path: "/x"}))

				return err
			},
			want: "does not know the row type",
		},
		{
			name: "an action list in a field that is on no list",
			run: func() error {
				_, err := enforcedFieldsOf("test", "GET /x", reflect.ValueOf(documentedRow{
					Documented: []identity.Action{identity.ActionUnitRead},
				}))

				return err
			},
			want: "does not know the action field Documented",
		},
		{
			name: "an action in an unknown field beside an enforced one",
			run: func() error {
				var row enforcedRow

				row.Actions = []identity.Action{identity.ActionUnitRead}
				row.Documented = []identity.Action{identity.ActionUnitRead}

				_, err := enforcedFieldsOf("test", "GET /x", reflect.ValueOf(row))

				return err
			},
			want: "does not know the action field Documented",
		},
		{
			name: "a scope value that the hook does not know",
			run: func() error {
				var row tfe.Route

				row.Scope = tfe.Scope(99)

				_, err := scopeOf("test", "GET /x", reflect.ValueOf(row))

				return err
			},
			want: "does not know the scope value 99",
		},
		{
			name: "a row with no Scope field",
			run: func() error {
				var row unknownRow

				_, err := scopeOf("test", "GET /x", reflect.ValueOf(row))

				return err
			},
			want: "has no Scope field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one that holds %q", err, tt.want)
			}
		})
	}
}

// TestRouteHookReadsTheScopeAndTheEnforcedFields proves that the hook reads
// the public scope of a row, and counts only the fields that the wrapper
// enforces.
func TestRouteHookReadsTheScopeAndTheEnforcedFields(t *testing.T) {
	t.Parallel()

	var public pulumi.Route

	public.Method, public.Path, public.Scope = "GET", "/x", pulumi.ScopePublic
	public.Actions = []identity.Action{identity.ActionUnitRead}

	got, err := declarationOf("pulumi", reflect.ValueOf(public))
	if err != nil {
		t.Fatalf("declarationOf: %v", err)
	}

	if got.Scope != RouteScopePublic || len(got.EnforcedFields) != 1 || got.EnforcedFields[0] != "Actions" {
		t.Fatalf("declaration = %+v, want the public scope and the field Actions", got)
	}

	fields, err := enforcedFieldsOf("test", "GET /x", reflect.ValueOf(struct {
		Writes []identity.Action
		Extra  []identity.Action
	}{
		Writes: []identity.Action{identity.ActionUnitRead}, Extra: []identity.Action{identity.ActionUnitRead},
	}))
	if err != nil || len(fields) != 0 {
		t.Fatalf("enforcedFieldsOf = %v, %v, want no field: Writes and Extra are not enforced", fields, err)
	}
}
