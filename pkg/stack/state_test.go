package stack_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

const (
	// stateSigKey is the Pulumi secret signature key in test deployments.
	stateSigKey = "4dabf18193072939515e22adb298388d"
	// stateSecretValue is the secret value under the signature key.
	stateSecretValue = "1b47061264138c4ac30d75fd1eb44270"
	// stateAssetValue is the asset value under the signature key.
	stateAssetValue = "c44067f5952c0a294b673a41bacd8c17"
	// stateArchiveValue is the archive value under the signature key.
	stateArchiveValue = "0def7320c3a5731c473e5ecbe6d01bc7"
	// stateResourceRefValue is the resource reference value under the
	// signature key.
	stateResourceRefValue = "5cf8f73096256a8f31e491e813e4eb8e"
)

// secretOf returns a secret object with ciphertext: the plaintext never
// appears in the deployment.
func secretOf(ciphertext string) string {
	raw, err := json.Marshal(ciphertext)
	if err != nil {
		return `{"` + stateSigKey + `":"` + stateSecretValue + `","ciphertext":"?"}`
	}

	return `{"` + stateSigKey + `":"` + stateSecretValue + `","ciphertext":` + string(raw) + `}`
}

// maskedBodies returns the JSON of detail inputs and outputs and the outputs
// document, for the leak checks.
func maskedBodies(t *testing.T, detail stack.StateDetail, outputs stack.StateOutputs) []string {
	t.Helper()

	inputs, err := json.Marshal(detail.Inputs)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	outs, err := json.Marshal(detail.Outputs)
	if err != nil {
		t.Fatalf("marshal outputs: %v", err)
	}

	stackOuts, err := json.Marshal(outputs.Outputs)
	if err != nil {
		t.Fatalf("marshal stack outputs: %v", err)
	}

	return []string{string(inputs), string(outs), string(stackOuts)}
}

// checkNoLeak fails when any body holds the ciphertext, the signature key
// or a ciphertext field.
func checkNoLeak(t *testing.T, bodies []string, ciphertext string) {
	t.Helper()

	leaked := []string{stateSigKey, "ciphertext"}
	if ciphertext != "" {
		leaked = append(leaked, ciphertext)
	}

	for _, body := range bodies {
		for _, want := range leaked {
			if strings.Contains(body, want) {
				t.Fatalf("masked body holds %q: %s", want, body)
			}
		}
	}
}

// isSecretMarker reports whether v is a secret marker with a value path.
func isSecretMarker(v any) (string, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return "", false
	}

	secret, ok := obj["secret"].(bool)
	if !ok || !secret {
		return "", false
	}

	path, ok := obj["path"].(string)
	if !ok || path == "" {
		return "", false
	}

	return path, true
}

// checkMarkerListed fails when path has no entry in masked.
func checkMarkerListed(t *testing.T, path string, masked []string) {
	t.Helper()

	if !slices.Contains(masked, path) {
		t.Fatalf("marker for %q is not in masked %q", path, masked)
	}
}

// walkMarkers proves each secret marker under v names a path in masked.
func walkMarkers(t *testing.T, v any, masked []string) {
	t.Helper()

	switch typed := v.(type) {
	case map[string]any:
		if path, ok := isSecretMarker(typed); ok {
			checkMarkerListed(t, path, masked)

			return
		}

		for _, elem := range typed {
			walkMarkers(t, elem, masked)
		}
	case []any:
		for _, elem := range typed {
			walkMarkers(t, elem, masked)
		}
	default:
	}
}

// checkMaskedAuthoritative proves each marker path in body is in masked: a
// real value with the marker shape but no entry stays a real value.
func checkMaskedAuthoritative(t *testing.T, body string, masked []string) {
	t.Helper()

	var doc any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("decode masked body: %v", err)
	}

	walkMarkers(t, doc, masked)
}

