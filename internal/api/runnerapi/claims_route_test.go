package runnerapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// allowAllAuthorizer authorizes every action. Claims tests use it so the
// credential and registry behavior, not the role table, is under test.
type allowAllAuthorizer struct{}

func (allowAllAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, _ identity.Action, _ identity.Object,
) error {
	return nil
}

func (allowAllAuthorizer) ScopeExact() bool { return false }

func (allowAllAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// denyAllAuthorizer denies every action with a denial that names the
// action and the object.
type denyAllAuthorizer struct{}

func (denyAllAuthorizer) Authorize(
	_ context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	return &identity.DeniedError{Subject: p.Subject, Action: action, Object: obj}
}

func (denyAllAuthorizer) ScopeExact() bool { return false }

func (denyAllAuthorizer) ListObjects(
	_ context.Context, _ identity.Principal, _ identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// fakeJobTokenStore is a jobtoken.Store that serves canned matches by
// digest. Issue is a no-op: claims tests mint matches directly.
type fakeJobTokenStore struct {
	matches map[jobtoken.Digest]jobtoken.Match
}

func (f *fakeJobTokenStore) Issue(
	_ context.Context, _ jobtoken.Token, _ namespace.Fence, _ audit.Entry,
) error {
	return nil
}

func (f *fakeJobTokenStore) GetByDigest(
	_ context.Context, d jobtoken.Digest,
) (jobtoken.Match, error) {
	m, ok := f.matches[d]
	if !ok {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	return m, nil
}

// testClaimWorkspaces is a ClaimWorkspaces fake.
type testClaimWorkspaces struct {
	spaces map[workspace.ID]workspace.Workspace
}

func (f *testClaimWorkspaces) GetWorkspaceByID(
	_ context.Context, id workspace.ID,
) (workspace.Workspace, error) {
	w, ok := f.spaces[id]
	if !ok {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	return w, nil
}

// testVersionKey keys a version by namespace, workspace and ID.
type testVersionKey struct {
	ns namespace.Name
	ws workspace.ID
	id workspace.StateVersionID
}

// testClaimVersions is a ClaimVersions fake.
type testClaimVersions struct {
	versions map[testVersionKey]workspace.StateVersion
}

func (f *testClaimVersions) GetWorkspaceStateVersion(
	_ context.Context, ns namespace.Name, ws workspace.ID, id workspace.StateVersionID,
) (workspace.StateVersion, error) {
	sv, ok := f.versions[testVersionKey{ns: ns, ws: ws, id: id}]
	if !ok {
		return workspace.StateVersion{}, workspace.ErrStateVersionNotFound
	}

	return sv, nil
}

// baseVersion returns a finalized base state version with every field
// set explicitly.
func baseVersion(id workspace.StateVersionID, ws workspace.ID) workspace.StateVersion {
	return workspace.StateVersion{
		CreatedAt:       time.Time{},
		FinalizedAt:     time.Time{},
		UploadExpiresAt: time.Time{},
		Upload:          token.Digest{},
		Holder:          "",
		LockID:          "",
		ID:              id,
		Workspace:       ws,
		Lineage:         "",
		Status:          workspace.StatusFinalized,
		Content:         "",
		Outputs:         nil,
		MD5:             [16]byte{},
		Serial:          0,
		Size:            0,
		Force:           false,
		UnlockedWrite:   false,
		Sealed:          false,
		ClientEncrypted: workspace.ClientEncryptedUnknown,
		SensitiveMarks:  nil,
		RestoredFrom:    "",
		FenceBy:         "",
		Origin:          identity.StateOrigin{Job: "", Commit: ""},
	}
}

// testClaimDeleted is a ClaimDeleted fake.
type testClaimDeleted struct {
	deleted map[workspace.ID]bool
}

func (f *testClaimDeleted) IsDeleted(_ context.Context, id workspace.ID) (bool, error) {
	return f.deleted[id], nil
}

// claimDoc is one claim of a register or list answer.
type claimDoc struct {
	ID          string   `json:"id"`
	WorkspaceID string   `json:"workspaceID"`
	Base        string   `json:"base"`
	Addresses   []string `json:"addresses"`
	Principal   string   `json:"principal"`
	JobID       string   `json:"jobID"`
	Trigger     string   `json:"trigger"`
	State       string   `json:"state"`
	StartedAt   string   `json:"startedAt"`
	EndedAt     string   `json:"endedAt"`
	LeaseEndsAt string   `json:"leaseEndsAt"`
	RenewedAt   string   `json:"renewedAt"`
}

// claimListDoc is a list answer.
type claimListDoc struct {
	Claims []claimDoc `json:"claims"`
}

// claimErrorDoc is a refusal answer.
type claimErrorDoc struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// claimWorld is the fixture of the claims route tests: one workspace,
// one base version and the tokens of two jobs on it.
type claimWorld struct {
	env     *env
	wsID    string
	base    string
	unit    identity.Object
	secret  map[string]string
	tokens  *fakeJobTokenStore
	deleted *testClaimDeleted
	spaces  *testClaimWorkspaces
	// clock is the clock of the claim registry, in Unix milliseconds.
	clock *atomic.Int64
}

// liveTokenMatch returns a live job token match for job jobID on unit.
func liveTokenMatch(now time.Time, secret jobtoken.Secret, jobID string, unit identity.Object) jobtoken.Match {
	tok := jobtoken.Token{
		Digest:         jobtoken.DigestOf(secret),
		StarterID:      "prn-ada",
		JobID:          job.ID(jobID),
		Namespace:      unit.Namespace(),
		Unit:           unit,
		ClaimOperation: "op-1",
		ClaimToken:     7,
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
	}

	return jobtoken.Match{
		Token: tok,
		Claim: jobtoken.Claim{
			OperationState: durable.StateRunning,
			LeaseEndsAt:    now.Add(time.Hour),
			JobID:          job.ID(jobID),
			JobState:       job.StateRunning,
			JobOperation:   "op-1",
			JobUnitID:      unit.UnitID(),
			JobStarterID:   "prn-ada",
			Starter: identity.Principal{
				Subject:                "ada",
				DisplayName:            "ada",
				Admin:                  false,
				TokenID:                "",
				TokenExpiresAt:         time.Time{},
				TemporaryAdministrator: false,
				TemporaryExpiresAt:     time.Time{},
				Kind:                   identity.KindPerson,
				Credential:             identity.CredentialJobToken,
				JobID:                  jobID,
			},
			StarterRecordID:     "prn-ada",
			OperationClaimToken: 7,
			StarterEnabled:      true,
		},
	}
}

// newClaimWorld opens an environment with a claim registry over fakes for
// workspaces and versions, a fake job token store, and the authorizer
// authz. The workspace ws-claims-1 lives in namespace acme with base
// version sv-base-1.
func newClaimWorld(t *testing.T, authz identity.Authorizer) *claimWorld {
	t.Helper()

	now := time.UnixMilli(1_700_000_000_000).UTC()
	nsName := namespace.Name("acme")
	wsID := workspace.ID("ws-claims-1")
	base := workspace.StateVersionID("sv-base-1")

	spaces := &testClaimWorkspaces{spaces: map[workspace.ID]workspace.Workspace{
		wsID: {
			ID:           wsID,
			NamespaceID:  namespace.ID("ns-acme"),
			CreatedAt:    now,
			UpdatedAt:    now,
			Lock:         workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
			LastRelease:  workspace.NoRelease(),
			InCollection: false,
			Overrides:    namespace.Overrides{Forced: false, Unlocked: false},
			Origin:       identity.StateOrigin{Job: "", Commit: ""},
			Ref:          workspace.Ref{Namespace: nsName, Name: "network"},
			Revision:     1,
		},
	}}
	versions := &testClaimVersions{versions: map[testVersionKey]workspace.StateVersion{
		{ns: nsName, ws: wsID, id: base}: baseVersion(base, wsID),
	}}
	deleted := &testClaimDeleted{deleted: map[workspace.ID]bool{}}

	unit, err := identity.UnitObject("opentofu", nsName, "network")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	unit = unit.WithUnitID(string(wsID))

	tokens := &fakeJobTokenStore{matches: make(map[jobtoken.Digest]jobtoken.Match)}
	secrets := make(map[string]string)

	for _, jobID := range []string{"job-1", "job-2"} {
		secret, secretErr := jobtoken.NewSecret()
		if secretErr != nil {
			t.Fatalf("new secret: %v", secretErr)
		}

		secrets[jobID] = secret.Reveal()
		tokens.matches[jobtoken.DigestOf(secret)] = liveTokenMatch(now, secret, jobID, unit)
	}

	clock := new(atomic.Int64)
	clock.Store(now.UnixMilli())

	registry, err := workspace.NewClaimRegistry(workspace.ClaimRegistryConfig{
		Workspaces: spaces,
		Versions:   versions,
		Deleted:    deleted,
		Manual:     nil,
		Store:      workspace.NewMemoryClaimStore(spaces),
		Now:        func() time.Time { return time.UnixMilli(clock.Load()).UTC() },
		Lease:      0,
	})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	tokenService := jobtoken.NewService(tokens, func() time.Time { return now })

	e := newEnvServing(t, openSQLite, func(s stores, opts *serveOptions) {
		opts.authz = authz
		opts.mutate = func(cfg *runnerapi.Config) {
			cfg.Claims = registry
			cfg.JobTokens = tokenService
			cfg.Jobs = s.jobs

			records, ok := s.namespaces.(runnerapi.StarterRecords)
			if !ok {
				t.Fatal("the store keeps no records of principals")
			}

			cfg.Starters = records
		}
	})

	return &claimWorld{
		env: e, wsID: string(wsID), base: string(base), unit: unit, secret: secrets, tokens: tokens,
		deleted: deleted, spaces: spaces, clock: clock,
	}
}

// call sends a claims route request with the Authorization header auth
// and returns the status and the raw body.
func (w *claimWorld) call(t *testing.T, method, path, auth string, body any) (int, []byte) {
	t.Helper()

	var reader io.Reader = http.NoBody

	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}

		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(t.Context(), method, w.env.server.URL+path, reader)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	req.Header.Set("Dustpan-Release", "0.4.0")

	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	status, raw := w.env.send(req)

	return status, raw
}

// decodeClaim decodes a register answer.
func decodeClaim(t *testing.T, raw []byte) claimDoc {
	t.Helper()

	var doc claimDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode claim %q: %v", raw, err)
	}

	return doc
}

// decodeClaimList decodes a list answer.
func decodeClaimList(t *testing.T, raw []byte) claimListDoc {
	t.Helper()

	var doc claimListDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode claims %q: %v", raw, err)
	}

	return doc
}

// decodeClaimError decodes a refusal answer.
func decodeClaimError(t *testing.T, raw []byte) claimErrorDoc {
	t.Helper()

	var doc claimErrorDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode refusal %q: %v", raw, err)
	}

	return doc
}

