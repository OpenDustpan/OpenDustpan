package workspace_test

// Hostile tests for the masked read of a workspace (the tool-neutral read of
// a pinned version): authorize at point of use, the boundary check for the
// version, the value path and the cursor, the limits of the read, and the
// refusals that reach the denial limiter. Each guard of the read has one
// table here, and each case names the layer it attacks. The failing first
// tests of the read live in pkg/stack/masked_read_test.go; this file proves
// the OpenTofu side of the same contract.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup/blobguard/blobguardtest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// testCursorKey returns the key the tests sign cursors with.
func testCursorKey() []byte { return []byte(strings.Repeat("*", 32)) }

// person returns the principal of the tests.
func person(subject string) identity.Principal {
	return identity.Principal{
		Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind: identity.KindPerson, Credential: identity.CredentialAccessToken, JobID: "",
	}
}

// grantAuthorizer allows each action in grants on the object encoded in the
// key of the map. It checks the object, not only the action, as the test for
// the point of use requires.
type grantAuthorizer struct {
	grants map[string][]identity.Action
}

// Authorize implements identity.Authorizer.
func (g grantAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	if slices.Contains(g.grants[obj.String()], action) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, action, obj)
}

// ListObjects implements identity.Authorizer; the masked read never lists.
func (g grantAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// tofuSecretState is a state with two resources and two outputs: a
// terraform_data with one sensitive attribute and one marked input, a
// random_password with a sensitive attribute and a kept mark that names the
// same attribute, and one sensitive output. The values are well-known fakes.
const tofuSecretState = `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"lin-masked",` +
	`"outputs":{"password":{"value":"s3cr3t-out","type":"string","sensitive":true},` +
	`"greeting":{"value":"hello","type":"string"}},` +
	`"resources":[` +
	`{"mode":"managed","type":"terraform_data","name":"smoke",` +
	`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
	`"instances":[{"schema_version":0,` +
	`"attributes":{"id":"smoke-id","input":"s3cr3t-input","output":"s3cr3t-output","triggers_replace":null},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"output"}]]}]},` +
	`{"mode":"managed","type":"random_password","name":"pw",` +
	`"provider":"provider[\"registry.opentofu.org/hashicorp/random\"]",` +
	`"instances":[{"schema_version":3,` +
	`"attributes":{"id":"pw-id","result":"s3cr3t-res","length":16},` +
	`"sensitive_attributes":[[{"type":"get_attr","value":"result"}]]}]}` +
	`]}`

// tofuSecretMarks are the kept marks of the test state: the input of the
// smoke resource (named by the rendering only) and the result of the
// password (named by both the sensitive_attributes and the rendering).
func tofuSecretMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{
		"terraform_data.smoke": {"/input"},
		"random_password.pw":   {"/result"},
	}
}

// maskedLockedFixture is lockedFixture with a state limit that holds the
// test state of this file; the shared fixture caps state at 64 bytes.
func maskedLockedFixture(t *testing.T) fixture {
	t.Helper()

	f := newFixtureSized(t, openFixtureBlobs(t), blobguardtest.None{}, nil, 4<<20)

	if _, err := f.svc.LockWithID(t.Context(), f.ws.ID, "alice", "apply", "lock-1"); err != nil {
		t.Fatalf("lock: %v", err)
	}

	return f
}

// tofuMaskedReadFixture writes the state and returns the fixture with its
// marks stored on the version, and the unit object of the workspace.
func tofuMaskedReadFixture(t *testing.T) (fixture, identity.Object, workspace.StateVersionID) {
	t.Helper()

	f := maskedLockedFixture(t)

	sv, err := f.svc.WriteState(
		t.Context(),
		f.ws.ID,
		workspace.StreamInput{Holder: "alice", LockID: "lock-1", MD5: ""},
		streamAuthority(noOverrides(), nil),
		strings.NewReader(tofuSecretState),
	)
	if err != nil {
		t.Fatalf("write state: %v", err)
	}

	fence := namespace.Fence{Namespace: "acme", FailoverVersion: 1}

	if err := f.store.SaveSensitiveMarks(t.Context(), sv.ID, tofuSecretMarks(), fence); err != nil {
		t.Fatalf("save marks: %v", err)
	}

	return f, unitObjectOf(t, f), sv.ID
}

// TestMaskedReadReplacesSecrets proves the masked read answers the markers
// of every secret value of the version, keyed by the JSON Pointer value
// path, and never a plain secret value. It covers each rule of the
// OpenTofu mask: the sensitive outputs, the sensitive_attributes of an
// instance, and the kept marks of the version.
func TestMaskedReadReplacesSecrets(t *testing.T) {
	t.Parallel()

	f, obj, id := tofuMaskedReadFixture(t)
	pat := person("pat")

	authz := grantAuthorizer{grants: map[string][]identity.Action{
		obj.String(): {identity.ActionUnitRead},
	}}

	want := []string{
		"/outputs/password/value",
		"/resources/0/instances/0/attributes/input",
		"/resources/0/instances/0/attributes/output",
		"/resources/1/instances/0/attributes/result",
	}

	selectors := []struct {
		name     string
		selector workspace.VersionSelector
	}{
		{name: "the current version", selector: workspace.VersionSelector{Latest: true, ID: ""}},
		{name: "the pinned version", selector: workspace.VersionSelector{Latest: false, ID: id}},
	}

	for _, tc := range selectors {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			page, err := f.svc.ReadMasked(t.Context(), workspace.ReadMaskedArgs{
				Ref: f.ws.Ref, Object: obj, Selector: tc.selector,
				Principal: pat, Authorizer: authz, Key: testCursorKey(), Sink: nil, Cursor: "",
			})
			if err != nil {
				t.Fatalf("masked read: %v", err)
			}

			checkMaskedPage(t, page, id, want)
		})
	}
}

// checkMaskedPage checks a one-page answer of the masked read of the test
// state: the version, the markers, the value paths, the strict parse of each
// path, and the absence of every plain secret.
func checkMaskedPage(t *testing.T, page workspace.MaskedPage, id workspace.StateVersionID, want []string) {
	t.Helper()

	if page.Version != string(id) {
		t.Errorf("version = %q, want %q", page.Version, id)
	}

	got := make([]string, 0, len(page.Markers))

	for _, marker := range page.Markers {
		if !marker.Secret {
			t.Errorf("marker %q: Secret = false, want true", marker.Path)
		}

		got = append(got, marker.Path)
	}

	slices.Sort(got)

	if !slices.Equal(got, want) {
		t.Fatalf("markers = %q, want the sensitive output, the marked input, "+
			"the sensitive attribute and the marked result: %q", got, want)
	}

	if !slices.IsSorted(page.ValuePaths) {
		t.Errorf("valuePaths = %q, want the sorted order for stable pages", page.ValuePaths)
	}

	if !slices.Equal(page.ValuePaths, got) {
		t.Errorf("valuePaths = %q, want %q", page.ValuePaths, got)
	}

	checkMaskedPageBytes(t, page, got)
}

// checkMaskedPageBytes checks that the JSON of a page holds no plain secret,
// that each path parses strictly, and that a one-page answer has no cursor.
func checkMaskedPageBytes(t *testing.T, page workspace.MaskedPage, got []string) {
	t.Helper()

	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}

	for _, needle := range []string{"s3cr3t-out", "s3cr3t-input", "s3cr3t-output", "s3cr3t-res"} {
		if strings.Contains(string(raw), needle) {
			t.Errorf("the page holds %q, want markers only", needle)
		}
	}

	// Each JSON Pointer of the page passes the strict parser of the boundary
	// check.
	for _, path := range got {
		if _, err := workspace.ParseValuePath(path); err != nil {
			t.Errorf("value path %q does not parse: %v", path, err)
		}
	}

	if page.NextCursor != "" {
		t.Errorf("nextCursor = %q, want empty for a one-page answer", page.NextCursor)
	}
}

// TestMaskedReadDeniedForACallerWithoutGrant proves the read refuses a
// caller without unit:read on the workspace, with no secret bytes. The HTTP
// shape of the answer (403 with the code unit.forbidden) is the handler
// layer; it is tested at the handler.
func TestMaskedReadDeniedForACallerWithoutGrant(t *testing.T) {
	t.Parallel()

	f, obj, _ := tofuMaskedReadFixture(t)
	pat := person("pat")

	authz := grantAuthorizer{grants: map[string][]identity.Action{}}

	_, err := f.svc.ReadMasked(t.Context(), workspace.ReadMaskedArgs{
		Ref: f.ws.Ref, Object: obj, Selector: workspace.VersionSelector{Latest: true, ID: ""},
		Principal: pat, Authorizer: authz, Key: testCursorKey(), Sink: nil, Cursor: "",
	})
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("err = %v, want the permission denied of the unit", err)
	}

	if strings.Contains(fmt.Sprint(err), "s3cr3t") {
		t.Fatalf("the error holds secret bytes: %v", err)
	}
}
