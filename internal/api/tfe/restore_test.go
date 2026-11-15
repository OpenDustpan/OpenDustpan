package tfe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// restoreRequest builds an authenticated restore request for version id.
func restoreRequest(t *testing.T, id string) *http.Request {
	t.Helper()

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/state-versions/"+id+"/actions/restore",
		strings.NewReader(`{}`),
	)
	req.Header.Set("Authorization", "Bearer test-token")

	return req
}

// restoreTestAttributes decodes the attributes of a restored version in
// tests.
type restoreTestAttributes struct {
	Serial       int64  `json:"serial"`
	Lineage      string `json:"lineage"`
	MD5          string `json:"md5"`
	Force        bool   `json:"force"`
	RestoredFrom string `json:"restored-from"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// restoreTestData decodes the data object of a restore response in tests.
type restoreTestData struct {
	ID         string                `json:"id"`
	Type       string                `json:"type"`
	Attributes restoreTestAttributes `json:"attributes"`
}

// restoreTestMeta decodes the meta object of a restore response in tests.
type restoreTestMeta struct {
	Message string `json:"message"`
}

// restoreTestDoc decodes a restore response in tests: the new state version
// and the message that no infrastructure changed.
type restoreTestDoc struct {
	Data restoreTestData `json:"data"`
	Meta restoreTestMeta `json:"meta"`
}

// decodeRestoreDoc decodes a restore response and fails the test when the
// status is wrong.
func decodeRestoreDoc(t *testing.T, rec *httptest.ResponseRecorder, want int) restoreTestDoc {
	t.Helper()

	if rec.Code != want {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, want, rec.Body.String())
	}

	if got := rec.Header().Get("Content-Type"); got != "application/vnd.api+json" {
		t.Fatalf("media type = %q, want %q", got, "application/vnd.api+json")
	}

	var doc restoreTestDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode restore body: %v", err)
	}

	return doc
}

// TestRestoreDeclaresUnitWrite proves the restore route declares unit:write
// and resolves the unit opentofu:acme/web.
func TestRestoreDeclaresUnitWrite(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	checkWorkspaceRoute(t, handler, workspaceRouteCase{
		name:    "state version restore",
		pattern: "POST /api/v2/state-versions/{state_version_id}/actions/restore",
		method:  http.MethodPost,
		target:  "/api/v2/state-versions/sv-test123/actions/restore",
		body:    `{}`,
		actions: []identity.Action{identity.ActionUnitWrite},
		object:  "opentofu:acme/web",
		isUnit:  true,
	})
}

// TestRestoreStateVersion proves a restore answers the new version with the
// lineage, serial and MD5 of the old one, the forced flag, the restored-from
// version and the message that no infrastructure changed.
func TestRestoreStateVersion(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-test123"))

	doc := decodeRestoreDoc(t, rec, http.StatusOK)

	if doc.Data.ID != "sv-restored123" || doc.Data.Type != "state-versions" {
		t.Fatalf("state version = %q %q, want sv-restored123 of type state-versions", doc.Data.ID, doc.Data.Type)
	}

	attrs := doc.Data.Attributes
	if attrs.Serial != 3 || attrs.Lineage != "seed-lineage" {
		t.Fatalf("attributes = %+v, want serial 3 of the seed lineage", attrs)
	}

	if !attrs.Force || attrs.RestoredFrom != "sv-test123" {
		t.Fatalf("attributes = %+v, want force true restored from sv-test123", attrs)
	}

	if doc.Meta.Message == "" {
		t.Fatal("restore response holds no message that no infrastructure changed")
	}

	if !strings.Contains(strings.ToLower(doc.Meta.Message), "no infrastructure") &&
		!strings.Contains(strings.ToLower(doc.Meta.Message), "changes no") {
		t.Fatalf("message = %q, want the words that no infrastructure changed", doc.Meta.Message)
	}
}

// TestRestoreUnknownVersion proves a restore of an unknown version answers
// 404.
func TestRestoreUnknownVersion(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-missing"))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// foreignRestoreWorkspaces serves sv-foreign of ws-other123, a workspace of
// its own, while Restore reports that the version belongs to another
// workspace. The resolver finds ws-other123, so the handler maps the
// service error to 404.
type foreignRestoreWorkspaces struct {
	stubWorkspaces
}

func (foreignRestoreWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	if id.String() != "sv-foreign" {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	sv := stubStateVersion()
	sv.ID = workspace.StateVersionID("sv-foreign")
	sv.Workspace = workspace.ID("ws-other123")

	return sv, nil
}

func (foreignRestoreWorkspaces) GetByID(
	ctx context.Context, id workspace.ID,
) (workspace.Workspace, error) {
	if id.String() != "ws-other123" {
		return stubWorkspaces{}.GetByID(ctx, id)
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName("other")
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "",
			Holder:   "",
			Reason:   "",
		},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns, Name: name},
		Revision:    1,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

func (foreignRestoreWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
}

// TestRestoreVersionOfAnotherWorkspace proves a restore whose version belongs
// to another workspace answers 404.
func TestRestoreVersionOfAnotherWorkspace(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     foreignRestoreWorkspaces{stubWorkspaces: stubWorkspaces{}},
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
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-foreign"))

	checkJSONAPIError(t, rec, http.StatusNotFound)
}

// lockedRestoreWorkspaces holds ws-test123 for alice: Restore answers
// ErrLockedByOther while GetByID reports the held lock, so the handler names
// the holder in the 409 detail.
type lockedRestoreWorkspaces struct {
	stubWorkspaces
}

func (lockedRestoreWorkspaces) GetByID(
	_ context.Context, id workspace.ID,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		return workspace.Workspace{}, err
	}

	name, err := workspace.ParseName("web")
	if err != nil {
		return workspace.Workspace{}, err
	}

	return workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   stubWorkspaceTime(),
		UpdatedAt:   stubWorkspaceTime(),
		Lock: workspace.Lock{
			LockedAt: stubWorkspaceTime(),
			ID:       "lock-alice",
			Holder:   "alice",
			Reason:   "apply",
		},
		LastRelease: workspace.NoRelease(),
		ID:          id,
		Ref:         workspace.Ref{Namespace: ns, Name: name},
		Revision:    2,
		Overrides: namespace.Overrides{
			Forced:   false,
			Unlocked: false,
		},
		Origin: identity.StateOrigin{Job: "", Commit: ""},
	}, nil
}

func (lockedRestoreWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, fmt.Errorf("%w: held by alice", workspace.ErrLockedByOther)
}

// unheldRestoreWorkspaces answers ErrLockedByOther while GetByID reports no
// held lock, so the handler has no holder to name in the 409 detail.
type unheldRestoreWorkspaces struct {
	stubWorkspaces
}

func (unheldRestoreWorkspaces) Restore(
	_ context.Context, _ workspace.ID, _ workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	return workspace.StateVersion{}, fmt.Errorf("%w: no holder known", workspace.ErrLockedByOther)
}

// TestRestoreLockedByOtherWithoutHolder proves a restore that the service
// refuses as locked by another principal, with no known holder, answers
// workspace.locked with no arguments.
func TestRestoreLockedByOtherWithoutHolder(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     unheldRestoreWorkspaces{stubWorkspaces: stubWorkspaces{}},
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
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-test123"))

	errcodetest.CheckAnswer(t, errcodetest.ShapeJSONAPI, http.StatusConflict, rec.Body.Bytes(),
		errcodetest.Want{Code: errcode.WorkspaceLocked, Args: nil, RequestBody: nil})
}

// TestRestoreLockedByOther proves a restore while another principal holds the
// lock answers 409 with a detail holding "is locked by User" and the holder.
func TestRestoreLockedByOther(t *testing.T) {
	t.Parallel()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     lockedRestoreWorkspaces{stubWorkspaces: stubWorkspaces{}},
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
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-test123"))

	if rec.Code != http.StatusConflict {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	var doc jsonAPIErrorDoc

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode error body: %v", err)
	}

	if len(doc.Errors) != 1 {
		t.Fatalf("error document holds %d errors, want 1", len(doc.Errors))
	}

	detail := doc.Errors[0].Detail
	if !strings.Contains(detail, "is locked by User") || !strings.Contains(detail, "alice") {
		t.Fatalf("restore detail = %q, want %q and the holder", detail, "is locked by User")
	}
}