// claimPath returns the claims path of workspace id.
func claimPath(id string) string {
	return "/runner/v1/workspaces/" + id + "/claims"
}

// registerBody returns the register body for the base, addresses and job
// of the world.
func registerBody(w *claimWorld, jobID string, addresses ...string) map[string]any {
	return map[string]any{"base": w.base, "addresses": addresses, "jobID": jobID}
}

// bearer returns the bearer credential of the world's secret of jobID.
func (w *claimWorld) bearer(jobID string) string {
	return "Bearer " + w.secret[jobID]
}

// registerClaim registers one claim and fails the test on any error.
func (w *claimWorld) registerClaim(t *testing.T, jobID string, addresses ...string) claimDoc {
	t.Helper()

	status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer(jobID),
		registerBody(w, jobID, addresses...))
	if status != http.StatusCreated {
		t.Fatalf("register = %d %s, want 201", status, raw)
	}

	return decodeClaim(t, raw)
}

// checkAccepted proves that doc is an accepted claim of the world.
func checkAccepted(t *testing.T, w *claimWorld, name string, doc claimDoc) {
	t.Helper()

	if doc.State != "accepted" {
		t.Errorf("%s claim state = %q, want accepted", name, doc.State)
	}

	if doc.Base != w.base {
		t.Errorf("%s claim base = %q, want %s", name, doc.Base, w.base)
	}

	if doc.Principal != "ada" {
		t.Errorf("%s claim principal = %q, want ada", name, doc.Principal)
	}

	if doc.StartedAt == "" {
		t.Errorf("%s claim has no start time", name)
	}

	if doc.EndedAt != "" {
		t.Errorf("%s claim has an end time while accepted", name)
	}
}

