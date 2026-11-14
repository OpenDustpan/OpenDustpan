package tfe_test

import (
	"context"
	"crypto/md5" //nolint:gosec // The test fixture uses MD5 as the protocol does.
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass and
// TestNoPulumiImport can prove the adapter shape from the sources.
//
//go:embed *.go
var packageSources embed.FS

// publicPatterns lists the routes that need no sign-in. Every other route
// must declare its actions in the route table.
func publicPatterns() []string {
	return []string{
		"GET /.well-known/terraform.json",
		"GET /api/v2/ping",
		"GET /api/v2/test-public",
	}
}

// productionPublicPatterns lists the public routes of the production table:
// every public route without a test-only path.
func productionPublicPatterns() []string {
	return []string{
		"GET /.well-known/terraform.json",
		"GET /api/v2/ping",
	}
}

// uploadPatterns lists the routes that may use the upload scope: the state
// content and JSON rendering uploads carry the capability in the path. Every
// other route must authenticate.
func uploadPatterns() []string {
	return []string{
		"PUT /api/v2/state-uploads/{id}/{token}",
		"PUT /api/v2/json-state-uploads/{id}/{token}",
	}
}

// isUploadAllowed reports whether pattern may use the upload scope.
func isUploadAllowed(pattern string) bool {
	return slices.Contains(uploadPatterns(), pattern)
}

// stubAuthenticator denies every token. The declaration tests send no token,
// so it never runs.
type stubAuthenticator struct{}

func (stubAuthenticator) AuthenticateToken(_ context.Context, _ string) (identity.Principal, error) {
	return identity.Principal{}, identity.ErrUnauthenticated
}

// tokenAuthenticator signs in exactly the test token.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, raw string) (identity.Principal, error) {
	if raw != "test-token" {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject:        "tester",
		DisplayName:    "Tester",
		Admin:          false,
		TokenID:        "",
		TokenExpiresAt: time.Time{},
		Kind:           "",
		Credential:     "",
		JobID:          "",
	}, nil
}

// stubAuthorizer denies every action.
type stubAuthorizer struct{}

