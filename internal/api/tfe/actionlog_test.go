package tfe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// actionLogEntry decodes one structured action log line in tests.
type actionLogEntry struct {
	Level     string `json:"level"`
	Message   string `json:"msg"`
	Action    string `json:"action"`
	Object    string `json:"object"`
	Principal string `json:"principal"`
	Released  string `json:"released"`
}

// loggedTFEHandler returns a production Handler over stores whose info log
// goes to buf.
func loggedTFEHandler(t *testing.T, stores tfe.WorkspaceService, buf *bytes.Buffer) *tfe.Handler {
	t.Helper()

	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     allowAll(),
		AddressGate:    nil,
		Logger:         logger,
		Namespaces:     newTestNamespaces(t),
		Workspaces:     stores,
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

	return handler
}

// decodeActionLog proves buf holds exactly one log line and returns it.
func decodeActionLog(t *testing.T, buf *bytes.Buffer) actionLogEntry {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("log holds %d lines, want 1: %q", len(lines), buf.String())
	}

	var entry actionLogEntry

	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("decode log line: %v (%q)", err, lines[0])
	}

	return entry
}

// wantActionLog holds the wanted fields of one action log line.
type wantActionLog struct {
	action    string
	object    string
	principal string
	released  string
}

// checkActionLog proves the entry holds the wanted fields at info level.
func checkActionLog(t *testing.T, entry actionLogEntry, want wantActionLog) {
	t.Helper()

	if entry.Level != slog.LevelInfo.String() {
		t.Fatalf("log level = %q, want %q", entry.Level, slog.LevelInfo.String())
	}

	if entry.Action != want.action || entry.Object != want.object ||
		entry.Principal != want.principal || entry.Released != want.released {
		t.Fatalf(
			"log entry = %+v, want action %q object %q principal %q released %q",
			entry,
			want.action,
			want.object,
			want.principal,
			want.released,
		)
	}
}

// lockedActionWorkspaces holds ws-test123 locked by alice: ForceUnlock frees
// it, so the force-unlock log test proves the released holder.
type lockedActionWorkspaces struct {
	stubWorkspaces
}

func (lockedActionWorkspaces) GetByID(_ context.Context, id workspace.ID) (workspace.Workspace, error) {
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

//nolint:revive // The signature of the workspace port that the handler reads.
func (rlockedActionWorkspaces lockedActionWorkspaces) ForceUnlockLock(
	ctx context.Context,
	id workspace.ID,
	_ workspace.Lock,
	by string,
	_ namespace.Overrides, _ func() error, _ audit.Entry,
) (workspace.Workspace, bool, error) {
	w, err := rlockedActionWorkspaces.forceUnlockUnfenced(ctx, id, by)

	return w, false, err
}

func (lockedActionWorkspaces) forceUnlockUnfenced(
	_ context.Context,
	id workspace.ID,
	_ string,
) (workspace.Workspace, error) {
	if id.String() != "ws-test123" {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	ws, err := stubWorkspaceByID(id)
	if err != nil {
		return workspace.Workspace{}, err
	}

	return ws, nil
}

// TestForceUnlockLogsAction proves a force-unlock writes one info line with
// the action, the unit, the principal and the released holder.
func TestForceUnlockLogsAction(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	handler := loggedTFEHandler(t, lockedActionWorkspaces{stubWorkspaces: stubWorkspaces{}}, &buf)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/v2/workspaces/ws-test123/actions/force-unlock", http.NoBody,
	)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	checkActionLog(t, decodeActionLog(t, &buf), wantActionLog{
		action:    "force-unlock",
		object:    "opentofu:acme/web",
		principal: "tester",
		released:  "alice",
	})
}

// TestForceUnlockLogsARepeat proves a force-unlock on a unit with no lock
// writes a repeat line without a released holder, and a release of a held
// lock keeps the released holder.
func TestForceUnlockLogsARepeat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		store        tfe.WorkspaceService
		wantMessage  string
		wantReleased string
	}{
		{
			name:         "release of a held lock",
			store:        lockedActionWorkspaces{stubWorkspaces: stubWorkspaces{}},
			wantMessage:  "force-unlock completed",
			wantReleased: "alice",
		},
		{
			name:         "repeat with no lock",
			store:        stubWorkspaces{},
			wantMessage:  "force-unlock repeated with no change",
			wantReleased: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			handler := loggedTFEHandler(t, tc.store, &buf)

			req := httptest.NewRequestWithContext(
				t.Context(), http.MethodPost, "/api/v2/workspaces/ws-test123/actions/force-unlock", http.NoBody,
			)
			req.Header.Set("Authorization", "Bearer test-token")

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
			}

			entry := decodeActionLog(t, &buf)

			checkActionLog(t, entry, wantActionLog{
				action:    "force-unlock",
				object:    "opentofu:acme/web",
				principal: "tester",
				released:  tc.wantReleased,
			})

			if entry.Message != tc.wantMessage {
				t.Fatalf("log message = %q, want %q", entry.Message, tc.wantMessage)
			}

			if tc.wantReleased == "" && strings.Contains(buf.String(), `"released"`) {
				t.Fatalf("repeat log line holds a released attribute: %q", buf.String())
			}
		})
	}
}