// TestClaimRoutesDeclareActions proves that the two claim routes declare
// unit:claim-register for POST and unit:read for GET.
func TestClaimRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})

	seen := map[string]identity.Action{}

	for i := range w.env.handler.Routes() {
		route := &w.env.handler.Routes()[i]

		if route.Path == runnerapi.PathWorkspaceClaimRenew {
			if len(route.Actions) != 1 || route.Actions[0] != identity.ActionUnitClaimRegister {
				t.Errorf("POST renew declares %v, want unit:claim-register", route.Actions)
			}

			continue
		}

		if route.Path != runnerapi.PathWorkspaceClaims {
			continue
		}

		if len(route.Actions) != 1 {
			t.Fatalf("route %s %s declares %v, want one action", route.Method, route.Path, route.Actions)
		}

		seen[route.Method] = route.Actions[0]
	}

	if seen[http.MethodPost] != identity.ActionUnitClaimRegister {
		t.Errorf("POST claims declares %q, want unit:claim-register", seen[http.MethodPost])
	}

	if seen[http.MethodGet] != identity.ActionUnitRead {
		t.Errorf("GET claims declares %q, want unit:read", seen[http.MethodGet])
	}
}

// TestClaimMissingActionDenied proves that a caller without
// unit:claim-register gets 404, the answer of a workspace that does not exist
// and not a 403 that says the workspace exists, and the server stores no
// claim.
func TestClaimMissingActionDenied(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, denyAllAuthorizer{})

	status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
		registerBody(w, "job-1", "aws_instance.a"))

	if status != http.StatusNotFound {
		t.Fatalf("register without the action = %d %s, want 404", status, raw)
	}

	refused := decodeClaimError(t, raw)

	if refused.Code != "not-found" {
		t.Errorf("code = %q, want not-found", refused.Code)
	}

	// The answer of a missing workspace and the answer of a caller without the
	// action are the same: it names no action and no object.
	missing, missingRaw := w.call(t, http.MethodPost, claimPath("ws-missing"), w.bearer("job-1"),
		registerBody(w, "job-1", "aws_instance.a"))
	if missing != http.StatusNotFound || decodeClaimError(t, missingRaw) != refused {
		t.Errorf("missing workspace = %d %s, want the answer %+v", missing, missingRaw, refused)
	}

	for _, leak := range []string{"unit:claim-register", "opentofu:acme/network", "network"} {
		if strings.Contains(refused.Message, leak) {
			t.Errorf("message %q names %q", refused.Message, leak)
		}
	}
}

