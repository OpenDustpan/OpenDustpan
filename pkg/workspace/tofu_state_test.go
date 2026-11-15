package workspace_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
	"opendustpan.org/opendustpan/pkg/workspace/workspacetest"
)

// tofuTestState is a state file with two resources in the shape the CLI
// writes: a terraform_data resource with a sensitive input, whose derived
// output copies the secret without a mark of its own, and a provider
// resource whose schema marks bcrypt_hash and result sensitive. Dynamic
// values travel wrapped with their type; sensitive_attributes holds cty path
// step objects.
const tofuTestState = `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"seed-lineage",` +
	`"outputs":{},"resources":[` +
	`{"mode":"managed","type":"terraform_data","name":"smoke",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"smoke-id",` +
	`"input":{"value":"s3cr3t","type":"string"},` +
	`"output":{"value":"s3cr3t","type":"string"},"triggers_replace":null},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]},` +
	`{"mode":"managed","type":"random_password","name":"pw",` +
	`"provider":"provider[\"registry.opentofu.org/hashicorp/random\"]",` +
	`"instances":[{"schema_version":3,` +
	`"attributes":{"id":"pw-id","result":"pw-s3cr3t","bcrypt_hash":"pw-hash","length":16},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"bcrypt_hash"}],[{"type":"get_attr","value":"result"}]] }]}]}`

// tofuTestMarks are the kept json-state marks of the test state.
func tofuTestMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{
		"terraform_data.smoke": {"/input"},
		"random_password.pw":   {"/result"},
	}
}

// listTestPage returns one page of the masked resource list of the test
// state, failing the test otherwise.
func listTestPage(t *testing.T, cursor string, size int) workspace.StatePage {
	t.Helper()

	page, err := workspace.ListStateResources([]byte(tofuTestState), nil, cursor, size, "")
	if err != nil {
		t.Fatalf("list state resources: %v", err)
	}

	return page
}

// checkTestListNew proves every listed resource reads new without a previous
// state.
func checkTestListNew(t *testing.T, page workspace.StatePage) {
	t.Helper()

	for _, res := range page.Resources {
		if !res.Changed {
			t.Errorf("resource %q changed = false, want true without a previous state", res.Address)
		}
	}
}

// readTestDetail returns one masked resource of the test state, failing the
// test otherwise.
func readTestDetail(t *testing.T, address string, marks workspace.SensitiveMarks) workspace.StateDetail {
	t.Helper()

	detail, err := workspace.ReadStateResource([]byte(tofuTestState), marks, address)
	if err != nil {
		t.Fatalf("read state resource %q: %v", address, err)
	}

	return detail
}

// detailAttributes returns the attributes object of the first instance of a
// masked resource, failing the test otherwise.
func detailAttributes(t *testing.T, detail workspace.StateDetail) map[string]any {
	t.Helper()

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	return attributes
}

// isSecretMarker reports whether v is a secret marker: an object with the
// secret flag set.
func isSecretMarker(v any) bool {
	marker, ok := v.(map[string]any)
	if !ok {
		return false
	}

	secret, ok := marker["secret"].(bool)

	return ok && secret
}

// secretMarker returns the secret marker for key in attributes, failing the
// test when key holds no marker.
func secretMarker(t *testing.T, attributes map[string]any, key string) map[string]any {
	t.Helper()

	marker, ok := attributes[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %v, want a secret marker", key, attributes[key])
	}

	secret, ok := marker["secret"].(bool)
	if !ok || !secret {
		t.Fatalf("%s = %v, want a secret marker", key, attributes[key])
	}

	return marker
}

// checkMarkerPath proves the marker names the value path want.
func checkMarkerPath(t *testing.T, marker map[string]any, want string) {
	t.Helper()

	if marker["path"] != want {
		t.Errorf("marker path = %v, want %q", marker["path"], want)
	}
}

