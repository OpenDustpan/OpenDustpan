package workspace_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// orderState wraps attributes and sensitive_attributes in a state with one
// managed terraform_data resource named x.
func orderState(attributes, sensitive string) string {
	return `{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"x",` +
		`"instances":[{"attributes":` + attributes + `,"sensitive_attributes":` + sensitive + `}]}]}`
}

// orderMarks keeps marks non-empty without matching the test address, so
// only sensitive_attributes drives masking.
func orderMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{"other.none": {"/id"}}
}

// orderBase is the attributes base path of the single test instance.
const orderBase = "/resources/0/instances/0/attributes"

// checkOrderMasked fails the test unless MaskedPaths holds exactly want, in
// order.
func checkOrderMasked(t *testing.T, state string, want []string) {
	t.Helper()

	got, err := workspace.MaskedPaths([]byte(state), orderMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("masked paths = %q, want %q", got, want)
	}
}

// checkOrderMarker fails the test unless the named attribute of the masked
// resource is a marker for path with no other key.
func checkOrderMarker(t *testing.T, state, key, path string) {
	t.Helper()

	detail, err := workspace.ReadStateResource([]byte(state), orderMarks(), "terraform_data.x")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	if len(detail.Instances) != 1 {
		t.Fatalf("instances = %d, want 1", len(detail.Instances))
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	marker, ok := attributes[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want a secret marker", key, attributes[key])
	}

	if len(marker) != 2 {
		t.Fatalf("%s = %v, want only secret and path keys", key, marker)
	}

	secret, ok := marker["secret"].(bool)
	if !ok || !secret {
		t.Fatalf("%s = %v, want secret true", key, marker)
	}

	if marker["path"] != path {
		t.Fatalf("%s = %v, want path %q", key, marker, path)
	}

	if !slices.Equal(detail.Masked, []string{path}) {
		t.Fatalf("detail masked = %q, want [%q]", detail.Masked, path)
	}
}

// TestMaskedPointerBelowMaskedPointer checks the failing case: a pointer
// below an already masked pointer masks nothing.
func TestMaskedPointerBelowMaskedPointer(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"p":{"a":"v"}}`,
		`[[{"type":"get_attr","value":"p"}],[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"secret"}]]`,
	)
	want := []string{orderBase + "/p"}

	checkOrderMasked(t, state, want)
	checkOrderMarker(t, state, "p", orderBase+"/p")
}

// TestMaskedPointerBelowMaskedPointerChain checks a chain of marker field
// names below a masked pointer: only the parent is masked.
func TestMaskedPointerBelowMaskedPointerChain(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"p":{"a":"v"}}`,
		`[[{"type":"get_attr","value":"p"}],`+
			`[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"secret"}],`+
			`[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"secret"},{"type":"get_attr","value":"path"}],`+
			`[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"path"}]]`,
	)
	want := []string{orderBase + "/p"}

	checkOrderMasked(t, state, want)
	checkOrderMarker(t, state, "p", orderBase+"/p")
}

// TestMaskedPointerBelowMaskedPointerIndex checks a pointer below a masked
// pointer by list index: only the list is masked.
func TestMaskedPointerBelowMaskedPointerIndex(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"l":["a","b"]}`,
		`[[{"type":"get_attr","value":"l"}],[{"type":"get_attr","value":"l"},{"type":"index","value":0}]]`,
	)
	want := []string{orderBase + "/l"}

	checkOrderMasked(t, state, want)
	checkOrderMarker(t, state, "l", orderBase+"/l")
}

// TestMaskedPointerDescendantFirst checks descendant first, ancestor second:
// both paths stay in the masked set, in the order applied.
func TestMaskedPointerDescendantFirst(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"p":{"k":"v"}}`,
		`[[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"k"}],[{"type":"get_attr","value":"p"}]]`,
	)
	want := []string{orderBase + "/p/k", orderBase + "/p"}

	got, err := workspace.MaskedPaths([]byte(state), orderMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("masked paths = %q, want %q", got, want)
	}

	detail, err := workspace.ReadStateResource([]byte(state), orderMarks(), "terraform_data.x")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	if !slices.Equal(detail.Masked, want) {
		t.Fatalf("detail masked = %q, want %q", detail.Masked, want)
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	marker, ok := attributes["p"].(map[string]any)
	if !ok {
		t.Fatalf("p = %v, want a secret marker", attributes["p"])
	}

	secret, ok := marker["secret"].(bool)
	if !ok || !secret {
		t.Fatalf("p = %v, want a secret marker", attributes["p"])
	}

	if marker["path"] != orderBase+"/p" {
		t.Fatalf("p = %v, want path %q", attributes["p"], orderBase+"/p")
	}
}

// TestMaskedPointerSameNodeThroughWrapper checks two literals that resolve
// to one node through a dynamic wrapper: both literal paths stay masked.
func TestMaskedPointerSameNodeThroughWrapper(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"w":{"value":{"k":"s"},"type":"dynamic"}}`,
		`[[{"type":"get_attr","value":"w"},{"type":"get_attr","value":"k"}],`+
			`[{"type":"get_attr","value":"w"},{"type":"get_attr","value":"value"},{"type":"get_attr","value":"k"}]]`,
	)
	want := []string{orderBase + "/w/k", orderBase + "/w/value/k"}

	got, err := workspace.MaskedPaths([]byte(state), orderMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("masked paths = %q, want %q", got, want)
	}
}

// TestMaskedPointerUnrelatedAfterMasked checks an unrelated pointer after a
// masked one still applies.
func TestMaskedPointerUnrelatedAfterMasked(t *testing.T) {
	t.Parallel()

	state := orderState(
		`{"p":{"a":"v"},"q":"x"}`,
		`[[{"type":"get_attr","value":"p"}],`+
			`[{"type":"get_attr","value":"p"},{"type":"get_attr","value":"secret"}],`+
			`[{"type":"get_attr","value":"q"}]]`,
	)
	want := []string{orderBase + "/p", orderBase + "/q"}

	got, err := workspace.MaskedPaths([]byte(state), orderMarks())
	if err != nil {
		t.Fatalf("masked paths: %v", err)
	}

	if !slices.Equal(got, want) {
		t.Fatalf("masked paths = %q, want %q", got, want)
	}

	detail, err := workspace.ReadStateResource([]byte(state), orderMarks(), "terraform_data.x")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	if !slices.Equal(detail.Masked, want) {
		t.Fatalf("detail masked = %q, want %q", detail.Masked, want)
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	marker, ok := attributes["p"].(map[string]any)
	if !ok {
		t.Fatalf("p = %v, want a secret marker", attributes["p"])
	}

	gotPath, ok := marker["path"].(string)
	if !ok || !strings.Contains(gotPath, "/p") {
		t.Fatalf("p = %v, want a marker for /p", attributes["p"])
	}
}
