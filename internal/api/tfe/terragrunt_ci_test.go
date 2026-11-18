package tfe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// ciStateTarget is the state version list that a CI unit reads for the
// workspace acme/web.
const ciStateTarget = "/api/v2/state-versions?filter[organization][name]=acme&filter[workspace][name]=web"

// ciOnlyAuthorizer allows every action on the one object wire form in allow
// and denies every other object: an access token granted on one workspace.
type ciOnlyAuthorizer struct {
	allow string
}

func (c ciOnlyAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if obj.String() == c.allow {
		return nil
	}

	return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
}

// ListObjects gives an administrator every object and any other principal none.
func (ciOnlyAuthorizer) ListObjects(
	_ context.Context, p identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// ciStateProbe wraps a workspace service and records each state read, each
// state write and each lock request that reaches the port, so a test proves
// what a refused request did not touch.
type ciStateProbe struct {
	tfe.WorkspaceService

	mu     *sync.Mutex
	reads  *[]string
	writes *[]string
	locks  *[]string
}

// newCiStateProbe returns a probe over inner with empty records.
func newCiStateProbe(inner tfe.WorkspaceService) ciStateProbe {
	return ciStateProbe{
		WorkspaceService: inner,
		mu:               &sync.Mutex{},
		reads:            &[]string{},
		writes:           &[]string{},
		locks:            &[]string{},
	}
}

func (p ciStateProbe) Current(ctx context.Context, id workspace.ID) (workspace.StateVersion, error) {
	p.record(p.reads, "current "+id.String())

	return p.WorkspaceService.Current(ctx, id)
}

func (p ciStateProbe) ReadState(ctx context.Context, id workspace.StateVersionID) (workspace.StateVersion, error) {
	p.record(p.reads, "read "+id.String())

	return p.WorkspaceService.ReadState(ctx, id)
}

func (p ciStateProbe) Open(ctx context.Context, sv workspace.StateVersion) (io.ReadCloser, error) {
	p.record(p.reads, "open "+sv.ID.String())

	return p.WorkspaceService.Open(ctx, sv)
}

func (p ciStateProbe) History(
	ctx context.Context, id workspace.ID, page workspace.Page,
) ([]workspace.StateVersion, int, error) {
	p.record(p.reads, "history "+id.String())

	return p.WorkspaceService.History(ctx, id, page)
}

func (p ciStateProbe) LockForJob(
	ctx context.Context, id workspace.ID, holder, reason, jobID string,
) (workspace.Workspace, error) {
	p.record(p.locks, id.String())

	return p.WorkspaceService.LockForJob(ctx, id, holder, reason, jobID)
}

func (p ciStateProbe) BeginState(
	ctx context.Context, id workspace.ID, holder string, in workspace.NewState, auth workspace.Authority,
) (workspace.PendingState, error) {
	p.record(p.writes, "begin "+id.String())

	return p.WorkspaceService.BeginState(ctx, id, holder, in, auth)
}

func (p ciStateProbe) Upload(
	ctx context.Context, id workspace.StateVersionID, tok token.Token, content io.Reader,
) (workspace.StateVersion, error) {
	p.record(p.writes, "upload "+id.String())

	return p.WorkspaceService.Upload(ctx, id, tok, content)
}

func (p ciStateProbe) Restore(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, holder string,
) (workspace.StateVersion, error) {
	p.record(p.writes, "restore "+id.String())

	return p.WorkspaceService.Restore(ctx, id, from, holder)
}

func (p ciStateProbe) RestoreExpecting(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, holder string,
	expectCurrent workspace.StateVersionID,
) (workspace.StateVersion, error) {
	p.record(p.writes, "restore "+id.String())

	return p.WorkspaceService.RestoreExpecting(ctx, id, from, holder, expectCurrent)
}

func (p ciStateProbe) record(list *[]string, item string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	*list = append(*list, item)
}

func (p ciStateProbe) snapshot(list *[]string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(*list)
}

// checkNoWrite fails the test when a request wrote state.
func (p ciStateProbe) checkNoWrite(t *testing.T) {
	t.Helper()

	if writes := p.snapshot(p.writes); len(writes) != 0 {
		t.Fatalf("the request wrote state: %q", writes)
	}
}

// checkNoAccess fails the test when a refused request read or wrote state.
func (p ciStateProbe) checkNoAccess(t *testing.T) {
	t.Helper()

	p.checkNoWrite(t)

	if reads := p.snapshot(p.reads); len(reads) != 0 {
		t.Fatalf("the refused request read state: %q", reads)
	}
}

// newCiHandler returns a production Handler over workspaces with authz as
// the authorizer.
func newCiHandler(t *testing.T, authz identity.Authorizer, workspaces tfe.WorkspaceService) *tfe.Handler {
	t.Helper()

	handler, err := tfe.NewHandler(tfe.Config{
		Registry:       nil,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		AddressGate:    nil,
		Logger:         slog.New(slog.DiscardHandler),
		Namespaces:     newTestNamespaces(t),
		Workspaces:     workspaces,
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

// ciStateList is the state version list document that a CI unit reads.
type ciStateList struct {
	Data []stateTestResource `json:"data"`
}

// ciUnitRun sends the requests of one CI unit that names acme/web: it reads
// the workspace by name, locks it by its ID and lists its state versions. It
// returns the workspace ID and the lineage of each listed state version.
func ciUnitRun(t *testing.T, handler *tfe.Handler) (string, []string) {
	t.Helper()

	out := httptest.NewRecorder()
	handler.ServeHTTP(out, workspaceRequest(t, http.MethodGet, "/api/v2/organizations/acme/workspaces/web", ""))

	if out.Code != http.StatusOK {
		t.Fatalf("workspace read answers %d, want 200: %s", out.Code, out.Body.String())
	}

	var ws workspaceTestDoc
	if err := json.Unmarshal(out.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode workspace body: %v", err)
	}

	out = httptest.NewRecorder()
	handler.ServeHTTP(out, workspaceRequest(t, http.MethodPost,
		"/api/v2/workspaces/"+ws.Data.ID+"/actions/lock", `{"data":{}}`))

	if out.Code != http.StatusOK {
		t.Fatalf("lock answers %d, want 200: %s", out.Code, out.Body.String())
	}

	out = httptest.NewRecorder()
	handler.ServeHTTP(out, stateRequest(t, ciStateTarget))

	if out.Code != http.StatusOK {
		t.Fatalf("state version list answers %d, want 200: %s", out.Code, out.Body.String())
	}

	var list ciStateList
	if err := json.Unmarshal(out.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode state version list: %v", err)
	}

	lineages := make([]string, 0, len(list.Data))
	for i := range list.Data {
		lineages = append(lineages, list.Data[i].Attributes.Lineage)
	}

	return ws.Data.ID, lineages
}

// TestTerragruntCiSameWorkspaceAddress proves that two CI units that name one
// workspace reach the same object: both resolve to one workspace ID, so both
// lock requests name one lock, and both read one state lineage.
func TestTerragruntCiSameWorkspaceAddress(t *testing.T) {
	t.Parallel()

	rec := &recordingAuthorizer{calls: nil}
	probe := newCiStateProbe(stubWorkspaces{})
	handler := newCiHandler(t, rec, probe)

	ids := make([]string, 0, 2)
	lineages := make([]string, 0, 2)

	for range 2 {
		id, unitLineages := ciUnitRun(t, handler)
		ids = append(ids, id)
		lineages = append(lineages, unitLineages...)
	}

	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("the two units resolve to workspace IDs %q, want one ID", ids)
	}

	if locks := probe.snapshot(probe.locks); len(locks) != 2 || locks[0] != ids[0] || locks[1] != ids[0] {
		t.Fatalf("lock requests name %q, want two requests on %s", locks, ids[0])
	}

	if len(lineages) < 2 || slices.ContainsFunc(lineages, func(l string) bool { return l != lineages[0] }) {
		t.Fatalf("the two units read lineages %q, want one lineage", lineages)
	}

	for _, call := range rec.calls {
		if call.object != "opentofu:acme/web" {
			t.Fatalf("authorized object = %q, want opentofu:acme/web", call.object)
		}
	}
}

// TestTerragruntCiLockRefusal proves that a lock request on a workspace that
// another holder has locked is refused with 409 and names the holder, and that
// the refused request writes no state. The workspace port is a stub whose
// current state is constant, so this test does not show the effect of a
// refused lock on a real state lineage.
func TestTerragruntCiLockRefusal(t *testing.T) {
	t.Parallel()

	probe := newCiStateProbe(conflictWorkspaces{stubWorkspaces: stubWorkspaces{}})
	handler := newConflictHandlerOver(t, probe)

	before, err := probe.Current(t.Context(), workspace.ID("ws-test123"))
	if err != nil {
		t.Fatalf("current state before the refusal: %v", err)
	}

	detail := lockErrorDetail(t, handler, "/api/v2/workspaces/ws-test123/actions/lock")

	if !strings.Contains(detail, "is locked by User") || !strings.Contains(detail, "alice") {
		t.Fatalf("lock detail = %q, want the holder of the lock", detail)
	}

	if writes := probe.snapshot(probe.writes); len(writes) != 0 {
		t.Fatalf("the refused lock wrote state: %q", writes)
	}

	after, err := probe.Current(t.Context(), workspace.ID("ws-test123"))
	if err != nil {
		t.Fatalf("current state after the refusal: %v", err)
	}

	if after.ID != before.ID || after.Lineage != before.Lineage || after.Serial != before.Serial {
		t.Fatalf("current state after the refusal = %s %s/%d, want %s %s/%d",
			after.ID, after.Lineage, after.Serial, before.ID, before.Lineage, before.Serial)
	}
}

// TestTerragruntCiCredentialRefusal proves that a CI job with a missing or not
// valid access token is refused with 401, and that a valid token with no
// grant on the workspace is refused. It also proves that a token granted on
// workspace A is denied on workspace B. A refused request reads and writes
// no state.
func TestTerragruntCiCredentialRefusal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		authz   identity.Authorizer
		bearer  string
		want    []int
		refused bool
	}{
		{"no token", allowAll(), "", []int{http.StatusUnauthorized}, true},
		{"unknown token", allowAll(), "Bearer other-token", []int{http.StatusUnauthorized}, true},
		{
			"token with no grant",
			stubAuthorizer{},
			"Bearer test-token",
			[]int{http.StatusForbidden, http.StatusNotFound},
			true,
		},
		{
			"token granted on workspace A, read on workspace B",
			ciOnlyAuthorizer{allow: "opentofu:acme/api"},
			"Bearer test-token",
			[]int{http.StatusForbidden, http.StatusNotFound},
			true,
		},
		{
			"token granted on workspace B",
			ciOnlyAuthorizer{allow: "opentofu:acme/web"},
			"Bearer test-token",
			[]int{http.StatusOK},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			probe := newCiStateProbe(stubWorkspaces{})
			handler := newCiHandler(t, tc.authz, probe)
			req := stateRequest(t, ciStateTarget)

			if tc.bearer == "" {
				req.Header.Del("Authorization")
			} else {
				req.Header.Set("Authorization", tc.bearer)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if !slices.Contains(tc.want, rec.Code) {
				t.Fatalf("request answers %d, want one of %v: %s", rec.Code, tc.want, rec.Body.String())
			}

			if tc.refused {
				probe.checkNoAccess(t)

				return
			}

			// The granted case proves that the probe sees the state read, so
			// an empty record on a refused case means that no read reached
			// the port.
			probe.checkNoWrite(t)

			if len(probe.snapshot(probe.reads)) == 0 {
				t.Fatal("the granted request read no state, want the state version list")
			}
		})
	}
}