func (stubAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (stubAuthorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// readOnlyAuthorizer allows the read actions of a namespace and a unit and
// denies the rest: a caller that sees an object but cannot change it.
type readOnlyAuthorizer struct{}

func (readOnlyAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionUnitRead || action == identity.ActionNamespaceRead {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (readOnlyAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// failingReadAuthorizer denies every action except the read actions, and
// fails the read actions with a store error, as an Authorizer does when its
// store is down.
type failingReadAuthorizer struct{}

func (failingReadAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action == identity.ActionUnitRead || action == identity.ActionNamespaceRead {
		return errStoreDown
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (failingReadAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// errStoreDown is the store error of failingReadAuthorizer.
var errStoreDown = errors.New("store down")

// selectiveAuthorizer denies exactly the actions in deny and allows the rest.
type selectiveAuthorizer struct {
	deny map[identity.Action]bool
}

func (s selectiveAuthorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if s.deny[action] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (selectiveAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// recordedCall is one Authorize call: the action on the object wire form.
type recordedCall struct {
	action identity.Action
	object string
}

// recordingAuthorizer allows every action and records every call, so tests
// prove which object a route authorizes.
type recordingAuthorizer struct {
	calls []recordedCall
}

func (r *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	r.calls = append(r.calls, recordedCall{action: action, object: obj.String()})

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (*recordingAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// denyObjectAuthorizer denies the one object wire form in deny and allows
// everything else, so tests prove a route authorizes the resolved object:
// denying it answers 403.
type denyObjectAuthorizer struct {
	deny string
}

func (d denyObjectAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if obj.String() == d.deny {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

// ListObjects gives an administrator every object and any other principal none.
func (denyObjectAuthorizer) ListObjects(
	_ context.Context,
	p identity.Principal,
	_ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// newObjectHandler returns a production Handler (no test-only routes) with
// the given Authorizer, so the object tests cover the real table.
func newObjectHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, authz, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// stubWorkspaces serves one workspace, acme/web with ID ws-test123, for
// every workspace route. Unknown names and IDs answer workspace.ErrNotFound
// and ws-locked answers workspace.ErrLocked, so the error tests can prove
// the 404 and 409 documents through HTTP. Create answers a new workspace, or
// workspace.ErrAlreadyExists for the existing web name.
type stubWorkspaces struct{}

func (stubWorkspaces) GetWorkspaceByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	return stubWorkspaceByID(id)
}

func (stubWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
	return stubWorkspaceByID(id)
}

func (stubWorkspaces) Get(_ context.Context, ref workspace.Ref) (workspace.Workspace, error) {
	switch {
	case ref.Namespace.String() == "acme" && ref.Name.String() == "web":
		return stubWorkspaceByID(workspace.ID("ws-test123"))
	case ref.Namespace.String() == "acme" && ref.Name.String() == "empty":
		return stubWorkspaceByID(workspace.ID("ws-empty123"))
	default:
		return workspace.Workspace{}, workspace.ErrNotFound
	}
}

// Delete soft-deletes acme/web, answers ErrLocked for acme/locked,
// ErrPendingState for acme/pending, and ErrNotFound for every other workspace.
func (stubWorkspaces) Delete(_ context.Context, ref workspace.Ref, _ audit.Entry) error {
	switch {
	case ref.Namespace.String() != "acme":
		return namespace.ErrNotFound
	case ref.Name.String() == "web":
		return nil
	case ref.Name.String() == "locked":
		return workspace.ErrLocked
	case ref.Name.String() == "pending":
		return workspace.ErrPendingState
	default:
		return workspace.ErrNotFound
	}
}

func (stubWorkspaces) Create(_ context.Context, ref workspace.Ref, _ workspace.Placement) (workspace.Workspace, error) {
	if ref.Namespace.String() != "acme" {
		return workspace.Workspace{}, namespace.ErrNotFound
	}

	if ref.Name.String() == "web" {
		return workspace.Workspace{}, workspace.ErrAlreadyExists
	}

	ns, err := namespace.ParseName(ref.Namespace.String())
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName(ref.Name.String())
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID("ws-" + name.String()),
		Ref:         workspace.Ref{Namespace: ns, Name: name},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

func (stubWorkspaces) List(_ context.Context, ns namespace.Name) ([]workspace.Workspace, error) {
	if ns.String() != "acme" {
		return []workspace.Workspace{}, nil
	}

	ws, err := stubWorkspaceByID(workspace.ID("ws-test123"))
	if err != nil {
		return nil, err
	}

	return []workspace.Workspace{ws}, nil
}

func (stubWorkspaces) Query(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := stubWorkspaces{}.List(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (stubWorkspaces) LockForJob(
	_ context.Context, id workspace.ID, holder, _, _ string,
) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: stubWorkspaceTime(), ID: "lock-test", Holder: holder, Reason: ""}

	return ws, nil
}

func (stubWorkspaces) Unlock(_ context.Context, id workspace.ID, _ string) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	return ws, nil
}

//nolint:revive // The signature of the workspace port that the handler reads.
func (rstubWorkspaces stubWorkspaces) ForceUnlockLock(
	ctx context.Context,
	id workspace.ID,
	_ workspace.Lock,
	by string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	w, err := rstubWorkspaces.forceUnlockUnfenced(ctx, id, by)

	return w, false, err
}

func (stubWorkspaces) Current(_ context.Context, id workspace.ID) (workspace.StateVersion, error) {
	if id.String() != "ws-test123" {
		return workspace.StateVersion{}, workspace.ErrNoState
	}

	return stubStateVersion(), nil
}

// BeginState declares a pending version with a fresh capability. The stub
// ignores the lock and echoes the declared values: route tests prove
// authorization, not the state rules.
func (stubWorkspaces) BeginState(
	_ context.Context, id workspace.ID, holder string, in workspace.NewState, _ workspace.Authority,
) (workspace.PendingState, error) {
	upload, err := token.New()
	if err != nil {
		return workspace.PendingState{}, err
	}

	var sum [16]byte

	if raw, err := hex.DecodeString(in.MD5); err == nil && len(raw) == len(sum) {
		copy(sum[:], raw)
	}

	return workspace.PendingState{
		Upload: upload,
		Version: workspace.StateVersion{
			CreatedAt:       stubWorkspaceTime(),
			FinalizedAt:     time.Time{},
			UploadExpiresAt: stubWorkspaceTime().Add(time.Hour),
			Upload:          token.Digest{},
			ID:              workspace.StateVersionID("sv-new123"),
			Workspace:       id,
			Holder:          holder,
			LockID:          "lock-test",
			Lineage:         in.Lineage,
			Status:          workspace.StatusPending,
			Content:         "",
			Outputs:         in.Outputs,
			MD5:             sum,
			Serial:          in.Serial,
			Size:            0,
			Force:           in.Force,
			Sealed:          false,
			RestoredFrom:    "",
			SensitiveMarks:  nil,
			FenceBy:         "",
			UnlockedWrite:   false, Origin: identity.StateOrigin{Job: "", Commit: ""},
		},
	}, nil
}

// Upload denies every content upload: route tests never reach it with a
// valid capability.
func (stubWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrUploadDenied
}

// CheckUpload allows every capability: route tests never reach it with a
// wrong one unless they override it.
func (stubWorkspaces) CheckUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

// CheckJSONRenderingUpload allows every capability: route tests never reach
// it with a wrong one unless they override it.
func (stubWorkspaces) CheckJSONRenderingUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

// DiscardJSONRendering denies every rendering upload: route tests never
// reach it with a valid capability.
func (stubWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return workspace.ErrUploadDenied
}

// stubStateContent is the state the stub download serves: a tfstate document
// with a greeting output.
func stubStateContent() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"seed-lineage",` +
		`"outputs":{"greeting":{"value":"hello","type":"string"}},"resources":[]}`
}

// stubStateOutputs is the json-state-outputs document of the stub state: the
// flat map of outputs the CLI sends, with the greeting output and its cty
// value and type.
func stubStateOutputs() string {
	return `{"greeting":{"sensitive":false,"value":"hello","type":"string"}}`
}

// stubStateVersion is the finalized current state of ws-test123: serial 3 of
// the seed lineage, with the greeting output.
func stubStateVersion() workspace.StateVersion {
	content := stubStateContent()
	sum := md5.Sum([]byte(content)) //nolint:gosec // The test fixture uses MD5 as the protocol does.

	return workspace.StateVersion{
		CreatedAt:       stubWorkspaceTime(),
		FinalizedAt:     stubWorkspaceTime(),
		UploadExpiresAt: stubWorkspaceTime(),
		Upload:          token.Digest{},
		ID:              workspace.StateVersionID("sv-test123"),
		Workspace:       workspace.ID("ws-test123"),
		Holder:          "tester",
		LockID:          "lock-test",
		Lineage:         "seed-lineage",
		Status:          workspace.StatusFinalized,
		Content:         "namespaces/ns-aaaaaaaaaaaaaaaaaaaa/tofu/ws-test123/sv-test123/seed",
		Outputs:         []byte(stubStateOutputs()),
		MD5:             sum,
		Serial:          3,
		Size:            int64(len(content)),
		Force:           false,
		Sealed:          false,
		RestoredFrom:    "",
		SensitiveMarks:  nil,
		FenceBy:         "",
		UnlockedWrite:   false, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}
}

func (stubWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	switch id.String() {
	case "sv-test123", "sv-second123", "sv-nomarks123", "sv-skipped123", "sv-encrypted123":
		sv := stubStateVersion()
		sv.ID = id

		return sv, nil
	default:
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}
}

// stubResourceState is the tfstate document the masked resource routes read,
// in the shape the CLI writes: a terraform_data resource with a sensitive
// input whose derived output copies the secret without a mark of its own,
// and a provider resource whose schema marks result sensitive without a
// sensitive_attributes entry, so only the kept json-state marks mask it.
func stubResourceState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"seed-lineage",` +
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
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"pw-id","result":"pw-s3cr3t"},` +
		`"sensitive_attributes":[] }]}]}`
}

// stubEncryptedState is a state file the client encrypted: the resources
// hide inside the envelope, so the resource routes answer 409.
func stubEncryptedState() string {
	return `{"serial":3,"lineage":"seed-lineage","meta":{},` +
		`"encrypted_data":"x","encryption_version":"v0"}`
}

// stubResourceMarks are the kept json-state marks of the stub state: the
// input of the data resource, and the result the provider schema marks
// sensitive.
func stubResourceMarks() workspace.SensitiveMarks {
	return workspace.SensitiveMarks{
		"terraform_data.smoke": {"/input"},
		"random_password.pw":   {"/result"},
	}
}

// stubSkippedState is a tfstate document with one valid resource and entries
// the server skips: a non-object and an object without type and name, so the
// list reads incomplete.
func stubSkippedState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":3,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id",` +
		`"input":{"value":"s3cr3t","type":"string"},` +
		`"output":{"value":"s3cr3t","type":"string"},"triggers_replace":null},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]},` +
		`"junk",{"mode":"managed"}]}`
}

// stubResourceBytes returns the state bytes and the kept marks of the masked
// resource routes for id, or ErrStateVersionNotFound. The version with no
// marks reads the same state without them, so every attribute value is a
// marker. The second version changes the sensitive input and adds one
// resource, so the diff route has a change to answer. The version with
// skipped entries reads one valid resource with the list marked incomplete.
func stubResourceBytes(id workspace.StateVersionID) ([]byte, workspace.SensitiveMarks, error) {
	switch id.String() {
	case "sv-test123":
		return []byte(stubResourceState()), stubResourceMarks(), nil
	case "sv-second123":
		return []byte(stubSecondState()), stubResourceMarks(), nil
	case "sv-nomarks123":
		return []byte(stubResourceState()), nil, nil
	case "sv-skipped123":
		return []byte(stubSkippedState()), stubResourceMarks(), nil
	case "sv-encrypted123":
		return []byte(stubEncryptedState()), stubResourceMarks(), nil
	default:
		return nil, nil, workspace.ErrStateVersionNotFound
	}
}

// stubSecondState is the state of the second stub version: the sensitive
// input changed and one resource added, so the diff of the two stub versions
// answers an added and a changed resource with no secret value.
func stubSecondState() string {
	return `{"version":4,"terraform_version":"1.11.0","serial":4,"lineage":"seed-lineage",` +
		`"outputs":{},"resources":[` +
		`{"mode":"managed","type":"terraform_data","name":"smoke",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"smoke-id",` +
		`"input":{"value":"other","type":"string"},` +
		`"output":{"value":"other","type":"string"},"triggers_replace":null},` +
		`"sensitive_attributes":[[{"type":"get_attr","value":"input"}]] }]},` +
		`{"mode":"managed","type":"random_password","name":"pw",` +
		`"provider":"provider[\"registry.opentofu.org/hashicorp/random\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"pw-id","result":"pw-s3cr3t"},` +
		`"sensitive_attributes":[] }]},` +
		`{"mode":"managed","type":"terraform_data","name":"added",` +
		`"provider":"provider[\"terraform.io/builtin/terraform\"]",` +
		`"instances":[{"schema_version":0,` +
		`"attributes":{"id":"added-id","input":"added"},` +
		`"sensitive_attributes":[] }]}]}`
}

func (stubWorkspaces) ReadResources(
	_ context.Context, id workspace.StateVersionID, cursor string, size int, filter string,
) (workspace.StatePage, error) {
	raw, _, err := stubResourceBytes(id)
	if err != nil {
		return workspace.StatePage{}, err
	}

	if encrypted, derr := workspace.IsClientEncrypted(raw); derr != nil {
		return workspace.StatePage{}, derr
	} else if encrypted {
		return workspace.StatePage{}, fmt.Errorf("read resources of %s: %w", id, workspace.ErrClientEncrypted)
	}

	return workspace.ListStateResources(raw, nil, cursor, size, filter)
}

func (stubWorkspaces) ReadResource(
	_ context.Context, id workspace.StateVersionID, address string,
) (workspace.StateDetail, error) {
	raw, marks, err := stubResourceBytes(id)
	if err != nil {
		return workspace.StateDetail{}, err
	}

	if encrypted, derr := workspace.IsClientEncrypted(raw); derr != nil {
		return workspace.StateDetail{}, derr
	} else if encrypted {
		return workspace.StateDetail{}, fmt.Errorf("read resource of %s: %w", id, workspace.ErrClientEncrypted)
	}

	return workspace.ReadStateResource(raw, marks, address)
}

// stubStateAtFirst is the finalize time of the first stub state version in
// Unix milliseconds. The stub holds sv-test123 from this time, sv-second123
// from 2000 ms and sv-encrypted123 from 3000 ms.
const stubStateAtFirst = 1000

func (stubWorkspaces) ReadStateAt(
	ctx context.Context, _ workspace.ID, req workspace.StateAtRequest,
) (workspace.StateAtRead, error) {
	return stubReadStateAt(ctx, req)
}

// stubReadStateAt answers the state at a time from the stub state versions.
func stubReadStateAt(ctx context.Context, req workspace.StateAtRequest) (workspace.StateAtRead, error) {
	stub := stubWorkspaces{}
	id, finalized := workspace.StateVersionID("sv-test123"), int64(stubStateAtFirst)

	switch ms := req.At.UnixMilli(); {
	case ms < stubStateAtFirst:
		return workspace.StateAtRead{}, fmt.Errorf(
			"%w: the first state version of acme/web is at 1970-01-01T00:00:01Z (%d ms)",
			&workspace.NoStateAtError{First: time.UnixMilli(stubStateAtFirst).UTC()}, stubStateAtFirst,
		)
	case ms >= 3000:
		id, finalized = "sv-encrypted123", 3000
	case ms >= 2000:
		id, finalized = "sv-second123", 2000
	default:
		// The first state version answers.
	}

	sv, err := stub.ReadState(ctx, id)
	if err != nil {
		return workspace.StateAtRead{}, err
	}

	out := workspace.StateAtRead{
		Page:    workspace.StatePage{Resources: nil, NextCursor: "", Incomplete: false},
		Version: sv,
		State:   workspace.StateAt{FinalizedAt: time.UnixMilli(finalized).UTC(), By: "alice", StateVersion: id},
		Names:   inventory.Names{AtTime: "", Current: ""},
		Opaque:  false,
	}

	out.Page, err = stub.ReadResources(ctx, id, req.Cursor, req.Size, req.Filter)
	if errors.Is(err, workspace.ErrClientEncrypted) {
		out.Page = workspace.StatePage{Resources: nil, NextCursor: "", Incomplete: false}
		out.Opaque = true

		return out, nil
	}

	return out, err
}

func (stubWorkspaces) DiffStates(
	_ context.Context, to, from workspace.StateVersionID, cursor string, size int,
) (workspace.StateDiff, error) {
	if from.String() == "sv-other123" {
		return workspace.StateDiff{}, fmt.Errorf("%w: state version %s", workspace.ErrStateVersionNotFound, from)
	}

	toRaw, toMarks, err := stubResourceBytes(to)
	if err != nil {
		return workspace.StateDiff{}, err
	}

	fromRaw, fromMarks, err := stubResourceBytes(from)
	if err != nil {
		return workspace.StateDiff{}, err
	}

	for _, raw := range [][]byte{toRaw, fromRaw} {
		encrypted, derr := workspace.IsClientEncrypted(raw)
		if derr != nil {
			return workspace.StateDiff{}, derr
		}

		if encrypted {
			return workspace.StateDiff{}, fmt.Errorf("diff states: %w", workspace.ErrClientEncrypted)
		}
	}

	return workspace.DiffStates(
		workspace.StateWithMarks{State: fromRaw, Marks: fromMarks},
		workspace.StateWithMarks{State: toRaw, Marks: toMarks},
		cursor, size,
	)
}

func (stubWorkspaces) Open(_ context.Context, sv workspace.StateVersion) (io.ReadCloser, error) {
	if sv.ID.String() != "sv-test123" || sv.Status != workspace.StatusFinalized {
		return nil, workspace.ErrStateVersionNotFound
	}

	return io.NopCloser(strings.NewReader(stubStateContent())), nil
}

// Restore makes the stub state current again as a new, forced version, so
// the route tests prove authorization, not the state rules.
func (stubWorkspaces) Restore(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	src, err := stubWorkspaces{}.ReadState(ctx, from)
	if err != nil {
		return workspace.StateVersion{}, err
	}

	if src.Workspace != id {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	restored := src
	restored.ID = workspace.StateVersionID("sv-restored123")
	restored.Force = true
	restored.RestoredFrom = from
	restored.CreatedAt = stubWorkspaceTime()
	restored.FinalizedAt = stubWorkspaceTime()

	return restored, nil
}

// ReadRollbackChange answers a change of the stub state: the current version
// of the stub, no removed address and one resource.
func (stubWorkspaces) ReadRollbackChange(
	ctx context.Context, id workspace.ID, to workspace.StateVersionID,
) (workspace.RollbackChange, error) {
	src, err := stubWorkspaces{}.ReadState(ctx, to)
	if err != nil {
		return workspace.RollbackChange{}, err
	}

	if src.Workspace != id {
		return workspace.RollbackChange{}, workspace.ErrStateVersionNotFound
	}

	cur, err := stubWorkspaces{}.Current(ctx, id)
	if err != nil {
		return workspace.RollbackChange{}, err
	}

	return workspace.RollbackChange{Current: cur.ID, Resources: 1, Removed: []string{}, Encrypted: false}, nil
}

// RestoreExpecting restores as Restore does, and fails when expectCurrent is
// not the current version of the stub.
func (stubWorkspaces) RestoreExpecting(
	ctx context.Context,
	id workspace.ID,
	from workspace.StateVersionID,
	by string,
	expectCurrent workspace.StateVersionID,
) (workspace.StateVersion, error) {
	cur, err := stubWorkspaces{}.Current(ctx, id)
	if err != nil {
		return workspace.StateVersion{}, err
	}

	if cur.ID != expectCurrent {
		return workspace.StateVersion{}, workspace.ErrConcurrentModification
	}

	return stubWorkspaces{}.Restore(ctx, id, from, by)
}

// History serves the stub state of ws-test123 as a list of one, so the list
// route test proves authorization, not the state rules.
func (stubWorkspaces) History(
	_ context.Context, id workspace.ID, _ workspace.Page,
) ([]workspace.StateVersion, int, error) {
	if id.String() != "ws-test123" {
		return nil, 0, nil
	}

	return []workspace.StateVersion{stubStateVersion()}, 1, nil
}

func (stubWorkspaces) forceUnlockUnfenced(_ context.Context, id workspace.ID, _ string) (workspace.Workspace, error) {
	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	ws.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}

	return ws, nil
}

// stubWorkspaceTime stamps every stub workspace.
func stubWorkspaceTime() time.Time {
	return time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)
}

// stubWorkspaceByID resolves workspace IDs: ws-test123 is acme/web with
// state, ws-empty123 is acme/empty without state. Unknown IDs answer
// workspace.ErrNotFound and ws-locked answers workspace.ErrLocked.
func stubWorkspaceByID(id workspace.ID) (workspace.Workspace, error) {
	var name string

	switch id.String() {
	case "ws-locked":
		return workspace.Workspace{}, workspace.ErrLocked
	case "ws-test123":
		name = "web"
	case "ws-empty123":
		name = "empty"
	default:
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	parsed, err := workspace.ParseName(name)
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: time.Time{},
			ID:       "",
			Holder:   "",
			Reason:   "",
		},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns, Name: parsed},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

func newTestHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Registry:       nil,
		Authenticator:  stubAuthenticator{},
		Authorizer:     stubAuthorizer{},
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, stubAuthorizer{}, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

func newEnforcingHandler(t *testing.T, authz identity.Authorizer) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, authz, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// newProductionHandler returns a Handler with only the production table: no
// test-only extra routes.
func newProductionHandler(t *testing.T) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  stubAuthenticator{},
		Authorizer:     stubAuthorizer{},
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stubWorkspaces{},
		OutputChecker:  newTestOutputChecker(t, stubAuthorizer{}, stubWorkspaces{}),
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// stubNamespaceStore holds only the acme namespace, so the entitlement set
// answers 200 for acme and 404 for every other organization.
type stubNamespaceStore struct{}

func (stubNamespaceStore) CreateNamespace(_ context.Context, _ namespace.Namespace) error {
	return nil
}

func (stubNamespaceStore) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name.String() != "acme" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Time{},
		Name:                name,
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (stubNamespaceStore) ListNamespaces(ctx context.Context) ([]namespace.Namespace, error) {
	ns, err := stubNamespaceStore{}.GetNamespace(ctx, namespace.Name("acme"))
	if err != nil {
		return nil, err
	}

	return []namespace.Namespace{ns}, nil
}

func (stubNamespaceStore) Ping(_ context.Context) error {
	return nil
}

// newTestNamespaces returns a namespace service over the stub store.
func newTestNamespaces(t *testing.T) *namespace.Service {
	t.Helper()

	svc, err := namespace.NewService(
		namespace.Config{Store: stubNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	return svc
}

// TestRoutesDeclareActions walks every registered route and fails for a
// route with no declared action. It runs on the test routes and on the real
// table: every served pattern has a table row that passes the scope check,
// and every table row is served.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	t.Run("test routes", func(t *testing.T) {
		t.Parallel()
		checkRoutesDeclareActions(t, newTestHandler(t), publicPatterns())
	})

	t.Run("production table", func(t *testing.T) {
		t.Parallel()
		checkRoutesDeclareActions(t, newProductionHandler(t), productionPublicPatterns())
	})
}

// checkRoutesDeclareActions checks both directions: every served pattern has
// a table row that passes the scope check, and every table row is served.
func checkRoutesDeclareActions(t *testing.T, handler *tfe.Handler, publicList []string) {
	t.Helper()

	public := map[string]bool{}
	for _, pattern := range publicList {
		public[pattern] = true
	}

	byPattern := map[string]tfe.Route{}
	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRouteScope(t, route, public)
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("served route %q has no declared action", pattern)
		}
	}

	for pattern := range public {
		if _, ok := byPattern[pattern]; !ok {
			t.Fatalf("public route %q is not declared", pattern)
		}
	}

	checkUploadRoutesDeclared(t, byPattern)

	for _, route := range byPattern {
		probeRoute(t, handler, route)
	}
}

// checkUploadRoutesDeclared fails for an upload list entry with no table row
// or with a scope other than the upload scope. It keeps the allowlist from
// drifting from the production table.
func checkUploadRoutesDeclared(t *testing.T, byPattern map[string]tfe.Route) {
	t.Helper()

	for _, pattern := range uploadPatterns() {
		route, ok := byPattern[pattern]
		if !ok {
			t.Fatalf("upload route %q is not declared", pattern)
		}

		if route.Scope != tfe.ScopeUpload {
			t.Fatalf("upload route %q uses scope %d, want the upload scope", pattern, route.Scope)
		}
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go, where Handle and HandleFunc
// both record. Test files are skipped; they never serve production routes.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// TestNoPulumiImport proves the TFE adapter never calls the other tool
// adapter: no source in this package imports it. The needle
// is built by concatenation so this file does not contain it literally.
func TestNoPulumiImport(t *testing.T) {
	t.Parallel()

	needle := "api/" + "pulumi"

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		content, err := packageSources.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read file %s: %v", entry.Name(), err)
		}

		if strings.Contains(string(content), needle) {
			t.Fatalf("file %s imports the other tool adapter", entry.Name())
		}
	}
}

// errInvalidRoute reports a route table row that fails the scope check.
var errInvalidRoute = errors.New("invalid route")

// validateRoute fails for a route that is neither public, authorized with
// actions and a resolver, nor an upload route with the capability scope.
func validateRoute(route tfe.Route, public map[string]bool) error {
	switch route.Scope {
	case tfe.ScopePublic:
		return validatePublicRoute(route, public)
	case tfe.ScopeAuthorized:
		return validateAuthorizedRoute(route, public)
	case tfe.ScopeConsole:
		if route.Method+" "+route.Path != revealPattern() {
			return fmt.Errorf("%w: %q uses the console scope", errInvalidRoute, route.Path)
		}

		return validateAuthorizedRoute(route, public)
	case tfe.ScopeRegistry, tfe.ScopeSigned:
		// The registry routes are not in this table. registry_test.go checks them.
		return fmt.Errorf("%w: %q uses a registry scope", errInvalidRoute, route.Path)
	case tfe.ScopeUpload:
		return validateUploadRoute(route, public)
	default:
		return fmt.Errorf("%w: %q declares no scope", errInvalidRoute, route.Method+" "+route.Path)
	}
}

// validatePublicRoute fails for a public route that is not on the public
// list or that declares actions or a resolver.
func validatePublicRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if !public[pattern] {
		return fmt.Errorf("%w: %q is public but not on the public list", errInvalidRoute, pattern)
	}

	if len(route.Actions) != 0 {
		return fmt.Errorf("%w: public route %q declares actions", errInvalidRoute, pattern)
	}

	if route.Resolve != nil {
		return fmt.Errorf("%w: public route %q declares a resolver", errInvalidRoute, pattern)
	}

	return nil
}

// validateAuthorizedRoute fails for an authorized route that is on the
// public list or that declares no action or no resolver.
func validateAuthorizedRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if public[pattern] {
		return fmt.Errorf("%w: %q is on the public list but declares actions", errInvalidRoute, pattern)
	}

	if len(route.Actions) == 0 {
		return fmt.Errorf("%w: %q declares no action", errInvalidRoute, pattern)
	}

	if route.Resolve == nil {
		return fmt.Errorf("%w: %q declares actions but no resolver", errInvalidRoute, pattern)
	}

	return nil
}