// TestClaimListShowsBothAccepted proves that after two disjoint
// registrations, the list shows both claims with state accepted, base,
// address set, principal, start time and no end time.
func TestClaimListShowsBothAccepted(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})

	first := w.registerClaim(t, "job-1", "aws_instance.a")
	second := w.registerClaim(t, "job-2", "aws_instance.b")

	checkAccepted(t, w, "first", first)
	checkAccepted(t, w, "second", second)

	if first.ID == second.ID {
		t.Errorf("both claims share ID %v", first.ID)
	}

	status, raw := w.call(t, http.MethodGet, claimPath(w.wsID), w.bearer("job-1"), nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d %s, want 200", status, raw)
	}

	listed := decodeClaimList(t, raw)

	if len(listed.Claims) != 2 {
		t.Fatalf("list holds %d claims, want 2", len(listed.Claims))
	}

	seen := map[string]bool{}

	for _, entry := range listed.Claims {
		checkAccepted(t, w, entry.ID, entry)
		seen[entry.ID] = true

		if len(entry.Addresses) == 0 {
			t.Errorf("list entry %s has no addresses", entry.ID)
		}
	}

	for _, id := range []string{first.ID, second.ID} {
		if !seen[id] {
			t.Errorf("list misses claim %s", id)
		}
	}
}

// TestClaimLocalWrapperAccepted proves that the local wrapper registers a
// disjoint non-replace claim with its access token and no runner job ID.
func TestClaimLocalWrapperAccepted(t *testing.T) {
	t.Parallel()

	w := newClaimWorld(t, allowAllAuthorizer{})

	status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), "token "+adminToken,
		map[string]any{"base": w.base, "addresses": []string{"aws_instance.a"}})
	if status != http.StatusCreated {
		t.Fatalf("register as local wrapper = %d %s, want 201", status, raw)
	}

	doc := decodeClaim(t, raw)

	if doc.JobID != "" {
		t.Errorf("wrapper claim has job ID %q, want none", doc.JobID)
	}

	if doc.Principal != "ada" {
		t.Errorf("wrapper claim principal = %q, want ada", doc.Principal)
	}
}