func TestMaskNestedSecrets(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::custom:resource:Type::res1",` +
		`"type":"custom:resource:Type",` +
		`"inputs":{"plain":"hello","nested":{"password":` + secretOf("v1:cipher1") + `},` +
		`"list":["a",` + secretOf("v1:cipher2") + `]},` +
		`"outputs":{"token":` + secretOf("v1:cipher3") + `}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::custom:resource:Type::res1")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	for _, want := range []string{
		"/resources/0/inputs/nested/password",
		"/resources/0/inputs/list/1",
		"/resources/0/outputs/token",
	} {
		if !slices.Contains(detail.Masked, want) {
			t.Errorf("masked %q lacks %q", detail.Masked, want)
		}
	}

	raw, err := json.Marshal(detail.Inputs)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	checkNoLeak(t, []string{string(raw)}, "v1:cipher")
	checkMaskedAuthoritative(t, string(raw), detail.Masked)
}

func TestMaskEscapesPointerTokens(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::custom:resource:Type::res1",` +
		`"type":"custom:resource:Type",` +
		`"inputs":{"a/b":` + secretOf("v1:c1") + `,"c~d":` + secretOf("v1:c2") + `},` +
		`"outputs":{}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::custom:resource:Type::res1")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	for _, want := range []string{"/resources/0/inputs/a~1b", "/resources/0/inputs/c~0d"} {
		if !slices.Contains(detail.Masked, want) {
			t.Errorf("masked %q lacks escaped path %q", detail.Masked, want)
		}
	}
}

func TestMarkerShapedRealValueStays(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::custom:resource:Type::res1",` +
		`"type":"custom:resource:Type",` +
		`"inputs":{"note":{"secret":true,"path":"/real"}},` +
		`"outputs":{}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::custom:resource:Type::res1")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(detail.Masked) != 0 {
		t.Fatalf("masked = %q, want none for a marker-shaped real value", detail.Masked)
	}

	raw, err := json.Marshal(detail.Inputs)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	if !strings.Contains(string(raw), `"/real"`) {
		t.Fatalf("inputs %s lost the real marker-shaped value", raw)
	}
}

func TestDeploymentWithNoSecrets(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::custom:resource:Type::res1",` +
		`"type":"custom:resource:Type","provider":"urn:pulumi:dev::web::pulumi:providers:aws::prov::id",` +
		`"parent":"urn:pulumi:dev::web::my:component:Comp::comp",` +
		`"inputs":{"name":"plain"},"outputs":{"id":"abc"}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::custom:resource:Type::res1")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(detail.Masked) != 0 {
		t.Fatalf("masked = %q, want none", detail.Masked)
	}

	if detail.Provider == "" || detail.Parent == "" || detail.Type == "" {
		t.Fatalf("detail = %+v, want identity fields verbatim", detail)
	}

	raw, err := json.Marshal(detail.Outputs)
	if err != nil {
		t.Fatalf("marshal outputs: %v", err)
	}

	if !strings.Contains(string(raw), `"abc"`) {
		t.Fatalf("outputs %s lost the plain value", raw)
	}

	outputs, err := stack.ReadStateOutputs([]byte(deployment))
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}

	if len(outputs.Masked) != 0 {
		t.Fatalf("outputs masked = %q, want none without a stack resource", outputs.Masked)
	}
}

func TestStackOutputsMaskSecrets(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack",` +
		`"outputs":{"password":` + secretOf("v1:outcipher") + `,"greeting":"hello"}}]}`

	outputs, err := stack.ReadStateOutputs([]byte(deployment))
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}

	if len(outputs.Masked) != 1 || outputs.Masked[0] != "/resources/0/outputs/password" {
		t.Fatalf("masked = %q, want the password path", outputs.Masked)
	}

	raw, err := json.Marshal(outputs.Outputs)
	if err != nil {
		t.Fatalf("marshal outputs: %v", err)
	}

	checkNoLeak(t, []string{string(raw)}, "v1:outcipher")
	checkMaskedAuthoritative(t, string(raw), outputs.Masked)

	if !strings.Contains(string(raw), `"hello"`) {
		t.Fatalf("outputs %s lost the plain greeting", raw)
	}
}

func TestListChangedOnlyOnChangedResource(t *testing.T) {
	t.Parallel()

	urnStack := "urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"
	urnRef := "urn:pulumi:dev::web::pulumi:pulumi:StackReference::ref"

	previous := `{"resources":[{` +
		`"urn":"` + urnStack + `","type":"pulumi:pulumi:Stack",` +
		`"outputs":{"password":` + secretOf("v1:old") + `,"greeting":"hi"}},` +
		`{"urn":"` + urnRef + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}}]}`

	current := `{"resources":[{` +
		`"urn":"` + urnStack + `","type":"pulumi:pulumi:Stack",` +
		`"outputs":{"password":` + secretOf("v1:new") + `,"greeting":"changed"}},` +
		`{"urn":"` + urnRef + `","type":"pulumi:pulumi:StackReference",` +
		`"inputs":{"name":"acme/web/other"},"outputs":{}}]}`

	page, err := stack.ListStateResources([]byte(current), []byte(previous), "", 100)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}

	if page.Incomplete {
		t.Fatalf("page is incomplete: %q", page.Reason)
	}

	if len(page.Resources) != 2 {
		t.Fatalf("resources = %d, want 2", len(page.Resources))
	}

	// Only the stack resource changed: its greeting changed and its secret
	// ciphertext differs, while the reference with the same inputs stays
	// unchanged, because secrets compare by marker.
	for _, res := range page.Resources {
		wantChanged := res.URN == urnStack
		if res.Changed != wantChanged {
			t.Errorf("resource %q changed = %t, want %t", res.URN, res.Changed, wantChanged)
		}

		if res.URN != urnStack && res.URN != urnRef {
			t.Errorf("resource urn = %q, want a stored URN verbatim", res.URN)
		}
	}
}

func TestListNewResourcesCountAsChanged(t *testing.T) {
	t.Parallel()

	current := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::custom:resource:Type::res1",` +
		`"type":"custom:resource:Type","inputs":{},"outputs":{}}]}`

	page, err := stack.ListStateResources([]byte(current), nil, "", 100)
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}

	if len(page.Resources) != 1 || !page.Resources[0].Changed {
		t.Fatalf("resources = %+v, want one new resource with changed", page.Resources)
	}
}