// validateUploadRoute fails for an upload route that is not on the upload
// list, that is on the public list, that declares anything but unit:write,
// or that declares a resolver. The capability in the path stands in for
// unit:write granted at creation, so the Authorizer does not run and no
// resolver is needed.
func validateUploadRoute(route tfe.Route, public map[string]bool) error {
	pattern := route.Method + " " + route.Path

	if !isUploadAllowed(pattern) {
		return fmt.Errorf("%w: %q uses the upload scope but is not on the upload list", errInvalidRoute, pattern)
	}

	if public[pattern] {
		return fmt.Errorf("%w: %q is on the public list but declares the upload scope", errInvalidRoute, pattern)
	}

	if len(route.Actions) != 1 || route.Actions[0] != identity.ActionUnitWrite {
		return fmt.Errorf(
			"%w: upload route %q declares %v, want exactly [unit:write]",
			errInvalidRoute,
			pattern,
			route.Actions,
		)
	}

	if route.Resolve != nil {
		return fmt.Errorf("%w: upload route %q declares a resolver", errInvalidRoute, pattern)
	}

	return nil
}

// checkRouteScope fails the test for a route that validateRoute rejects.
func checkRouteScope(t *testing.T, route tfe.Route, public map[string]bool) {
	t.Helper()

	if err := validateRoute(route, public); err != nil {
		t.Fatalf("%v", err)
	}
}