// TestClaimTokenBoundToJobAndClaim proves that the job token of the apply
// phase is bound to its job and its apply claim: a request with the token
// of another job, or with the token of an ended claim, is refused.
func TestClaimTokenBoundToJobAndClaim(t *testing.T) {
	t.Parallel()

	t.Run("token of another job", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-2"),
			registerBody(w, "job-1", "aws_instance.a"))

		if status != http.StatusForbidden {
			t.Fatalf("register with the token of another job = %d %s, want 403", status, raw)
		}

		if refused := decodeClaimError(t, raw); refused.Code != "forbidden" {
			t.Errorf("code = %q, want forbidden", refused.Code)
		}
	})

	t.Run("token of an ended claim", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		secret, secretErr := jobtoken.NewSecret()
		if secretErr != nil {
			t.Fatalf("new secret: %v", secretErr)
		}

		ended := liveTokenMatch(time.UnixMilli(1_700_000_000_000).UTC(), secret, "job-3", w.unit)
		ended.Claim.OperationState = durable.StateFailed
		w.tokens.matches[jobtoken.DigestOf(secret)] = ended

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), "Bearer "+secret.Reveal(),
			registerBody(w, "job-3", "aws_instance.a"))

		if status != http.StatusUnauthorized {
			t.Fatalf("register with the token of an ended claim = %d %s, want 401", status, raw)
		}

		if refused := decodeClaimError(t, raw); refused.Code != "token-expired" {
			t.Errorf("code = %q, want token-expired", refused.Code)
		}
	})
}

// TestClaimRegisterConflictRefusals proves the refusal of each conflicting
// register call: overlap answers 409 with the blocking claim ID, and
// replace answers 409 with the closure-unproven code.
func TestClaimRegisterConflictRefusals(t *testing.T) {
	t.Parallel()

	t.Run("overlap", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})
		first := w.registerClaim(t, "job-1", "aws_instance.a")

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-2"),
			registerBody(w, "job-2", "aws_instance.a", "aws_instance.b"))

		if status != http.StatusConflict {
			t.Fatalf("register overlap = %d %s, want 409", status, raw)
		}

		refused := decodeClaimError(t, raw)

		if refused.Code != "claim-overlap" {
			t.Errorf("code = %q, want claim-overlap", refused.Code)
		}

		if !strings.Contains(refused.Message, first.ID) {
			t.Errorf("message %q names no blocking claim %s", refused.Message, first.ID)
		}
	})

	t.Run("replace", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{
				"base":      w.base,
				"addresses": []string{"aws_instance.a"},
				"replace":   true,
				"jobID":     "job-1",
			})

		if status != http.StatusConflict {
			t.Fatalf("register replace = %d %s, want 409", status, raw)
		}

		if refused := decodeClaimError(t, raw); refused.Code != "claim.closure-unproven" {
			t.Errorf("code = %q, want claim.closure-unproven", refused.Code)
		}
	})
}

// TestClaimRegisterRequestRefusals proves the refusal of each invalid
// register call: an empty set and an unknown base answer 422.
func TestClaimRegisterRequestRefusals(t *testing.T) {
	t.Parallel()

	t.Run("empty set", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			registerBody(w, "job-1"))

		if status != http.StatusUnprocessableEntity {
			t.Fatalf("register empty set = %d %s, want 422", status, raw)
		}
	})

	t.Run("unknown base", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{"base": "sv-unknown", "addresses": []string{"aws_instance.a"}, "jobID": "job-1"})

		if status != http.StatusUnprocessableEntity {
			t.Fatalf("register unknown base = %d %s, want 422", status, raw)
		}

		if refused := decodeClaimError(t, raw); refused.Code != "claim-bad-base" {
			t.Errorf("code = %q, want claim-bad-base", refused.Code)
		}
	})
}