// TestListTofuResources proves the masked resource list: the addresses as
// stored with type, provider and module, every resource new without a
// previous state, and paging after the list.
func TestListTofuResources(t *testing.T) {
	t.Parallel()

	page := listTestPage(t, "", 0)

	if len(page.Resources) != 2 {
		t.Fatalf("listed resources = %+v, want 2", page.Resources)
	}

	first := page.Resources[0]
	if first.Address != "terraform_data.smoke" || first.Type != "terraform_data" ||
		first.Provider != `provider["terraform.io/builtin/terraform"]` || first.Module != "" {
		t.Errorf("first resource = %+v, want the data resource as stored", first)
	}

	checkTestListNew(t, page)

	if page.NextCursor != "" || page.Incomplete {
		t.Errorf("page = %+v, want one complete page", page)
	}

	checkTestListPaging(t)
}

// checkTestListPaging proves paging after the list: pages of one hold one
// resource each.
func checkTestListPaging(t *testing.T) {
	t.Helper()

	paged := listTestPage(t, "", 1)
	if len(paged.Resources) != 1 || paged.NextCursor == "" {
		t.Fatalf("paged list = %+v, want one resource with a next cursor", paged)
	}

	last := listTestPage(t, paged.NextCursor, 1)
	if len(last.Resources) != 1 || last.Resources[0].Address != "random_password.pw" || last.NextCursor != "" {
		t.Fatalf("last page = %+v, want the provider resource with no next cursor", last)
	}
}

// listTestFilteredPage returns one filtered page of the masked resource list
// of the test state, failing the test otherwise.
func listTestFilteredPage(t *testing.T, filter, cursor string, size int) workspace.StatePage {
	t.Helper()

	page, err := workspace.ListStateResources([]byte(tofuTestState), nil, cursor, size, filter)
	if err != nil {
		t.Fatalf("list state resources with filter %q: %v", filter, err)
	}

	return page
}

// TestListTofuFilterMatchesType proves the filter with a type substring
// answers only resources of that type, on one complete page.
func TestListTofuFilterMatchesType(t *testing.T) {
	t.Parallel()

	page := listTestFilteredPage(t, "terraform_data", "", 0)

	if len(page.Resources) != 1 || page.Resources[0].Address != "terraform_data.smoke" {
		t.Fatalf("filtered list = %+v, want only the data resource", page.Resources)
	}

	if page.NextCursor != "" || page.Incomplete {
		t.Fatalf("filtered list = %+v, want one complete page", page)
	}
}

// TestListTofuFilterMatchesAddress proves the filter with an address
// substring answers the named resource.
func TestListTofuFilterMatchesAddress(t *testing.T) {
	t.Parallel()

	page := listTestFilteredPage(t, "random_password.pw", "", 0)

	if len(page.Resources) != 1 || page.Resources[0].Address != "random_password.pw" {
		t.Fatalf("filtered list = %+v, want only the provider resource", page.Resources)
	}
}

// TestListTofuFilterIgnoresValues proves the filter never matches attribute
// values or sensitive values: an id, a plain-adjacent value and each secret
// answer no resources.
func TestListTofuFilterIgnoresValues(t *testing.T) {
	t.Parallel()

	for _, filter := range []string{"smoke-id", "pw-id", "s3cr3t", "pw-s3cr3t", "pw-hash"} {
		page := listTestFilteredPage(t, filter, "", 0)

		if len(page.Resources) != 0 {
			t.Fatalf("filter %q matched %+v, want none", filter, page.Resources)
		}

		if page.NextCursor != "" {
			t.Fatalf("filter %q next cursor = %q, want empty", filter, page.NextCursor)
		}
	}
}

// TestListTofuFilterPaging proves paging applies after the filter: a page
// holds only matching resources and the cursor walks them.
func TestListTofuFilterPaging(t *testing.T) {
	t.Parallel()

	first := listTestFilteredPage(t, "_", "", 1)

	if len(first.Resources) != 1 || first.NextCursor == "" {
		t.Fatalf("first filtered page = %+v, want 1 match with a cursor", first)
	}

	second := listTestFilteredPage(t, "_", first.NextCursor, 1)

	if len(second.Resources) != 1 || second.NextCursor != "" {
		t.Fatalf("second filtered page = %+v, want 1 match with no cursor", second)
	}

	if first.Resources[0].Address == second.Resources[0].Address {
		t.Fatalf("filtered pages overlap on %q", second.Resources[0].Address)
	}
}