// TestRouteWithoutActionsFails proves a route without actions fails the
// route test.
func TestRouteWithoutActionsFails(t *testing.T) {
	t.Parallel()

	route := tfe.Route{
		Method:  http.MethodGet,
		Path:    "/api/v2/test",
		Scope:   tfe.ScopeAuthorized,
		Actions: nil,
		Resolve: nil,
		Serve:   nil,
	}

	if err := validateRoute(route, map[string]bool{}); err == nil {
		t.Fatal("route without actions passed, want an error")
	}
}

// TestPublicRouteNotOnListFails proves a public route that is not on the
// public list fails the route test.
func TestPublicRouteNotOnListFails(t *testing.T) {
	t.Parallel()

	route := tfe.Route{
		Method:  http.MethodGet,
		Path:    "/api/v2/hidden",
		Scope:   tfe.ScopePublic,
		Actions: nil,
		Resolve: nil,
		Serve:   nil,
	}

	if err := validateRoute(route, map[string]bool{}); err == nil {
		t.Fatal("public route not on the list passed, want an error")
	}
}

// TestUploadRouteNotOnListFails proves an upload route that is not on the
// upload list fails the route test.
func TestUploadRouteNotOnListFails(t *testing.T) {
	t.Parallel()

	route := tfe.Route{
		Method:  http.MethodPut,
		Path:    "/api/v2/hidden-upload/{id}/{token}",
		Scope:   tfe.ScopeUpload,
		Actions: []identity.Action{identity.ActionUnitWrite},
		Resolve: nil,
		Serve:   nil,
	}

	if err := validateRoute(route, map[string]bool{}); err == nil {
		t.Fatal("upload route not on the list passed, want an error")
	}
}