// TestTFERestoreLogsAction proves a restore writes one info line with the
// action, the unit and the principal and an empty released value.
func TestTFERestoreLogsAction(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	handler := loggedTFEHandler(t, stubWorkspaces{}, &buf)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, restoreRequest(t, "sv-test123"))

	if rec.Code != http.StatusOK {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	checkActionLog(t, decodeActionLog(t, &buf), wantActionLog{
		action:    "restore",
		object:    "opentofu:acme/web",
		principal: "tester",
		released:  "",
	})
}

// canaryRestoreWorkspaces serves any state version ID as the finalized state
// of ws-test123, so the canary test proves the version ID in the path never
// reaches the log.
type canaryRestoreWorkspaces struct {
	stubWorkspaces
}

func (canaryRestoreWorkspaces) ReadState(
	_ context.Context, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	sv := stubStateVersion()
	sv.ID = id

	return sv, nil
}

func (canaryRestoreWorkspaces) Restore(
	_ context.Context, id workspace.ID, from workspace.StateVersionID, _ string,
) (workspace.StateVersion, error) {
	if id.String() != "ws-test123" {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	restored := stubStateVersion()
	restored.ID = workspace.StateVersionID("sv-restored123")
	restored.Force = true
	restored.RestoredFrom = from

	return restored, nil
}

// TestActionLogHidesPathAndQuery proves the action log never holds the
// request path or query: a canary in the version ID and in the query never
// appears in the output.
func TestActionLogHidesPathAndQuery(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	handler := loggedTFEHandler(t, canaryRestoreWorkspaces{stubWorkspaces: stubWorkspaces{}}, &buf)

	const pathCanary = "sv-canary-path-8f3a2b1c9d"
	const queryCanary = "canary-query-4d2e6f1a9b"

	req := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		"/api/v2/state-versions/"+pathCanary+"/actions/restore?probe="+queryCanary,
		strings.NewReader(`{}`),
	)
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("request answers %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	entry := decodeActionLog(t, &buf)
	checkActionLog(t, entry, wantActionLog{
		action:    "restore",
		object:    "opentofu:acme/web",
		principal: "tester",
		released:  "",
	})

	for _, canary := range []string{pathCanary, queryCanary} {
		if strings.Contains(buf.String(), canary) {
			t.Fatalf("log holds canary value %q", canary)
		}
	}
}

// TestActionLogDeclaresUnitActions proves the logged routes keep their
// declared actions: force-unlock needs unit:write-forced and restore needs
// unit:write.
func TestActionLogDeclaresUnitActions(t *testing.T) {
	t.Parallel()

	handler := newStateHandler(t, allowAll())

	want := map[string]identity.Action{
		"POST /api/v2/workspaces/{id}/actions/force-unlock":              identity.ActionUnitWriteForced,
		"POST /api/v2/state-versions/{state_version_id}/actions/restore": identity.ActionUnitWrite,
	}

	seen := map[string]bool{}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path

		action, ok := want[pattern]
		if !ok {
			continue
		}

		seen[pattern] = true

		if len(route.Actions) != 1 || route.Actions[0] != action {
			t.Errorf("route %q actions = %v, want exactly [%s]", pattern, route.Actions, action)
		}
	}

	for pattern := range want {
		if !seen[pattern] {
			t.Errorf("route %q is not declared", pattern)
		}
	}
}
