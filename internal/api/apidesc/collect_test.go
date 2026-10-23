package apidesc_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// TestCollectSkipsOnlyTheExactUndescribedRoutes proves that Collect leaves out
// a route by its exact pattern. A route with a similar path stays, so a new
// route cannot hide behind a part of a listed path.
func TestCollectSkipsOnlyTheExactUndescribedRoutes(t *testing.T) {
	t.Parallel()

	var listed string

	for pattern := range apidesc.Undescribed() {
		listed = pattern

		break
	}

	method, path, _ := strings.Cut(listed, " ")

	rows := []apidesc.Row{
		{
			Method: method, Path: path, Actions: []string{"unit:read"}, NamespaceActions: nil, Public: false,
			IdempotencyKey: false,
		},
		{
			Method: method, Path: path + "/extra", Actions: []string{"unit:read"}, NamespaceActions: nil,
			Public: false, IdempotencyKey: false,
		},
	}

	got := apidesc.Collect(rows, nil)
	if len(got) != 1 || got[0].Path != path+"/extra" {
		t.Fatalf("Collect() = %v, want the one route %s/extra", got, path)
	}
}

// TestLabelStatuses proves the success statuses that a label gives: the
// default of the body, the codes in ascending order, and an error at Build
// for a code that the label gives twice or that is not a 2xx code.
func TestLabelStatuses(t *testing.T) {
	t.Parallel()

	row := apidesc.Row{
		Method: "PUT", Path: "/api/dustpan/v1/things", Actions: []string{"thing:write"}, NamespaceActions: nil,
		Public: false, IdempotencyKey: false,
	}

	typed := apidesc.Typed[struct{}]("Thing")
	label := apidesc.Name("putThing", "things").On(apidesc.KindServer)

	tests := []struct {
		name    string
		label   apidesc.Label
		want    []int
		refused bool
	}{
		{"default with a body", label.WithBodies(apidesc.NoBody, typed), []int{200}, false},
		{"default with no body", label.WithBodies(apidesc.NoBody, apidesc.NoBody), []int{204}, false},
		{"one code", label.WithStatus(202).WithBodies(apidesc.NoBody, typed), []int{202}, false},
		{"two codes in order", label.WithStatus(201, 200).WithBodies(apidesc.NoBody, typed), []int{200, 201}, false},
		{
			"two calls add codes",
			label.WithStatus(201).WithStatus(200).WithBodies(apidesc.NoBody, typed),
			[]int{200, 201},
			false,
		},
		{"a repeated code", label.WithStatus(200).WithStatus(200, 201).WithBodies(apidesc.NoBody, typed), nil, true},
		{"a code outside 2xx", label.WithStatus(200, 302).WithBodies(apidesc.NoBody, typed), nil, true},
	}

	for _, tt := range tests {
		ops := apidesc.Collect([]apidesc.Row{row}, map[string]apidesc.Label{"PUT /api/dustpan/v1/things": tt.label})
		if len(ops) != 1 {
			t.Fatalf("%s: Collect() = %d operations, want 1", tt.name, len(ops))
		}

		_, err := apidesc.Build(ops)
		if tt.refused {
			if !errors.Is(err, apidesc.ErrInvalidOperation) {
				t.Errorf("%s: Build() = %v, want ErrInvalidOperation", tt.name, err)
			}

			continue
		}

		if err != nil || !slices.Equal(ops[0].Statuses, tt.want) {
			t.Errorf("%s: statuses = %v, err = %v, want %v", tt.name, ops[0].Statuses, err, tt.want)
		}
	}
}

// TestCollectTakesTheKeyFromTheRowOrTheLabel proves that a route table that
// has a key field sets the flag without a label.
func TestCollectTakesTheKeyFromTheRowOrTheLabel(t *testing.T) {
	t.Parallel()

	rows := []apidesc.Row{
		{
			Method:           "POST",
			Path:             "/api/dustpan/v1/things",
			Actions:          []string{"thing:write"},
			NamespaceActions: nil,
			Public:           false,
			IdempotencyKey:   true,
		},
		{
			Method:           "GET",
			Path:             "/api/dustpan/v1/things",
			Actions:          []string{"thing:read"},
			NamespaceActions: nil,
			Public:           false,
			IdempotencyKey:   false,
		},
	}
	labels := map[string]apidesc.Label{
		"POST /api/dustpan/v1/things": apidesc.Name("createThing", "things"),
		"GET /api/dustpan/v1/things":  apidesc.Name("listThings", "things"),
	}

	got := apidesc.Collect(rows, labels)

	keys := make([]bool, len(got))
	for i, o := range got {
		keys[i] = o.IdempotencyKey
	}

	if !slices.Equal(keys, []bool{true, false}) {
		t.Fatalf("IdempotencyKey flags = %v, want [true false]", keys)
	}
}