// probeRoute checks that a declared route is registered: a request without a
// token must fail closed, never with 404.
func probeRoute(t *testing.T, handler *tfe.Handler, route tfe.Route) {
	t.Helper()

	req := routeRequest(t, route, "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusNotFound {
		t.Fatalf("route %q is declared but not served", route.Method+" "+route.Path)
	}
}

// routeRequest builds a valid request for route: path placeholders filled
// and a JSON body where the route reads one. raw is sent as a Bearer
// Authorization header when not empty.
func routeRequest(t *testing.T, route tfe.Route, raw string) *http.Request {
	t.Helper()

	path := strings.NewReplacer(
		"{organization}", "acme",
		"{workspace}", "web",
		"{id}", "ws-test123",
		"{workspace_id}", "ws-test123",
		"{at}", "1500",
		"{state_version_id}", "sv-test123",
		"{output_id}", "sv-test123.greeting",
		"{address}", "terraform_data.smoke",
		"{token}", "wrong-token",
	).Replace(route.Path)

	if route.Method == http.MethodGet && route.Path == "/api/v2/state-versions" {
		path += "?filter[organization][name]=acme&filter[workspace][name]=web"
	}

	if route.Method == http.MethodGet && route.Path == "/api/v2/state-versions/{state_version_id}/diff" {
		path += "?from=sv-test123"
	}

	body := ""
	if route.Method == http.MethodPut && strings.HasSuffix(route.Path, "/override-switches") {
		body = `{"allow-forced-writes":false,"allow-unlocked-writes":false}`
	}

	if route.Method == http.MethodPost {
		body = `{"data":{}}`
		if strings.HasSuffix(route.Path, "/workspaces") {
			body = `{"data":{"type":"workspaces","attributes":{"name":"fresh"}}}`
		}

		if strings.HasSuffix(route.Path, "/state-versions") {
			body = `{"data":{"type":"state-versions","attributes":{` +
				`"serial":4,"lineage":"seed-lineage","md5":"d41d8cd98f00b204e9800998ecf8427e"}}}`
		}
	}

	req := httptest.NewRequestWithContext(t.Context(), route.Method, path, strings.NewReader(body))

	if raw != "" {
		req.Header.Set("Authorization", "Bearer "+raw)
	}

	return req
}

// TestRoutesEnforceAuthentication proves the wrapper enforces the table. It
// is table-driven over every authorized route: without a token it answers
// 401, with a wrong token it answers 401, and with a token the Authorizer
// denies it answers 403. It runs on the test routes and on the real table.
func TestRoutesEnforceAuthentication(t *testing.T) {
	t.Parallel()

	t.Run("test routes", func(t *testing.T) {
		t.Parallel()
		checkAuthTable(t, newTestHandler(t))
	})

	t.Run("production table", func(t *testing.T) {
		t.Parallel()
		checkAuthTable(t, newProductionHandler(t))
	})
}

// checkAuthTable proves every authorized route of handler answers 401
// without a token, 401 with a wrong token, 403 when denied, and 200 when
// allowed. The reveal route is skipped: it needs a state with a masked
// secret, so its dedicated test proves it instead.
func checkAuthTable(t *testing.T, handler *tfe.Handler) {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Scope != tfe.ScopeAuthorized {
			continue
		}

		if route.Method+" "+route.Path == revealPattern() {
			continue
		}

		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteAuthentication(t, route)
		})
	}
}

// checkRouteAuthentication proves one authorized route answers 401 without a
// token, 401 with a wrong token, 403 when denied, and 200 when allowed.
func checkRouteAuthentication(t *testing.T, route tfe.Route) {
	t.Helper()

	checkRouteAuthenticationUnauthorized(t, route)
	checkRouteAuthenticationDenied(t, route)
	checkRouteAuthenticationAllowed(t, route)
}

// checkRouteAuthenticationUnauthorized proves one authorized route answers
// 401 without a token, with a wrong token, and with only a console session
// cookie: the TFE-compatible API does not take the cookie.
func checkRouteAuthenticationUnauthorized(t *testing.T, route tfe.Route) {
	t.Helper()

	t.Run("session cookie", func(t *testing.T) {
		t.Parallel()

		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		req := routeRequest(t, route, "")
		req.AddCookie(&http.Cookie{
			Name: sessionauth.Cookie, Value: "session-secret", Path: "/", Secure: true, HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		req.Header.Set(sessionauth.CSRFHeader, "csrf-value")
		req.Header.Set("Sec-Fetch-Site", "same-origin")

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("request with the session cookie answers %d, want %d", rec.Code, http.StatusUnauthorized)
		}

		checkJSONAPIError(t, rec, http.StatusUnauthorized)
	})

	cases := []struct {
		name  string
		token string
		want  int
	}{
		{name: "no token", token: "", want: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong-token", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

			rec := httptest.NewRecorder()
			allowed.ServeHTTP(rec, routeRequest(t, route, tc.token))

			if rec.Code != tc.want {
				t.Fatalf("request answers %d, want %d", rec.Code, tc.want)
			}

			checkJSONAPIError(t, rec, tc.want)
		})
	}
}