// collectPagedResources pages through deployment with limit and returns
// every URN it sees.
func collectPagedResources(t *testing.T, deployment string, limit int) map[string]bool {
	t.Helper()

	seen := map[string]bool{}
	cursor := ""

	for {
		page, err := stack.ListStateResources([]byte(deployment), nil, cursor, limit)
		if err != nil {
			t.Fatalf("list resources at %q: %v", cursor, err)
		}

		if page.Incomplete {
			t.Fatalf("page at %q is incomplete: %q", cursor, page.Reason)
		}

		for _, res := range page.Resources {
			if seen[res.URN] {
				t.Fatalf("resource %q listed twice", res.URN)
			}

			seen[res.URN] = true
		}

		cursor = page.NextCursor

		if cursor == "" {
			return seen
		}
	}
}

func TestListPagingOverManyResources(t *testing.T) {
	t.Parallel()

	parts := make([]string, 0, 1000)

	for i := range 1000 {
		parts = append(parts, `{"urn":"urn:pulumi:dev::web::custom:resource:Type::res`+strconv.Itoa(i)+`",`+
			`"type":"custom:resource:Type","inputs":{},"outputs":{}}`)
	}

	deployment := `{"resources":[` + strings.Join(parts, ",") + `]}`

	if seen := collectPagedResources(t, deployment, 500); len(seen) != 1000 {
		t.Fatalf("listed %d resources, want 1000", len(seen))
	}
}

func TestListRejectsBadPaging(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[]}`

	for _, tc := range []struct {
		name   string
		cursor string
		limit  int
	}{
		{name: "limit over 500", cursor: "", limit: 501},
		{name: "limit zero", cursor: "", limit: 0},
		{name: "limit negative", cursor: "", limit: -1},
		{name: "bad cursor", cursor: "nope", limit: 100},
		{name: "negative cursor", cursor: "-1", limit: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := stack.ListStateResources([]byte(deployment), nil, tc.cursor, tc.limit); err == nil {
				t.Fatalf("list with cursor %q limit %d succeeded, want an error", tc.cursor, tc.limit)
			} else if !errors.Is(err, stack.ErrInvalidUpdate) {
				t.Fatalf("error = %v, want ErrInvalidUpdate", err)
			}
		})
	}
}

func TestReadMissingResource(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[]}`

	if _, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::x:X::nope"); err == nil {
		t.Fatal("read of a missing resource succeeded, want an error")
	} else if !errors.Is(err, stack.ErrResourceNotFound) {
		t.Fatalf("error = %v, want ErrResourceNotFound", err)
	}
}