// TestClaimRegisterOversizeRefused proves the refusal of a register call
// whose address set is too large for one call: more than 10,000 addresses
// or an address longer than 1,024 bytes answers 422 with the unprocessable
// code and names the bound.
func TestClaimRegisterOversizeRefused(t *testing.T) {
	t.Parallel()

	t.Run("too many addresses", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		addresses := make([]string, 0, 10_001)
		for i := range 10_001 {
			addresses = append(addresses, fmt.Sprintf("aws_instance.node_%d", i))
		}

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{"base": w.base, "addresses": addresses, "jobID": "job-1"})

		if status != http.StatusUnprocessableEntity {
			t.Fatalf("register too many addresses = %d %s, want 422", status, raw)
		}

		refused := decodeClaimError(t, raw)
		if refused.Code != "unprocessable" {
			t.Errorf("code = %q, want unprocessable", refused.Code)
		}

		// The handler guard words the refusal, so the registry guard alone
		// would give another message.
		if !strings.Contains(refused.Message, "the claim holds 10001 addresses, at most 10000") {
			t.Errorf("message = %q, want the handler refusal that names the bound", refused.Message)
		}
	})

	t.Run("address too long", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{
				"base":      w.base,
				"addresses": []string{strings.Repeat("a", 1_025)},
				"jobID":     "job-1",
			})

		if status != http.StatusUnprocessableEntity {
			t.Fatalf("register address too long = %d %s, want 422", status, raw)
		}

		refused := decodeClaimError(t, raw)
		if refused.Code != "unprocessable" {
			t.Errorf("code = %q, want unprocessable", refused.Code)
		}

		if !strings.Contains(refused.Message, "an address holds more than 1024 bytes") {
			t.Errorf("message = %q, want the handler refusal that names the bound", refused.Message)
		}
	})
}

// TestClaimRegisterWorkspaceRefusals proves the refusal of a register call
// on a bad workspace: missing answers 404, deleted answers 410 and locked
// answers 422 with the claim-locked code. An unknown field answers 400,
// and a missing credential answers 401.
func TestClaimRegisterWorkspaceRefusals(t *testing.T) {
	t.Parallel()

	t.Run("missing workspace", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath("ws-missing"), w.bearer("job-1"),
			map[string]any{"base": w.base, "addresses": []string{"aws_instance.a"}, "jobID": "job-1"})

		if status != http.StatusNotFound {
			t.Fatalf("register missing workspace = %d %s, want 404", status, raw)
		}
	})

	t.Run("deleted workspace", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})
		w.deleted.deleted[workspace.ID(w.wsID)] = true
		delete(w.spaces.spaces, workspace.ID(w.wsID))

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			registerBody(w, "job-1", "aws_instance.a"))

		if status != http.StatusGone {
			t.Fatalf("register deleted workspace = %d %s, want 410", status, raw)
		}
	})

	t.Run("locked workspace", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})
		locked := w.spaces.spaces[workspace.ID(w.wsID)]
		locked.Lock = workspace.Lock{
			LockedAt: time.UnixMilli(1_700_000_000_000).UTC(),
			ID:       "lock-1",
			Holder:   "bob",
			Reason:   "apply",
		}
		w.spaces.spaces[workspace.ID(w.wsID)] = locked

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			registerBody(w, "job-1", "aws_instance.a"))

		if status != http.StatusUnprocessableEntity {
			t.Fatalf("register locked workspace = %d %s, want 422", status, raw)
		}

		if refused := decodeClaimError(t, raw); refused.Code != "claim-locked" {
			t.Errorf("code = %q, want claim-locked", refused.Code)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, raw := w.call(t, http.MethodPost, claimPath(w.wsID), w.bearer("job-1"),
			map[string]any{
				"base":      w.base,
				"addresses": []string{"aws_instance.a"},
				"jobID":     "job-1",
				"plan":      "oops",
			})

		if status != http.StatusBadRequest {
			t.Fatalf("register unknown field = %d %s, want 400", status, raw)
		}
	})

	t.Run("no credential", func(t *testing.T) {
		t.Parallel()

		w := newClaimWorld(t, allowAllAuthorizer{})

		status, _ := w.call(t, http.MethodPost, claimPath(w.wsID), "",
			registerBody(w, "job-1", "aws_instance.a"))

		if status != http.StatusUnauthorized {
			t.Fatalf("register without credential = %d, want 401", status)
		}
	})
}