// checkRouteAuthenticationDenied proves one authorized route answers 403
// when the Authorizer denies it.
func checkRouteAuthenticationDenied(t *testing.T, route tfe.Route) {
	t.Helper()

	t.Run("denied", func(t *testing.T) {
		t.Parallel()

		deny := map[identity.Action]bool{}
		for _, action := range route.Actions {
			deny[action] = true
		}

		denied := newEnforcingHandler(t, selectiveAuthorizer{deny: deny})

		rec := httptest.NewRecorder()
		denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		// 403 when the caller can still read the object, and the 404 of an
		// unknown object when the denied actions include its read.
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Fatalf("denied request answers %d, want %d or %d", rec.Code, http.StatusForbidden, http.StatusNotFound)
		}

		checkJSONAPIError(t, rec, rec.Code)
	})
}

// checkRouteAuthenticationAllowed proves one authorized route serves when the
// Authorizer allows it: a creation answers 201, a delete 204 and every other
// route 200.
func checkRouteAuthenticationAllowed(t *testing.T, route tfe.Route) {
	t.Helper()

	t.Run("allowed", func(t *testing.T) {
		t.Parallel()

		allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

		rec := httptest.NewRecorder()
		allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

		want := wantSuccessStatus(route)

		if rec.Code != want {
			t.Fatalf("allowed request answers %d, want %d", rec.Code, want)
		}

		if got := rec.Header().Get("Content-Type"); got != wantMediaType(route) {
			t.Fatalf("media type = %q, want %q", got, wantMediaType(route))
		}
	})
}

// wantSuccessStatus returns the success status of route: a creation answers
// 201 and every other route answers 200.
func wantSuccessStatus(route tfe.Route) int {
	if route.Method == http.MethodDelete {
		return http.StatusNoContent
	}

	if route.Method == http.MethodPost &&
		(strings.HasSuffix(route.Path, "/workspaces") || strings.HasSuffix(route.Path, "/state-versions")) {
		return http.StatusCreated
	}

	return http.StatusOK
}

// wantMediaType returns the media type route serves on success: the download
// route streams raw state as application/json, every other route answers
// JSON:API.
func wantMediaType(route tfe.Route) string {
	if route.Method == http.MethodDelete {
		return ""
	}

	if route.Method == http.MethodGet && route.Path == "/api/v2/state-versions/{state_version_id}/download" {
		return "application/json"
	}

	return "application/vnd.api+json"
}