func TestMaskedBodiesHoldNoSecrets(t *testing.T) {
	t.Parallel()

	deployment := `{"resources":[{` +
		`"urn":"urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev",` +
		`"type":"pulumi:pulumi:Stack",` +
		`"inputs":{"password":` + secretOf("v1:inputcipher") + `},` +
		`"outputs":{"password":` + secretOf("v1:outputcipher") + `}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), "urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev")
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	outputs, err := stack.ReadStateOutputs([]byte(deployment))
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}

	bodies := maskedBodies(t, detail, outputs)
	checkNoLeak(t, bodies, "v1:inputcipher")
	checkNoLeak(t, bodies, "v1:outputcipher")

	for _, body := range bodies {
		checkMaskedAuthoritative(t, body, detail.Masked)
	}
}

func TestSpecialValuesStayUnmasked(t *testing.T) {
	t.Parallel()

	urn := "urn:pulumi:dev::web::custom:resource:Type::res1"
	asset := `{"` + stateSigKey + `":"` + stateAssetValue + `","hash":"abc123","text":"hello"}`
	archive := `{"` + stateSigKey + `":"` + stateArchiveValue + `","assets":{"file.txt":{"` +
		stateSigKey + `":"` + stateAssetValue + `","text":"plain"}}}`
	ref := `{"` + stateSigKey + `":"` + stateResourceRefValue + `","urn":"urn:pulumi:dev::web::other:Type::o1"}`

	deployment := `{"resources":[{"urn":"` + urn + `","type":"custom:resource:Type",` +
		`"inputs":{"file":` + asset + `,"bundle":` + archive + `,"target":` + ref + `},` +
		`"outputs":{}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), urn)
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	if len(detail.Masked) != 0 {
		t.Fatalf("masked = %q, want none for asset, archive and resource reference values", detail.Masked)
	}

	raw, err := json.Marshal(detail.Inputs)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	for _, want := range []string{"abc123", "hello", "file.txt", "plain", "urn:pulumi:dev::web::other:Type::o1"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("inputs %s lost the special value part %q", raw, want)
		}
	}

	checkMaskedAuthoritative(t, string(raw), detail.Masked)
}

func TestSecretNestedInArchiveMasked(t *testing.T) {
	t.Parallel()

	urn := "urn:pulumi:dev::web::custom:resource:Type::res1"
	archive := `{"` + stateSigKey + `":"` + stateArchiveValue + `","assets":{"note.txt":{"` +
		stateSigKey + `":"` + stateAssetValue + `","text":"plain"},"password":` + secretOf("v1:archivecipher") + `}}`

	deployment := `{"resources":[{"urn":"` + urn + `","type":"custom:resource:Type",` +
		`"inputs":{"bundle":` + archive + `},"outputs":{}}]}`

	detail, err := stack.ReadStateResource([]byte(deployment), urn)
	if err != nil {
		t.Fatalf("read resource: %v", err)
	}

	want := "/resources/0/inputs/bundle/assets/password"
	if len(detail.Masked) != 1 || detail.Masked[0] != want {
		t.Fatalf("masked = %q, want only %q", detail.Masked, want)
	}

	raw, err := json.Marshal(detail.Inputs)
	if err != nil {
		t.Fatalf("marshal inputs: %v", err)
	}

	// Only the nested secret is masked: its ciphertext and the secret
	// value are gone, while the archive and asset wrappers stay verbatim
	// with their own signature values.
	if strings.Contains(string(raw), "v1:archivecipher") {
		t.Fatalf("inputs %s leak the nested secret ciphertext", raw)
	}

	if strings.Contains(string(raw), stateSecretValue) {
		t.Fatalf("inputs %s leak the nested secret value", raw)
	}

	if !strings.Contains(string(raw), `"note.txt"`) || !strings.Contains(string(raw), `"plain"`) {
		t.Fatalf("inputs %s lost the plain asset beside the nested secret", raw)
	}

	checkMaskedAuthoritative(t, string(raw), detail.Masked)
}

func TestDiffSecretAddedOrRemovedIsChangedSecret(t *testing.T) {
	t.Parallel()

	urn := "urn:pulumi:dev::web::pulumi:pulumi:Stack::web-dev"
	deployment := func(outputs string) []byte {
		return []byte(`{"resources":[{"urn":"` + urn + `","type":"pulumi:pulumi:Stack","outputs":` + outputs + `}]}`)
	}

	withSecret := deployment(`{"token":` + secretOf("v1:abc") + `,"greeting":"hi"}`)
	withoutSecret := deployment(`{"greeting":"hi"}`)

	tests := []struct {
		name     string
		from, to []byte
	}{
		{name: "added", from: withoutSecret, to: withSecret},
		{name: "removed", from: withSecret, to: withoutSecret},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			diff, err := stack.DiffStates(tt.from, tt.to, "", 100)
			if err != nil {
				t.Fatalf("diff states: %v", err)
			}

			if len(diff.Changed) != 1 {
				t.Fatalf("changed = %d, want 1", len(diff.Changed))
			}

			got := diff.Changed[0]
			if !slices.Equal(got.ChangedSecrets, []string{"/outputs/token"}) {
				t.Errorf("changed secrets = %q, want [/outputs/token]", got.ChangedSecrets)
			}

			if len(got.ChangedPaths) != 0 {
				t.Errorf("changed paths = %q, want none", got.ChangedPaths)
			}
		})
	}
}
