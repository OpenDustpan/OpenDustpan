package apidesc_test

import (
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
		{Method: method, Path: path, Actions: []string{"unit:read"}, Public: false, IdempotencyKey: false},
		{Method: method, Path: path + "/extra", Actions: []string{"unit:read"}, Public: false, IdempotencyKey: false},
	}

	got := apidesc.Collect(rows, nil)
	if len(got) != 1 || got[0].Path != path+"/extra" {
		t.Fatalf("Collect() = %v, want the one route %s/extra", got, path)
	}
}

// TestCollectTakesTheKeyFromTheRowOrTheLabel proves that a route table that
// has a key field sets the flag without a label.
func TestCollectTakesTheKeyFromTheRowOrTheLabel(t *testing.T) {
	t.Parallel()

	rows := []apidesc.Row{
		{
			Method:         "POST",
			Path:           "/api/dustpan/v1/things",
			Actions:        []string{"thing:write"},
			Public:         false,
			IdempotencyKey: true,
		},
		{
			Method:         "GET",
			Path:           "/api/dustpan/v1/things",
			Actions:        []string{"thing:read"},
			Public:         false,
			IdempotencyKey: false,
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