// jsonAPIErrorItem is one entry of a JSON:API error document.
type jsonAPIErrorItem struct {
	Status string `json:"status"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// jsonAPIErrorDoc decodes a JSON:API error document in tests.
type jsonAPIErrorDoc struct {
	Errors []jsonAPIErrorItem `json:"errors"`
}

// resolveIDAttributes is the name attribute in the ID test body.
type resolveIDAttributes struct {
	Name string `json:"name"`
}

// resolveIDData is the data object in the ID test body.
type resolveIDData struct {
	Attributes resolveIDAttributes `json:"attributes"`
}

// resolveIDBody is the body the ID resolver restores for the handler.
type resolveIDBody struct {
	Data resolveIDData `json:"data"`
}

// checkJSONAPIError proves rec is a JSON:API error with the media type and
// an errors array of status, title and detail. Status is the HTTP status
// code as a string, for example "404".
func checkJSONAPIError(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("status = %d, want %d", rec.Code, want)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) == 0 {
		t.Fatal("error document holds no errors")
	}

	wantStatus := strconv.Itoa(want)

	for _, item := range doc.Errors {
		if item.Status == "" || item.Title == "" || item.Detail == "" {
			t.Fatalf("error entry %+v misses status, title or detail", item)
		}

		if item.Status != wantStatus {
			t.Fatalf("error status = %q, want %q", item.Status, wantStatus)
		}
	}
}

// TestErrorDocuments proves every error helper writes a JSON:API document
// with status, title and detail and the media type. Status is the HTTP
// status code as a string: "401", "403", "404", "409" and "422" ("500" is
// covered by TestErrorHidesInternalDetail).
func TestErrorDocuments(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	cases := []struct {
		name   string
		target string
		want   int
		deny   bool
	}{
		{name: "unauthorized", target: "/api/v2/test-by-name/acme/web", want: http.StatusUnauthorized, deny: false},
		{name: "hidden", target: "/api/v2/test-by-name/acme/web", want: http.StatusNotFound, deny: true},
		{name: "not found", target: "/api/v2/test-by-id/ws-missing", want: http.StatusNotFound, deny: false},
		{name: "conflict", target: "/api/v2/test-by-id/ws-locked", want: http.StatusConflict, deny: false},
		{
			name:   "unprocessable",
			target: "/api/v2/test-by-name/acme/bad!",
			want:   http.StatusUnprocessableEntity,
			deny:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var rec *httptest.ResponseRecorder

			switch {
			case tc.want == http.StatusUnauthorized:
				rec = httptest.NewRecorder()
				handler.ServeHTTP(rec, httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				))
			case tc.deny:
				// A caller that cannot read the workspace gets the 404 of an
				// unknown workspace. TestUnknownAndHiddenIDsAnswerTheSame
				// covers the 403 of a caller that reads but cannot write.
				denied := newEnforcingHandler(t, stubAuthorizer{})
				req := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				)
				req.Header.Set("Authorization", "Bearer test-token")
				rec = httptest.NewRecorder()
				denied.ServeHTTP(rec, req)
			default:
				allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})
				req := httptest.NewRequestWithContext(
					t.Context(),
					http.MethodPost,
					tc.target,
					strings.NewReader(`{"data":{}}`),
				)
				req.Header.Set("Authorization", "Bearer test-token")
				rec = httptest.NewRecorder()
				allowed.ServeHTTP(rec, req)
			}

			if rec.Code != tc.want {
				t.Fatalf("request answers %d, want %d", rec.Code, tc.want)
			}

			checkJSONAPIError(t, rec, tc.want)
		})
	}
}

// errorWorkspaces fails every workspace use case with an internal error, so
// the 500 path stays covered now that the workspace service is required.
type errorWorkspaces struct{}

func (errorWorkspaces) Create(_ context.Context, _ workspace.Ref, _ workspace.Placement) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) Delete(_ context.Context, _ workspace.Ref, _ audit.Entry) error {
	return errTestInternal
}

func (errorWorkspaces) Get(_ context.Context, _ workspace.Ref) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) GetByID(_ context.Context, _ workspace.ID) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) List(_ context.Context, _ namespace.Name) ([]workspace.Workspace, error) {
	return nil, errTestInternal
}

func (errorWorkspaces) Query(ctx context.Context, q workspace.ListQuery) (workspace.ListPage, error) {
	all, err := errorWorkspaces{}.List(ctx, q.Namespace)
	if err != nil {
		return workspace.ListPage{}, err
	}

	return workspace.PageWorkspaces(all, q), nil
}

func (errorWorkspaces) LockForJob(
	_ context.Context, _ workspace.ID, _, _, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

func (errorWorkspaces) Unlock(
	_ context.Context, _ workspace.ID, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

//nolint:revive // The signature of the workspace port that the handler reads.
func (rerrorWorkspaces errorWorkspaces) ForceUnlockLock(
	ctx context.Context,
	id workspace.ID,
	_ workspace.Lock,
	by string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	w, err := rerrorWorkspaces.forceUnlockUnfenced(ctx, id, by)

	return w, false, err
}

func (errorWorkspaces) Current(_ context.Context, _ workspace.ID) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) BeginState(
	_ context.Context, _ workspace.ID, _ string, _ workspace.NewState, _ workspace.Authority,
) (workspace.PendingState, error) {
	return workspace.PendingState{}, errTestInternal
}

func (errorWorkspaces) Upload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) CheckUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

func (errorWorkspaces) CheckJSONRenderingUpload(
	_ context.Context, _ workspace.StateVersionID, _ token.Token,
) (namespace.Name, error) {
	return "acme", nil
}

func (errorWorkspaces) DiscardJSONRendering(
	_ context.Context, _ workspace.StateVersionID, _ token.Token, _ io.Reader,
) error {
	return errTestInternal
}

func (errorWorkspaces) ReadState(
	_ context.Context, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) ReadResources(
	_ context.Context, _ workspace.StateVersionID, _ string, _ int, _ string,
) (workspace.StatePage, error) {
	return workspace.StatePage{}, errTestInternal
}

func (errorWorkspaces) ReadResource(
	_ context.Context, _ workspace.StateVersionID, _ string,
) (workspace.StateDetail, error) {
	return workspace.StateDetail{}, errTestInternal
}

func (errorWorkspaces) ReadStateAt(
	_ context.Context, _ workspace.ID, _ workspace.StateAtRequest,
) (workspace.StateAtRead, error) {
	return workspace.StateAtRead{}, errTestInternal
}

func (errorWorkspaces) DiffStates(
	_ context.Context, _, _ workspace.StateVersionID, _ string, _ int,
) (workspace.StateDiff, error) {
	return workspace.StateDiff{}, errTestInternal
}

func (errorWorkspaces) Open(_ context.Context, _ workspace.StateVersion) (io.ReadCloser, error) {
	return nil, errTestInternal
}

func (errorWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) ReadRollbackChange(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID,
) (workspace.RollbackChange, error) {
	return workspace.RollbackChange{}, errTestInternal
}

func (errorWorkspaces) RestoreExpecting(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string, _ workspace.StateVersionID,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, errTestInternal
}

func (errorWorkspaces) History(
	_ context.Context, _ workspace.ID, _ workspace.Page,
) ([]workspace.StateVersion, int, error) {
	return nil, 0, errTestInternal
}

func (errorWorkspaces) forceUnlockUnfenced(_ context.Context, _ workspace.ID, _ string) (workspace.Workspace, error) {
	return workspace.Workspace{}, errTestInternal
}

// errTestInternal is the internal failure errorWorkspaces answers with.
var errTestInternal = errors.New("test internal failure")

// TestErrorHidesInternalDetail proves a 500 never returns an internal error
// text.
func TestErrorHidesInternalDetail(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandlerWithRoutes(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     selectiveAuthorizer{deny: map[identity.Action]bool{}},
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     errorWorkspaces{},
		OutputChecker:  nil,
		BaseURL:        baseurl.Resolver{},
		Audit:          newTestAudit(),
		Gate:           nil,
		MaxStateBytes:  0,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		SessionPolicy:  nil,
		StateGuard:     stategate.Off("the test keeps no gates"),
		Placer:         testPlacer(),
	}, tfe.ExtraRoutes)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-test123",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("request answers %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	checkJSONAPIError(t, rec, http.StatusInternalServerError)

	var doc jsonAPIErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	if doc.Errors[0].Status != strconv.Itoa(http.StatusInternalServerError) {
		t.Fatalf("error status = %q, want %q", doc.Errors[0].Status, "500")
	}

	if strings.Contains(doc.Errors[0].Detail, "lookup") {
		t.Fatalf("internal error leaks detail %q", doc.Errors[0].Detail)
	}
}

// TestProductionTableHasNoTestRoutes proves the production table serves no
// test path: test-only routes reach only handlers built with
// NewHandlerWithRoutes.
func TestProductionTableHasNoTestRoutes(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	for _, route := range handler.Routes() {
		if strings.Contains(route.Path, "test") {
			t.Fatalf("production route %q contains test", route.Method+" "+route.Path)
		}
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if strings.Contains(pattern, "test") {
			t.Fatalf("production pattern %q contains test", pattern)
		}
	}
}

// TestRoutesAuthorizeDeclaredObject proves every authorized route asks for
// its declared actions on the object its resolver returns, on the production
// table: the recording Authorizer sees each declared action on the resolved
// object, and denying that object answers 403. Public and upload routes never
// call the Authorizer: public routes need no credential and upload routes
// carry their capability in the path.
func TestRoutesAuthorizeDeclaredObject(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == revealPattern() {
			continue
		}

		t.Run(route.Method+" "+route.Path, func(t *testing.T) {
			t.Parallel()
			checkRouteObject(t, route)
		})
	}
}

// checkRouteObject proves one route authorizes its declared object.
func checkRouteObject(t *testing.T, route tfe.Route) {
	t.Helper()

	switch route.Scope {
	case tfe.ScopePublic:
		checkPublicRouteObject(t, route)

		return
	case tfe.ScopeUpload:
		checkUploadRouteObject(t, route)

		return
	case tfe.ScopeAuthorized, tfe.ScopeConsole:
		checkAuthorizedRouteObject(t, route)

		return
	case tfe.ScopeRegistry, tfe.ScopeSigned:
		// The production table of this test holds no registry route.
		t.Fatalf("route %q uses a registry scope", route.Path)
	default:
		t.Fatalf("route %q has unknown scope %v", route.Path, route.Scope)
	}
}

// checkPublicRouteObject proves a public route serves without sign-in and
// never calls the Authorizer.
func checkPublicRouteObject(t *testing.T, route tfe.Route) {
	t.Helper()

	recorder := &recordingAuthorizer{calls: nil}
	handler := newObjectHandler(t, recorder)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, routeRequest(t, route, ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
	}

	if len(recorder.calls) != 0 {
		t.Fatalf("public route calls %v, want no Authorizer call", recorder.calls)
	}
}

// checkUploadRouteObject proves an upload route serves its capability without
// calling the Authorizer.
func checkUploadRouteObject(t *testing.T, route tfe.Route) {
	t.Helper()

	recorder := &recordingAuthorizer{calls: nil}
	handler := newObjectHandler(t, recorder)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, routeRequest(t, route, ""))

	if rec.Code == http.StatusNotFound {
		t.Fatal("upload route is not served")
	}

	if len(recorder.calls) != 0 {
		t.Fatalf("upload route calls %v, want no Authorizer call", recorder.calls)
	}
}

// checkAuthorizedRouteObject proves an authorized route asks for its declared
// actions on its resolved object: the recorder sees each action on it, and
// denying it answers 403.
func checkAuthorizedRouteObject(t *testing.T, route tfe.Route) {
	t.Helper()

	expected := resolveExpected(t, route)

	recorder := &recordingAuthorizer{calls: nil}
	allowed := newObjectHandler(t, recorder)

	rec := httptest.NewRecorder()
	allowed.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	if want := wantSuccessStatus(route); rec.Code != want {
		t.Fatalf("allowed request answers %d, want %d (%s)", rec.Code, want, rec.Body.String())
	}

	for _, action := range route.Actions {
		if !slices.Contains(recorder.calls, recordedCall{action: action, object: expected}) {
			t.Fatalf("route never asks %s on %q (calls %v)", action, expected, recorder.calls)
		}
	}

	denied := newObjectHandler(t, denyObjectAuthorizer{deny: expected})

	rec = httptest.NewRecorder()
	denied.ServeHTTP(rec, routeRequest(t, route, "test-token"))

	// The Authorizer denies every action on the object, reads included, so
	// the route answers the 404 of an object that does not exist.
	if rec.Code != http.StatusNotFound {
		t.Fatalf("request denied on %q answers %d, want %d", expected, rec.Code, http.StatusNotFound)
	}
}

// resolveExpected returns the wire form of the object the route resolves.
// Path placeholders are filled as routeRequest fills them, since resolvers
// read path values that only routing sets.
func resolveExpected(t *testing.T, route tfe.Route) string {
	t.Helper()

	req := routeRequest(t, route, "test-token")
	req.SetPathValue("organization", "acme")
	req.SetPathValue("workspace", "web")
	req.SetPathValue("id", "ws-test123")
	req.SetPathValue("workspace_id", "ws-test123")
	req.SetPathValue("at", "1500")
	req.SetPathValue("state_version_id", "sv-test123")
	req.SetPathValue("output_id", "sv-test123.greeting")

	rec := httptest.NewRecorder()

	obj, err := route.Resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	return obj.String()
}

// resolverFor returns the resolver that a route declares.
func resolverFor(t *testing.T, handler *tfe.Handler, pattern string) tfe.ObjectResolver {
	t.Helper()

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			if route.Resolve == nil {
				t.Fatalf("route %q declares no resolver", pattern)
			}

			return route.Resolve
		}
	}

	t.Fatalf("route %q is not declared", pattern)

	return nil
}

// TestResolveWorkspaceByName proves the name route resolves the unit in the
// path.
func TestResolveWorkspaceByName(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-name/{organization}/{workspace}")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v2/test-by-name/acme/web", http.NoBody)
	req.SetPathValue("organization", "acme")
	req.SetPathValue("workspace", "web")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "opentofu:acme/web"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}
}

// TestResolveWorkspaceByNameInvalid proves invalid names fail.
func TestResolveWorkspaceByNameInvalid(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-name/{organization}/{workspace}")

	cases := []struct {
		name          string
		organization  string
		workspaceName string
		want          error
	}{
		{name: "invalid organization", organization: "../x", workspaceName: "web", want: namespace.ErrInvalidName},
		{name: "invalid workspace", organization: "acme", workspaceName: "../x", want: workspace.ErrInvalidName},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)
			req.SetPathValue("organization", tc.organization)
			req.SetPathValue("workspace", tc.workspaceName)

			rec := httptest.NewRecorder()
			if _, err := resolve(rec, req); !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestResolveWorkspaceByID proves the ID route resolves the ID to the unit
// name and restores the body for the handler.
func TestResolveWorkspaceByID(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-id/{id}")

	const body = `{"data":{"attributes":{"name":"web"}}}`

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-test123",
		strings.NewReader(body),
	)
	req.SetPathValue("id", "ws-test123")

	rec := httptest.NewRecorder()
	obj, err := resolve(rec, req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if want := "opentofu:acme/web"; obj.String() != want || !obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want unit %q", obj.String(), obj.IsUnit(), want)
	}

	if req.Body == nil {
		t.Fatal("resolver left a nil body")
	}

	var decoded resolveIDBody

	if err := json.NewDecoder(req.Body).Decode(&decoded); err != nil {
		t.Fatalf("handler read: %v", err)
	}

	if decoded.Data.Attributes.Name != "web" {
		t.Fatalf("restored body name = %q, want %q", decoded.Data.Attributes.Name, "web")
	}
}

// TestResolveWorkspaceByIDNotFound proves an unknown ID fails with the
// workspace error.
func TestResolveWorkspaceByIDNotFound(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)
	resolve := resolverFor(t, handler, "POST /api/v2/test-by-id/{id}")

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/test-by-id/ws-missing",
		strings.NewReader(`{}`),
	)
	req.SetPathValue("id", "ws-missing")

	rec := httptest.NewRecorder()
	if _, err := resolve(rec, req); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("error = %v, want %v", err, workspace.ErrNotFound)
	}
}

// TestPublicRouteServesWithoutToken proves the public test route answers 200
// with no sign-in.
func TestPublicRouteServesWithoutToken(t *testing.T) {
	t.Parallel()

	handler := newTestHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/test-public", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("public route answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}
}

// TestDiscoveryAdvertisesTFE proves the discovery document advertises tfe.v2
// and tfe.v2.1 at /api/v2/ and nothing else: there is no login.v1 service.
func TestDiscoveryAdvertisesTFE(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/.well-known/terraform.json",
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("discovery answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("media type = %q, want %q", got, "application/json")
	}

	var doc map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode discovery body: %v", err)
	}

	if len(doc) != 2 || doc["tfe.v2"] != "/api/v2/" || doc["tfe.v2.1"] != "/api/v2/" {
		t.Fatalf("discovery body = %v, want tfe.v2 and tfe.v2.1 at /api/v2/", doc)
	}
}

// TestPingSetsAPIVersion proves ping answers 200 with TFP-API-Version 2.5.
func TestPingSetsAPIVersion(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v2/ping", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ping answers %d, want %d", rec.Code, http.StatusOK)
	}

	if got := rec.Header().Get("Tfp-Api-Version"); got != "2.5" {
		t.Fatalf("TFP-API-Version = %q, want %q", got, "2.5")
	}
}

// entitlementTestAttributes is the attributes object of an entitlement
// document in tests.
type entitlementTestAttributes struct {
	Operations bool `json:"operations"`
}

// entitlementTestData is the data object of an entitlement document in tests.
type entitlementTestData struct {
	Type       string                    `json:"type"`
	Attributes entitlementTestAttributes `json:"attributes"`
}

// entitlementTestDoc decodes an entitlement document in tests.
type entitlementTestDoc struct {
	Data entitlementTestData `json:"data"`
}

// TestEntitlementSetReportsLocal proves the entitlement set reports
// operations false, so OpenTofu runs plans locally.
func TestEntitlementSetReportsLocal(t *testing.T) {
	t.Parallel()

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/acme/entitlement-set",
		http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	allowed.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("entitlement set answers %d, want %d", rec.Code, http.StatusOK)
	}

	var doc entitlementTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode entitlement body: %v", err)
	}

	if doc.Data.Type != "entitlement-sets" {
		t.Fatalf("entitlement type = %q, want %q", doc.Data.Type, "entitlement-sets")
	}

	if doc.Data.Attributes.Operations {
		t.Fatal("entitlement operations is true, want false")
	}
}

// TestEntitlementSetUnknownOrganization proves an unknown organization
// answers 404 as a JSON:API error.
func TestEntitlementSetUnknownOrganization(t *testing.T) {
	t.Parallel()

	allowed := newEnforcingHandler(t, selectiveAuthorizer{deny: map[identity.Action]bool{}})

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/ghost/entitlement-set",
		http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()

	allowed.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown organization answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// TestEntitlementSetDeclaresNamespaceRead proves the entitlement-set route
// declares namespace:read on the namespace object.
func TestEntitlementSetDeclaresNamespaceRead(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	const pattern = "GET /api/v2/organizations/{organization}/entitlement-set"

	var found *tfe.Route

	for _, route := range handler.Routes() {
		if route.Method+" "+route.Path == pattern {
			found = &route

			break
		}
	}

	if found == nil {
		t.Fatalf("route %q is not declared", pattern)
	}

	if len(found.Actions) != 1 || found.Actions[0] != identity.ActionNamespaceRead {
		t.Fatalf("route %q actions = %v, want [namespace:read]", pattern, found.Actions)
	}

	if found.Resolve == nil {
		t.Fatalf("route %q declares no resolver", pattern)
	}

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/organizations/acme/entitlement-set",
		http.NoBody,
	)
	req.SetPathValue("organization", "acme")

	obj, err := found.Resolve(httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if obj.String() != "acme" || obj.IsUnit() {
		t.Fatalf("object = %q (unit %v), want namespace %q", obj.String(), obj.IsUnit(), "acme")
	}
}

// TestUnknownPathAnswersJSONAPI404 proves a path the table does not hold
// answers 404 as a JSON:API error from this adapter.
func TestUnknownPathAnswersJSONAPI404(t *testing.T) {
	t.Parallel()

	handler := newProductionHandler(t)

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/api/v2/no-such-route",
		http.NoBody,
	)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path answers %d, want %d", rec.Code, http.StatusNotFound)
	}

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// SetOverrides is not used by this fake.
func (stubNamespaceStore) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}