// TestListTofuFilterKeepsIncomplete proves the filter keeps the incomplete
// marker: entries the server skips still mark the page, which holds only
// matching resources.
func TestListTofuFilterKeepsIncomplete(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]","instances":[]},` +
		`"junk",{"mode":"managed"},` +
		`{"mode":"managed","type":"random_password","name":"pw",` +
		`"provider":"provider[\"registry.opentofu.org/hashicorp/random\"]","instances":[]}]}`

	page, err := workspace.ListStateResources([]byte(state), nil, "", 0, "terraform_data")
	if err != nil {
		t.Fatalf("list state resources with a filter: %v", err)
	}

	if !page.Incomplete {
		t.Fatalf("filtered list = %+v, want incomplete with skipped entries", page)
	}

	if len(page.Resources) != 1 || page.Resources[0].Address != "terraform_data.smoke" {
		t.Fatalf("filtered list = %+v, want only the data resource", page.Resources)
	}
}

// TestListTofuChanged proves the changed markers: a second list over the
// same state reads every resource unchanged, and over a state with another
// input reads only the data resource changed.
func TestListTofuChanged(t *testing.T) {
	t.Parallel()

	same, err := workspace.ListStateResources([]byte(tofuTestState), []byte(tofuTestState), "", 0, "")
	if err != nil {
		t.Fatalf("list state resources over the same state: %v", err)
	}

	for _, res := range same.Resources {
		if res.Changed {
			t.Errorf("resource %q changed = true, want false over the same state", res.Address)
		}
	}

	changedState := strings.ReplaceAll(tofuTestState, `"value":"s3cr3t"`, `"value":"other"`)

	changed, err := workspace.ListStateResources([]byte(changedState), []byte(tofuTestState), "", 0, "")
	if err != nil {
		t.Fatalf("list state resources over a changed state: %v", err)
	}

	for _, res := range changed.Resources {
		want := res.Address == "terraform_data.smoke"
		if res.Changed != want {
			t.Errorf("resource %q changed = %t, want %t", res.Address, res.Changed, want)
		}
	}
}

// TestReadTofuResource proves one masked resource: the sensitive input
// answers as a marker with its value path, the plain id stays, and Masked
// names each marker path. The derived output copies the secret without a
// mark of its own, so it answers masked as a duplicate. The provider result
// answers as a marker through the kept json-state marks, and the bcrypt
// hash through the object-form sensitive_attributes.
func TestReadTofuResource(t *testing.T) {
	t.Parallel()

	detail := readTestDetail(t, "terraform_data.smoke", tofuTestMarks())

	if detail.Address != "terraform_data.smoke" || detail.Type != "terraform_data" || len(detail.Instances) != 1 {
		t.Fatalf("detail = %+v, want the data resource with one instance", detail)
	}

	attributes := detailAttributes(t, detail)

	if attributes["id"] != "smoke-id" {
		t.Errorf("id = %v, want the plain value", attributes["id"])
	}

	marker := secretMarker(t, attributes, "input")
	checkMarkerPath(t, marker, "/resources/0/instances/0/attributes/input")

	derived := secretMarker(t, attributes, "output")
	checkMarkerPath(t, derived, "/resources/0/instances/0/attributes/output")

	want := []string{
		"/resources/0/instances/0/attributes/input",
		"/resources/0/instances/0/attributes/output",
	}

	if len(detail.Masked) != len(want) || detail.Masked[0] != want[0] || detail.Masked[1] != want[1] {
		t.Errorf("masked = %q, want %q", detail.Masked, want)
	}

	provider := readTestDetail(t, "random_password.pw", tofuTestMarks())
	providerAttributes := detailAttributes(t, provider)

	result := secretMarker(t, providerAttributes, "result")
	checkMarkerPath(t, result, "/resources/1/instances/0/attributes/result")

	hash := secretMarker(t, providerAttributes, "bcrypt_hash")
	checkMarkerPath(t, hash, "/resources/1/instances/0/attributes/bcrypt_hash")

	if _, err := workspace.ReadStateResource([]byte(tofuTestState), tofuTestMarks(), "missing.none"); !errors.Is(
		err, workspace.ErrResourceNotFound,
	) {
		t.Errorf("read of an unknown address: err = %v, want ErrResourceNotFound", err)
	}
}

// TestReadTofuLegacyStringMarks proves states with the older string-form
// sensitive_attributes still mask: each named string step masks its value.
func TestReadTofuLegacyStringMarks(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_attributes":[["input"]] }]}]}`

	marks := workspace.SensitiveMarks{"terraform_data.smoke": {"/input"}}

	detail, err := workspace.ReadStateResource([]byte(state), marks, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	if !isSecretMarker(attributes["input"]) {
		t.Fatalf("input = %v, want a secret marker", attributes["input"])
	}
}

// TestReadTofuNestedSensitive proves a nested mark below a wrapped dynamic
// value: the mark names the unwrapped path, so the secret inside the wrapper
// answers masked, with the derived copy masked as a duplicate. The plain
// sibling stays.
func TestReadTofuNestedSensitive(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id",` +
		`"input":{"value":{"a":"plain","b":"nested-s3cr3t"},"type":["object",{"a":"string","b":"string"}]},` +
		`"output":{"value":{"a":"plain","b":"nested-s3cr3t"},"type":["object",{"a":"string","b":"string"}]},` +
		`"triggers_replace":null},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"},{"type":"get_attr","value":"b"}]] }]}]}`

	marks := workspace.SensitiveMarks{"terraform_data.smoke": {"/input/b"}}

	detail, err := workspace.ReadStateResource([]byte(state), marks, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	input, ok := attributes["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %v, want the wrapped object", attributes["input"])
	}

	inner, ok := input["value"].(map[string]any)
	if !ok {
		t.Fatalf("input value = %v, want an object", input["value"])
	}

	if !isSecretMarker(inner["b"]) {
		t.Fatalf("input value b = %v, want a secret marker", inner["b"])
	}

	if inner["a"] != "plain" {
		t.Errorf("input value a = %v, want the plain value", inner["a"])
	}

	if containsValue(detail.Instances[0].Attributes, "nested-s3cr3t") {
		t.Error("masked detail holds the nested secret in plain text")
	}
}

// TestCtyPathObjectSteps proves the sensitive_attributes read through the
// masked resource: the object steps the CLI writes, the older direct steps,
// and an unknown step shape that masks nothing on its own. Each case keeps
// marks for another address, so only the sensitive_attributes entry masks.
func TestCtyPathObjectSteps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		marks string
		check func(t *testing.T, input any)
	}{
		{name: "get_attr object", marks: `[[{"type":"get_attr","value":"input"}]]`, check: wantCtyMarker},
		{name: "legacy string", marks: `[["input"]]`, check: wantCtyMarker},
		{name: "unknown step", marks: `[[{"type":"element_key","value":"input"}]]`, check: wantCtyPlain},
		{name: "missing value", marks: `[[{"type":"get_attr"}]]`, check: wantCtyPlain},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.check(t, readCtyPathInput(t, tc.marks))
		})
	}
}

// readCtyPathInput returns the masked input of a small state with one
// sensitive_attributes entry. Marks for another address keep the version off
// the mask-everything fallback, so only the entry masks.
func readCtyPathInput(t *testing.T, steps string) any {
	t.Helper()

	state := `{"version":4,"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_attributes":` + steps + ` }]}]}`

	marks := workspace.SensitiveMarks{"other.none": {"/id"}}

	detail, err := workspace.ReadStateResource([]byte(state), marks, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read state resource: %v", err)
	}

	attributes, ok := detail.Instances[0].Attributes.(map[string]any)
	if !ok {
		t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
	}

	return attributes["input"]
}

// wantCtyMarker proves the value is a secret marker.
func wantCtyMarker(t *testing.T, input any) {
	t.Helper()

	if !isSecretMarker(input) {
		t.Errorf("input = %v, want a secret marker", input)
	}
}

// wantCtyPlain proves the value stayed plain.
func wantCtyPlain(t *testing.T, input any) {
	t.Helper()

	if isSecretMarker(input) {
		t.Errorf("input = %v, want the plain value", input)
	}
}

// TestCtyPathIndexSteps proves numeric path steps: an index object and a
// direct number each mask one list element, leaving its sibling plain.
func TestCtyPathIndexSteps(t *testing.T) {
	t.Parallel()

	for _, steps := range []string{
		`[[{"type":"get_attr","value":"items"},{"type":"index","value":1}]]`,
		`[["items",1]]`,
	} {
		state := `{"version":4,"resources":[` +
			`{"mode":"managed","type":"terraform_data","name":"smoke",` +
			`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
			`"instances":[{"schema_version":0,` +
			`"attributes":{"id":"smoke-id","items":["plain","s3cr3t"]},` +
			`"sensitive_attributes":` + steps + ` }]}]}`

		marks := workspace.SensitiveMarks{"other.none": {"/id"}}

		detail, err := workspace.ReadStateResource([]byte(state), marks, "terraform_data.smoke")
		if err != nil {
			t.Fatalf("read state resource with steps %s: %v", steps, err)
		}

		attributes, ok := detail.Instances[0].Attributes.(map[string]any)
		if !ok {
			t.Fatalf("attributes = %T, want an object", detail.Instances[0].Attributes)
		}

		items, ok := attributes["items"].([]any)
		if !ok || len(items) != 2 {
			t.Fatalf("items = %v, want two elements", attributes["items"])
		}

		if items[0] != "plain" {
			t.Errorf("items[0] = %v, want the plain value", items[0])
		}

		if !isSecretMarker(items[1]) {
			t.Errorf("items[1] = %v, want a secret marker", items[1])
		}
	}
}

// TestReadTofuResourceWithoutMarks proves the fallback: a version with no
// kept json-state marks every attribute value, including the plain id and
// every leaf below the wrapped dynamic values.
func TestReadTofuResourceWithoutMarks(t *testing.T) {
	t.Parallel()

	detail := readTestDetail(t, "terraform_data.smoke", nil)

	attributes := detailAttributes(t, detail)

	marker := secretMarker(t, attributes, "id")
	checkMarkerPath(t, marker, "/resources/0/instances/0/attributes/id")

	for _, key := range []string{"input", "output"} {
		wrapped, ok := attributes[key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want the wrapped object", key, attributes[key])
		}

		if !isSecretMarker(wrapped["value"]) {
			t.Fatalf("%s value = %v, want a secret marker", key, wrapped["value"])
		}
	}

	if len(detail.Masked) != 6 {
		t.Errorf("masked = %q, want every attribute leaf", detail.Masked)
	}
}

// TestTofuClientEncrypted proves the exported check of client-encrypted
// state: the envelope reads encrypted, a plain state reads plain, and an
// input the check cannot classify fails with ErrInvalidState.
func TestTofuClientEncrypted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		doc     string
		want    bool
		wantErr bool
	}{
		{"a plain state of the CLI", tofuTestState, false, false},
		{"the envelope", envelope, true, false},
		{
			"a plaintext state with the member",
			`{"encrypted_data":"x","resources":[{"type":"t","name":"a"}]}`, false, true,
		},
		{"an unknown version", `{"meta":{},"encrypted_data":"x","encryption_version":"v9"}`, false, true},
		{"an envelope in another object", `{"outer":` + envelope + `}`, false, false},
		{"not an object", `[]`, false, true},
		{"not JSON", `nope`, false, true},
		{"empty input", ``, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := workspace.IsClientEncrypted([]byte(tc.doc))
			if got != tc.want || (err != nil) != tc.wantErr ||
				(err != nil && !errors.Is(err, workspace.ErrInvalidState)) {
				t.Fatalf("IsClientEncrypted() = %v, %v, want %v and error %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// TestExtractSensitiveMarks proves the json-state read: only the paths that
// sensitive_values marks are kept, by instance address, with no value.
func TestExtractSensitiveMarks(t *testing.T) {
	t.Parallel()

	rendering := `{"format_version":"1.2","values":{"root_module":{"resources":[` +
		`{"address":"terraform_data.smoke","type":"terraform_data","name":"smoke",` +
		`"values":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_values":{"input":true}},` +
		`{"address":"random_password.pw","type":"random_password","name":"pw",` +
		`"values":{"id":"pw-id","result":"pw-s3cr3t"},` +
		`"sensitive_values":{"result":true}}]}}}`

	marks, err := workspace.ExtractSensitiveMarks([]byte(rendering))
	if err != nil {
		t.Fatalf("extract sensitive marks: %v", err)
	}

	want := tofuTestMarks()

	if len(marks) != len(want) {
		t.Fatalf("marks = %v, want %v", marks, want)
	}

	for address, paths := range want {
		got := marks[address]
		if len(got) != len(paths) || got[0] != paths[0] {
			t.Fatalf("marks[%q] = %q, want %q", address, got, paths)
		}
	}

	empty, err := workspace.ExtractSensitiveMarks([]byte(`{"values":{}}`))
	if err != nil {
		t.Fatalf("extract marks of a rendering without values: %v", err)
	}

	if len(empty) != 0 {
		t.Fatalf("marks of a rendering without values = %v, want none", empty)
	}

	// The rendering answers the derived output unwrapped and, as the CLI
	// writes it, without a mark of its own: only the marked input is kept.
	derived := `{"format_version":"1.2","values":{"root_module":{"resources":[` +
		`{"address":"terraform_data.smoke","type":"terraform_data","name":"smoke",` +
		`"values":{"id":"smoke-id","input":"s3cr3t","output":"s3cr3t","triggers_replace":null},` +
		`"sensitive_values":{"input":true,"output":{}}}]}}}`

	kept, err := workspace.ExtractSensitiveMarks([]byte(derived))
	if err != nil {
		t.Fatalf("extract marks of a rendering with a derived copy: %v", err)
	}

	if len(kept["terraform_data.smoke"]) != 1 || kept["terraform_data.smoke"][0] != "/input" {
		t.Fatalf("marks of a rendering with a derived copy = %v, want only [/input]", kept)
	}
}

// TestTofuSecretNeverLeaks walks the masked outputs above for the secret
// values: no body the tests decoded holds one in plain text.
func TestTofuSecretNeverLeaks(t *testing.T) {
	t.Parallel()

	detail := readTestDetail(t, "terraform_data.smoke", tofuTestMarks())
	raw := detailAttributes(t, detail)

	for _, leaked := range []string{"s3cr3t", "pw-s3cr3t"} {
		if containsValue(raw, leaked) {
			t.Errorf("masked attributes hold %q in plain text", leaked)
		}
	}
}

// containsValue reports whether v holds text anywhere below it.
func containsValue(v any, text string) bool {
	switch typed := v.(type) {
	case map[string]any:
		for _, elem := range typed {
			if containsValue(elem, text) {
				return true
			}
		}

		return false
	case []any:
		for _, elem := range typed {
			if containsValue(elem, text) {
				return true
			}
		}

		return false
	case string:
		return typed == text
	default:
		return false
	}
}

// maskedFixture is a workspace service with room for real state documents,
// so the masked read tests push states instead of tiny placeholders.
type maskedFixture struct {
	svc   *workspace.Service
	store *memStore
	ws    workspace.Workspace
	now   *time.Time
}

// newMaskedFixture returns a service over a fresh store with one unlocked
// workspace.
func newMaskedFixture(t *testing.T) maskedFixture {
	t.Helper()

	now := time.Date(2026, time.October, 9, 12, 0, 0, 0, time.UTC)
	store := newMemStore()
	acme := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "local",
		FailoverVersion:     1,
		Overrides:           noOverrides(),
	}

	if err := store.CreateNamespace(t.Context(), acme); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: store, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespaces: %v", err)
	}

	svc, err := workspace.NewService(workspace.Config{
		BlobGuard:          blobguardtest.None{},
		ManualLocks:        nil,
		Workspaces:         store,
		States:             store,
		Blobs:              openFixtureBlobs(t),
		Namespaces:         namespaces,
		OutOfScope:         workspacetest.NewRefusals(),
		Sealer:             newMemSealer(t, namespaces),
		Logger:             nil,
		Now:                func() time.Time { return now },
		MaxStateBytes:      1 << 20,
		MaxPendingVersions: 0,
		Retention:          0,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	name, err := workspace.ParseName("masked")
	if err != nil {
		t.Fatalf("name: %v", err)
	}

	ws, err := svc.Create(t.Context(), workspace.Ref{Namespace: "acme", Name: name}, memPlacement("acme"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	return maskedFixture{svc: svc, store: store, ws: ws, now: &now}
}

// pushMasked declares a version with content, uploads the rendering first so
// the marks land while the version is pending, then uploads the state. An
// empty rendering uploads no marks. It returns the version with its upload
// capability, so the test can retry the rendering after the finalize. The
// caller advances the clock between pushes, so versions finalize in order.
func (f maskedFixture) pushMasked(
	t *testing.T, serial int64, content, rendering string,
) (workspace.StateVersion, token.Token) {
	t.Helper()

	ctx := t.Context()

	pending, err := f.svc.BeginState(
		ctx,
		f.ws.ID,
		"alice",
		newState("l1", serial, content),
		authority(noOverrides(), nil),
	)
	if err != nil {
		t.Fatalf("begin serial %d: %v", serial, err)
	}

	if rendering != "" {
		f.uploadMaskedRendering(t, pending.Version.ID, pending.Upload, rendering, serial)
	}

	sv, err := f.svc.Upload(ctx, pending.Version.ID, pending.Upload, strings.NewReader(content))
	if err != nil {
		t.Fatalf("upload serial %d: %v", serial, err)
	}

	return sv, pending.Upload
}

// uploadMaskedRendering uploads the rendering of a pending version, failing
// the test otherwise.
func (f maskedFixture) uploadMaskedRendering(
	t *testing.T, id workspace.StateVersionID, upload token.Token, rendering string, serial int64,
) {
	t.Helper()

	content := strings.NewReader(rendering)
	if err := f.svc.DiscardJSONRendering(t.Context(), id, upload, content); err != nil {
		t.Fatalf("upload rendering of serial %d: %v", serial, err)
	}
}

// maskedRendering returns a json-state rendering that marks the input of the
// data resource and the result of the provider resource sensitive.
func maskedRendering() string {
	return `{"format_version":"1.2","values":{"root_module":{"resources":[` +
		`{"address":"terraform_data.smoke","type":"terraform_data","name":"smoke",` +
		`"values":{"id":"smoke-id","input":"s3cr3t"},` +
		`"sensitive_values":{"input":true}},` +
		`{"address":"random_password.pw","type":"random_password","name":"pw",` +
		`"values":{"id":"pw-id","result":"pw-s3cr3t"},` +
		`"sensitive_values":{"result":true}}]}}}`
}

// TestMaskedReadThroughService proves the masked read through the service:
// two pushes with one change, then the list reads only the changed resource
// changed, and one resource answers its secrets masked.
func TestMaskedReadThroughService(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	_, _ = f.pushMasked(t, 1, tofuTestState, maskedRendering())

	*f.now = f.now.Add(2 * time.Hour)
	secondContent := strings.ReplaceAll(tofuTestState, `"value":"s3cr3t"`, `"value":"other"`)
	second, _ := f.pushMasked(t, 2, secondContent, maskedRendering())

	page, err := f.svc.ReadResources(ctx, second.ID, "", 0, "")
	if err != nil {
		t.Fatalf("read resources: %v", err)
	}

	if len(page.Resources) != 2 {
		t.Fatalf("listed resources = %+v, want 2", page.Resources)
	}

	for _, res := range page.Resources {
		wantChanged := res.Address == "terraform_data.smoke"
		if res.Changed != wantChanged {
			t.Errorf("resource %q changed = %t, want %t", res.Address, res.Changed, wantChanged)
		}
	}

	detail, err := f.svc.ReadResource(ctx, second.ID, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	attributes := detailAttributes(t, detail)

	if attributes["id"] != "smoke-id" {
		t.Errorf("id = %v, want the plain value", attributes["id"])
	}

	marker := secretMarker(t, attributes, "input")

	checkMarkerPath(t, marker, "/resources/0/instances/0/attributes/input")

	derived := secretMarker(t, attributes, "output")
	checkMarkerPath(t, derived, "/resources/0/instances/0/attributes/output")

	want := []string{
		"/resources/0/instances/0/attributes/input",
		"/resources/0/instances/0/attributes/output",
	}

	if len(detail.Masked) != len(want) || detail.Masked[0] != want[0] || detail.Masked[1] != want[1] {
		t.Errorf("marker = %v masked = %q, want %q", marker, detail.Masked, want)
	}
}

// TestMaskedRenderingRetryKeepsMarks proves a retry of the rendering with
// other content still succeeds and keeps the marks of the version: the two
// PUTs of one upload carry other content by design.
func TestMaskedRenderingRetryKeepsMarks(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	first, firstUpload := f.pushMasked(t, 1, tofuTestState, maskedRendering())

	if err := f.svc.DiscardJSONRendering(ctx, first.ID, firstUpload, strings.NewReader(`{"other":1}`)); err != nil {
		t.Fatalf("retry rendering: %v", err)
	}

	again, err := f.svc.ReadResource(ctx, first.ID, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read resource after the retry: %v", err)
	}

	want := []string{
		"/resources/0/instances/0/attributes/input",
		"/resources/0/instances/0/attributes/output",
	}

	if len(again.Masked) != len(want) || again.Masked[0] != want[0] || again.Masked[1] != want[1] {
		t.Errorf("masked after the retry = %q, want %q", again.Masked, want)
	}

	stored, err := f.store.GetStateVersion(ctx, first.ID)
	if err != nil {
		t.Fatalf("get stored version: %v", err)
	}

	if len(stored.SensitiveMarks) == 0 {
		t.Error("stored version keeps no sensitive marks, want the kept paths")
	}
}

// TestMaskedReadWithoutMarksThroughService proves the fallback through the
// service: a version whose rendering marked nothing masks every attribute
// value of each resource instance.
func TestMaskedReadWithoutMarksThroughService(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	sv, _ := f.pushMasked(t, 1, tofuTestState, "")

	detail, err := f.svc.ReadResource(ctx, sv.ID, "terraform_data.smoke")
	if err != nil {
		t.Fatalf("read resource without marks: %v", err)
	}

	attributes := detailAttributes(t, detail)

	marker := secretMarker(t, attributes, "id")
	checkMarkerPath(t, marker, "/resources/0/instances/0/attributes/id")

	for _, key := range []string{"input", "output"} {
		wrapped, ok := attributes[key].(map[string]any)
		if !ok {
			t.Fatalf("%s = %v, want the wrapped object", key, attributes[key])
		}

		secretMarker(t, wrapped, "value")
	}

	if len(detail.Masked) != 6 {
		t.Errorf("masked = %q, want every attribute leaf", detail.Masked)
	}
}

// TestMaskedReadClientEncryptedThroughService proves a state the client
// encrypted fails the resource routes with ErrClientEncrypted.
func TestMaskedReadClientEncryptedThroughService(t *testing.T) {
	t.Parallel()

	f := newMaskedFixture(t)
	ctx := t.Context()

	if _, err := f.svc.Lock(ctx, f.ws.ID, "alice", "apply"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	sv, _ := f.pushMasked(t, 1, envelope, maskedRendering())

	if _, err := f.svc.ReadResources(ctx, sv.ID, "", 0, ""); !errors.Is(err, workspace.ErrClientEncrypted) {
		t.Errorf("read resources of encrypted state: err = %v, want ErrClientEncrypted", err)
	}

	if _, err := f.svc.ReadResource(ctx, sv.ID, "terraform_data.smoke"); !errors.Is(
		err, workspace.ErrClientEncrypted,
	) {
		t.Errorf("read resource of encrypted state: err = %v, want ErrClientEncrypted", err)
	}
}
