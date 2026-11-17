// Package decisionrecord_test holds the tests of the decision record read
// routes: the namespace route GET /api/dustpan/v1/namespaces/{namespace}/
// decision-records/{id} and the installation route GET /api/dustpan/v1/
// decision-records/{id}.
package decisionrecord_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	"opendustpan.org/opendustpan/internal/api/decisionrecord"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// packageSources holds every Go file of this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	testToken     = "decision-records-test-token"
	recordID      = "gdr-a1b2c3d4e5f6g7h8i9j0"
	instalRecord  = "gdr-k1l2m3n4o5p6q7r8s9t0"
	otherRecordID = "gdr-u1v2w3x4y5z6a7b8c9d0"

	// maskedWarning is a stored message that holds a secret value in its
	// stored masked form. The read must return it as stored.
	maskedWarning = "input password is set to ********"
	plainSecret   = "hunter2"

	namespaceTemplate    = "/api/dustpan/v1/namespaces/{namespace}/decision-records/{id}"
	installationTemplate = "/api/dustpan/v1/decision-records/{id}"
)

// fakeRecords keeps the records of the test in memory, by namespace and ID.
type fakeRecords struct {
	mu    sync.Mutex
	byKey map[string]gate.Record
	gets  int
	err   error
}

func newFakeRecords() *fakeRecords {
	f := &fakeRecords{mu: sync.Mutex{}, byKey: map[string]gate.Record{}, gets: 0, err: nil}

	rec := storedRecord("acme", recordID)
	f.byKey[recordKey("acme", recordID)] = rec
	f.byKey[recordKey(audit.ServerScope, instalRecord)] = storedRecord(audit.ServerScope, instalRecord)
	f.byKey[recordKey("other", otherRecordID)] = storedRecord("other", otherRecordID)

	return f
}

func recordKey(ns namespace.Name, id string) string { return string(ns) + "/" + id }

func (f *fakeRecords) Get(_ context.Context, ns namespace.Name, id gate.RecordID) (gate.Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.gets++

	if f.err != nil {
		return gate.Record{}, f.err
	}

	rec, ok := f.byKey[recordKey(ns, string(id))]
	if !ok {
		return gate.Record{}, fmt.Errorf("%w: record %s of %s", gate.ErrNotFound, id, ns)
	}

	return rec, nil
}

// storedRecord returns a stored decision record of ns, with a fact document
// whose change holds the keys of the secrets, never a value. For the server
// scope it returns the installation record of an access limit.
func storedRecord(ns namespace.Name, id string) gate.Record {
	at := time.UnixMilli(1_700_000_000_000).UTC()

	if ns == audit.ServerScope {
		return gate.Record{
			ID: gate.RecordID(id), Point: "",
			Change:       gate.ChangeRef{Kind: gate.ChangeAccess, ID: ""},
			Namespace:    audit.ServerScope,
			At:           at,
			Subject:      "bo@example.com",
			Schema:       gate.SchemaVersion{Subject: "access", Major: 1, Minor: 0},
			PathSets:     nil,
			Units:        nil,
			Requirements: nil,
			Warnings:     nil,
			AutoApply:    false,
			Controls:     nil,
			BreakGlass:   "",
			Result:       gate.ResultDeny,
			Action:       "unit:write",
			Count:        1,
			LastAt:       at,
		}
	}

	return gate.Record{
		ID:        gate.RecordID(id),
		Point:     gate.PointChangeApply,
		Change:    gate.ChangeRef{Kind: gate.ChangeChangeOrder, ID: "co-1"},
		Namespace: ns,
		At:        at,
		Subject:   "bo@example.com",
		Schema:    gate.SchemaVersion{Subject: "change.apply", Major: 1, Minor: 0},
		PathSets:  []gate.PathSetVersion{{Scope: gate.NamespaceScope(ns), ID: "network", Version: 3}},
		Units: []gate.UnitResult{{
			Unit: mustUnitRef("opentofu", ns, "web"),
			Facts: gate.Facts{
				Schema:    gate.SchemaVersion{Subject: "change.apply", Major: 1, Minor: 0},
				Principal: gate.PrincipalFacts{Kind: gate.PrincipalPerson, Credential: gate.CredentialSession},
				Source: gate.SourceFacts{
					Kind: "test", Ref: "", Protected: false, Commit: "", UpstreamUnit: "",
				},
				Unit:      gate.UnitFacts{Tool: "opentofu", Collection: "", Labels: []string{"env=prod"}},
				Operation: gate.OperationFacts{Kind: gate.OperationApply},
				Change: gate.ChangeFacts{
					InputKeys:     []string{"plan"},
					SecretKeys:    []string{"password", "token"},
					TruncatedKeys: nil,
					Paths:         nil,
				},
				Plan: gate.PlanFacts{
					Available:     false,
					Add:           0,
					Change:        0,
					Replace:       0,
					Destroy:       0,
					ResourceTypes: nil,
				},
				Cost:   gate.CostFacts{Available: false, DeltaMonthly: 0, TotalMonthly: 0, Currency: ""},
				Budget: gate.BudgetFacts{Available: false, Level: "", PercentUsed: 0},
				Drift:  gate.DriftFacts{Available: false, Resources: 0, AgeSeconds: 0},
				ResourcePolicy: gate.ResourcePolicyFacts{
					Available:       false,
					MandatoryFailed: 0,
					AdvisoryFailed:  0,
					Unreadable:      0,
				},
				ExternalChecks: gate.ExternalCheckFacts{Passed: nil, Failed: nil, Pending: nil, TimedOut: nil},
				Quota:          gate.QuotaFacts{Available: false, MaxPercentUsed: 0, Exceeded: nil},
				Dependents:     gate.DependentFacts{Available: false, Count: 0, Units: nil},
				Paths:          gate.PathFacts{Available: false, Touches: nil, Only: nil},
				Document:       gate.DocumentFacts{Truncated: nil},
				State:          gate.StateFacts{Available: false, Resources: 0, Removed: nil},
				Profile: gate.ProfileFacts{
					Name:          "",
					Access:        "",
					RefProtected:  false,
					JobOpen:       false,
					RoutingChange: false,
					ImportsAdded:  nil,
				},
			},
			Digest: "3f1c9d4b7a2e5068c1d8f0a4b6e2d7c9e3f1c9d4b7a2e5068c1d8f0a4b6e2d7c9",
			Gates:  nil,
			Result: gate.ResultDeny,
		}},
		Requirements: nil,
		Warnings:     []string{maskedWarning},
		AutoApply:    false,
		Controls:     nil,
		BreakGlass:   "",
		Result:       gate.ResultDeny,
		Action:       "",
		Count:        1,
		LastAt:       at,
	}
}

func mustUnitRef(tool string, ns namespace.Name, name string) gate.UnitRef {
	obj, err := identity.UnitObject(tool, ns, name)
	if err != nil {
		panic(err)
	}

	return gate.UnitRef{Object: obj}
}

// fakeNamespaces answers every namespace except "absent", whose primary installation is the test
// installation.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.Unix(0, 0),
		Name:                name,
		PrimaryInstallation: "test-region",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (fakeNamespaces) Ping(context.Context) error { return nil }

// errNoListing reports a call of ListObjects, which this API never makes.
var errNoListing = errors.New("no route lists objects")

// recordingAuthorizer allows the checks that holds names, as "<action> <object>".
type recordingAuthorizer struct {
	holds  map[string]bool
	checks []string
	mu     sync.Mutex
}

func newAuthorizer(holds ...string) *recordingAuthorizer {
	a := &recordingAuthorizer{holds: map[string]bool{}, checks: nil, mu: sync.Mutex{}}
	for _, h := range holds {
		a.holds[h] = true
	}

	return a
}

func (a *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = append(a.checks, key)

	if !a.holds[key] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, key)
	}

	return nil
}

// ListObjects implements identity.Authorizer. No route of this API lists
// objects.
func (*recordingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// tokenAuthenticator signs in the principal bo for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != testToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "bo@example.com", DisplayName: "bo", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Kind:       identity.KindPerson,
		Credential: identity.CredentialAccessToken,
		JobID:      "",
	}, nil
}

// testAudit keeps the entries of the handler in memory, for the tests that read them.
type testAudit struct{ entries []audit.Entry }

func (a *testAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	if e.Namespace != fence.Namespace {
		return audit.Entry{}, audit.ErrInvalidEntry
	}

	a.entries = append(a.entries, e)

	return e, nil
}

func (a *testAudit) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	a.entries = append(a.entries, e)

	return e, nil
}

func (a *testAudit) List(context.Context, namespace.Name, audit.Page) (audit.Listing, error) {
	return audit.Listing{Entries: append([]audit.Entry(nil), a.entries...), Total: int64(len(a.entries))}, nil
}

func (a *testAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

// heldFor returns the holds of the checks of a principal on a namespace and on
// the installation: namespace:read, namespace:read-gates and installation:read-gates.
func namespaceHolds(ns namespace.Name) []string {
	return []string{
		string(identity.ActionNamespaceRead) + " " + identity.NamespaceObject(ns).String(),
		string(identity.ActionNamespaceReadGates) + " " + identity.NamespaceObject(ns).String(),
	}
}

// readOnlyHolds returns the one hold of namespace:read on a namespace, with no
// namespace:read-gates.
func readOnlyHolds(ns namespace.Name) []string {
	return []string{string(identity.ActionNamespaceRead) + " " + identity.NamespaceObject(ns).String()}
}

func installationHolds() []string {
	return []string{string(identity.ActionInstallationReadGates) + " " + identity.ServerObject().String()}
}

func newHandler(t *testing.T, authz identity.Authorizer, records decisionrecord.Records) testHandler {
	t.Helper()

	return build(t, authz, records, &testAudit{entries: nil})
}

// newAuditedHandler builds a handler over the stored records of newFakeRecords
// with sink as its audit store.
func newAuditedHandler(t *testing.T, authz identity.Authorizer, sink *testAudit) testHandler {
	t.Helper()

	return build(t, authz, newFakeRecords(), sink)
}

func build(t *testing.T, authz identity.Authorizer, records decisionrecord.Records, sink *testAudit) testHandler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := decisionrecord.NewHandler(decisionrecord.Config{
		Namespaces:     namespaces,
		Records:        records,
		Authenticator:  tokenAuthenticator{},
		Authorizer:     authz,
		Audit:          sink,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}

	// The success statuses of the routes are checked against the description
	// of the adapter, as in the other tool-neutral APIs.
	return testHandler{handler, apidesctest.CheckStatus(t, handler)}
}

// testHandler is the handler of the adapter with the check of the success
// statuses on its answers. It serves through served; the tests that read the
// route table and the operations use the embedded handler.
type testHandler struct {
	*decisionrecord.Handler

	served http.Handler
}

// ServeHTTP serves with the check of the description.
func (h testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.served.ServeHTTP(w, r) }

func get(ctx context.Context, t *testing.T, h http.Handler, path, token string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequestWithContext(ctx, http.MethodGet, path, http.NoBody)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	return w
}

const notFoundBody = `{"message":"no decision record that you can read has this ID","code":404,"errorCode":"gate.not-found"}` + "\n"

// TestDecisionRecordReadReturnsStoredRecord reads an own-namespace record with
// a personal access token on the namespace route, and proves that the answer is
// the stored record, with the stored masked form of the secrets.
func TestDecisionRecordReadReturnsStoredRecord(t *testing.T) {
	t.Parallel()

	records := newFakeRecords()
	handler := newHandler(t, newAuthorizer(namespaceHolds("acme")...), records)

	w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/acme/decision-records/"+recordID, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body)
	}

	var rec gate.Record
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("decode the record: %v; body: %s", err, w.Body)
	}

	want := records.byKey[recordKey("acme", recordID)]
	if rec.ID != want.ID {
		t.Errorf("id = %q, want %q", rec.ID, want.ID)
	}

	if rec.Namespace != "acme" {
		t.Errorf("namespace = %q, want acme", rec.Namespace)
	}

	if rec.Result != gate.ResultDeny {
		t.Errorf("result = %q, want deny", rec.Result)
	}

	// The stored masked form: the fact document holds the keys of the
	// secrets and no value, and the answer shows exactly that.
	if !strings.Contains(w.Body.String(), `"secretKeys":["password","token"]`) {
		t.Errorf("body holds no secretKeys: %s", w.Body)
	}

	// The stored message holds the masked form of a secret. The answer
	// returns it as stored, and no plain value is in the answer.
	if !slices.Equal(rec.Warnings, []string{maskedWarning}) {
		t.Errorf("warnings = %q, want the stored masked form %q", rec.Warnings, maskedWarning)
	}

	if strings.Contains(w.Body.String(), plainSecret) {
		t.Errorf("body holds a secret value: %s", w.Body)
	}
}

// TestDecisionRecordReadDeniedGives404 proves that a caller with namespace
// access but no namespace gate read action gets the 404 of an unknown ID.
func TestDecisionRecordReadDeniedGives404(t *testing.T) {
	t.Parallel()

	// The caller holds namespace:read (to reach the namespace) but not
	// namespace:read-gates.
	holds := []string{string(identity.ActionNamespaceRead) + " " + identity.NamespaceObject("acme").String()}

	handler := newHandler(t, newAuthorizer(holds...), newFakeRecords())

	w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/acme/decision-records/"+recordID, testToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body)
	}

	if w.Body.String() != notFoundBody {
		t.Fatalf("body = %q, want %q", w.Body.String(), notFoundBody)
	}

	if strings.Contains(w.Body.String(), recordID) {
		t.Errorf("body names the record ID: %s", w.Body)
	}
}

// TestDecisionRecordReadForeignGives404 proves that a read of a record of
// another namespace, a malformed ID, an expired credential and no sign-in all
// answer the same 404 as an unknown ID.
func TestDecisionRecordReadForeignGives404(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		path  string
		holds []string
		token string
	}{
		{
			name:  "a record of another namespace",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/" + otherRecordID,
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			name:  "a record ID of the installation scope on the namespace route",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/" + instalRecord,
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			name:  "a malformed ID",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/gdr-too-short",
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			name:  "a one-character ID",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/x",
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			name:  "an empty ID on the namespace route",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/",
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			name:  "an empty ID on the installation route",
			path:  "/api/dustpan/v1/decision-records/",
			holds: installationHolds(), token: testToken,
		},
		{
			name:  "an invalid namespace name",
			path:  "/api/dustpan/v1/namespaces/Bad_Name!/decision-records/" + recordID,
			holds: namespaceHolds("acme"), token: testToken,
		},
		{
			// The authorizer allows every check of the route, so only the
			// sign-in step stops this request.
			name:  "no sign-in",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/" + recordID,
			holds: namespaceHolds("acme"), token: "",
		},
		{
			name:  "an expired credential",
			path:  "/api/dustpan/v1/namespaces/acme/decision-records/" + recordID,
			holds: namespaceHolds("acme"), token: "expired-token",
		},
		{
			name:  "a namespace the caller cannot read",
			path:  "/api/dustpan/v1/namespaces/absent/decision-records/" + recordID,
			holds: namespaceHolds("absent"), token: testToken,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// The absent case needs no token: the namespace does not exist.
			authz := newAuthorizer(tc.holds...)
			handler := newHandler(t, authz, newFakeRecords())

			w := get(t.Context(), t, handler, tc.path, tc.token)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body)
			}

			if w.Body.String() != notFoundBody {
				t.Fatalf("body = %q, want %q", w.Body.String(), notFoundBody)
			}
		})
	}
}

// TestInstallationDecisionRecordRead reads an installation record on the
// installation route, with and without installation:read-gates.
func TestInstallationDecisionRecordRead(t *testing.T) {
	t.Parallel()

	t.Run("a caller with installation:read-gates reads the record", func(t *testing.T) {
		t.Parallel()

		checkInstallationRecordRead(t)
	})

	t.Run("a caller without installation:read-gates gets the 404 of an unknown ID", func(t *testing.T) {
		t.Parallel()

		checkInstallationRecordDenied(t)
	})

	t.Run("a namespace record ID on the installation route answers 404", func(t *testing.T) {
		t.Parallel()

		checkInstallationRouteNamespaceRecord(t)
	})
}

// checkInstallationRecordRead proves that a caller with
// installation:read-gates reads the installation record.
func checkInstallationRecordRead(t *testing.T) {
	t.Helper()

	handler := newHandler(t, newAuthorizer(installationHolds()...), newFakeRecords())

	w := get(t.Context(), t, handler, "/api/dustpan/v1/decision-records/"+instalRecord, testToken)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body)
	}

	var rec gate.Record
	if err := json.Unmarshal(w.Body.Bytes(), &rec); err != nil {
		t.Fatalf("decode the record: %v", err)
	}

	if rec.ID != gate.RecordID(instalRecord) {
		t.Errorf("id = %q, want %q", rec.ID, instalRecord)
	}

	if rec.Namespace != audit.ServerScope {
		t.Errorf("namespace = %q, want %q", rec.Namespace, audit.ServerScope)
	}
}

// checkInstallationRecordDenied proves that a caller without
// installation:read-gates gets the 404 of an unknown ID.
func checkInstallationRecordDenied(t *testing.T) {
	t.Helper()

	handler := newHandler(t, newAuthorizer(), newFakeRecords())

	w := get(t.Context(), t, handler, "/api/dustpan/v1/decision-records/"+instalRecord, testToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body)
	}

	if w.Body.String() != notFoundBody {
		t.Fatalf("body = %q, want %q", w.Body.String(), notFoundBody)
	}
}

// checkInstallationRouteNamespaceRecord proves that a namespace record ID on
// the installation route answers 404.
func checkInstallationRouteNamespaceRecord(t *testing.T) {
	t.Helper()

	handler := newHandler(t, newAuthorizer(installationHolds()...), newFakeRecords())

	w := get(t.Context(), t, handler, "/api/dustpan/v1/decision-records/"+recordID, testToken)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body)
	}

	if w.Body.String() != notFoundBody {
		t.Fatalf("body = %q, want %q", w.Body.String(), notFoundBody)
	}
}

// TestDecisionRecordReadStoreErrorGives503 proves that a store error answers
// 503 with no record bytes.
func TestDecisionRecordReadStoreErrorGives503(t *testing.T) {
	t.Parallel()

	records := newFakeRecords()
	records.err = fmt.Errorf("%w: connection reset", gate.ErrUnavailable)

	handler := newHandler(t, newAuthorizer(namespaceHolds("acme")...), records)

	w := get(t.Context(), t, handler, "/api/dustpan/v1/namespaces/acme/decision-records/"+recordID, testToken)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body: %s", w.Code, w.Body)
	}

	if strings.Contains(w.Body.String(), recordID) {
		t.Errorf("body names the record ID: %s", w.Body)
	}

	if !strings.Contains(w.Body.String(), "gate.unavailable") {
		t.Errorf("body holds no gate.unavailable code: %s", w.Body)
	}
}

// pathOf returns the path of a template with the test values filled in.
func pathOf(template string) string {
	return strings.NewReplacer("{namespace}", "acme", "{id}", recordID).Replace(template)
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no read action or no resolver, and for a write route with no set
// action. It pins the read action of each route, checks both directions of
// the pattern table, and proves that a caller with no token gets the one
// not-found 404, never 401 and never 200.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(), newFakeRecords())

	// The pinned read actions of both routes, in the order of the route table.
	wantReads := map[string][]identity.Action{
		http.MethodGet + " " + namespaceTemplate:    {identity.ActionNamespaceReadGates},
		http.MethodGet + " " + installationTemplate: {identity.ActionInstallationReadGates},
	}

	byPattern := checkRouteTable(t, handler, wantReads)
	checkServedPatterns(t, handler, byPattern)
	checkPinnedRoutes(t, byPattern, wantReads)
	checkNoTokenReads(t, handler)
}

// checkRouteTable checks every row of the route table, and returns the rows
// by pattern: no duplicate pattern, every route authorized with a read
// action, a resolver and a handler, only read routes without a set action,
// and the pinned read actions.
func checkRouteTable(
	t *testing.T, handler testHandler, wantReads map[string][]identity.Action,
) map[string]decisionrecord.Route {
	t.Helper()

	byPattern := map[string]decisionrecord.Route{}

	for _, rt := range handler.Routes() {
		checkRouteRow(t, byPattern, rt, wantReads)
	}

	return byPattern
}

// checkRouteRow checks one row of the route table against the pinned reads,
// and records it by pattern.
func checkRouteRow(
	t *testing.T, byPattern map[string]decisionrecord.Route, rt decisionrecord.Route,
	wantReads map[string][]identity.Action,
) {
	t.Helper()

	pattern := rt.Method + " " + rt.Path

	if _, dup := byPattern[pattern]; dup {
		t.Fatalf("duplicate route %q", pattern)
	}

	byPattern[pattern] = rt

	switch {
	case rt.Scope != route.ScopeAuthorized:
		t.Errorf("route %q is not authorized: this API serves no public route", pattern)
	case len(rt.Reads) == 0:
		t.Errorf("route %q declares no read action", pattern)
	case rt.Resolve == nil || rt.Serve == nil:
		t.Errorf("route %q declares no resolver or no handler", pattern)
	case (rt.Method == http.MethodGet) != (len(rt.Actions) == 0):
		t.Errorf("route %q: only a read route may declare no set action", pattern)
	default:
	}

	want, known := wantReads[pattern]
	if !known {
		t.Errorf("route %q is not on the pinned list", pattern)

		return
	}

	if !slices.Equal(rt.Reads, want) {
		t.Errorf("route %q declares the reads %v, want %v", pattern, rt.Reads, want)
	}
}

// checkServedPatterns proves that every served route has a table row.
func checkServedPatterns(t *testing.T, handler testHandler, byPattern map[string]decisionrecord.Route) {
	t.Helper()

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("served route %q has no table row", pattern)
		}
	}
}

// checkPinnedRoutes proves that every pinned route has a table row.
func checkPinnedRoutes(
	t *testing.T,
	byPattern map[string]decisionrecord.Route,
	wantReads map[string][]identity.Action,
) {
	t.Helper()

	for pattern := range wantReads {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("pinned route %q has no table row", pattern)
		}
	}
}

// checkNoTokenReads proves that a caller with no token gets the one
// not-found answer on every route.
func checkNoTokenReads(t *testing.T, handler testHandler) {
	t.Helper()

	for _, template := range []string{namespaceTemplate, installationTemplate} {
		w := get(t.Context(), t, handler, pathOf(template), "")
		if w.Code != http.StatusNotFound {
			t.Errorf("route %q with no token = %d, want 404", template, w.Code)
		}

		if w.Body.String() != notFoundBody {
			t.Errorf("route %q with no token answered %q, want %q", template, w.Body.String(), notFoundBody)
		}
	}
}

// TestOperationsDeclareBothRoutes checks the operations that the adapter
// contributes to the description of the tool-neutral REST API: both routes,
// with the actions that the route table checks.
func TestOperationsDeclareBothRoutes(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(), newFakeRecords())

	byID := map[string]apidesc.Operation{}

	for _, op := range handler.Operations() {
		byID[op.ID] = op
	}

	for _, want := range []struct {
		id      string
		path    string
		actions []string
	}{
		{"getNamespaceDecisionRecord", namespaceTemplate, []string{"namespace:read", "namespace:read-gates"}},
		{"getInstallationDecisionRecord", installationTemplate, []string{"installation:read-gates"}},
	} {
		op, ok := byID[want.id]
		if !ok {
			t.Errorf("operation %q is not in the description of the adapter", want.id)
			continue
		}

		if op.Path != want.path {
			t.Errorf("operation %q path = %q, want %q", want.id, op.Path, want.path)
		}

		if !slices.Equal(op.Actions, want.actions) {
			t.Errorf("operation %q actions = %v, want %v", want.id, op.Actions, want.actions)
		}
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: no
// production file of this package builds a ServeMux or reaches the mux.
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

// TestNewServiceRefusesNilRecords proves that the service cannot be built
// without its port.
func TestNewServiceRefusesNilRecords(t *testing.T) {
	t.Parallel()

	svc, err := decisionrecord.NewService(nil)
	if !errors.Is(err, decisionrecord.ErrMissingDependency) {
		t.Fatalf("NewService(nil) error = %v, want ErrMissingDependency", err)
	}

	if svc != nil {
		t.Error("NewService(nil) returned a service")
	}
}

// TestDeniedReadWritesAuditEntry proves that a read that the authorizer
// refuses writes one audit entry, and that a read with a malformed ID, which
// the authorizer allows, writes none.
func TestDeniedReadWritesAuditEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		path        string
		holds       []string
		wantEntries int
	}{
		{
			name:        "a read without namespace:read-gates",
			path:        "/api/dustpan/v1/namespaces/acme/decision-records/" + recordID,
			holds:       readOnlyHolds("acme"),
			wantEntries: 1,
		},
		{
			name:        "a malformed ID that the authorizer allows",
			path:        "/api/dustpan/v1/namespaces/acme/decision-records/gdr-too-short",
			holds:       namespaceHolds("acme"),
			wantEntries: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			sink := &testAudit{entries: nil}
			handler := newAuditedHandler(t, newAuthorizer(tc.holds...), sink)

			w := get(t.Context(), t, handler, tc.path, testToken)
			if w.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body: %s", w.Code, w.Body)
			}

			if got := len(sink.entries); got != tc.wantEntries {
				t.Fatalf("audit entries = %d, want %d: %+v", got, tc.wantEntries, sink.entries)
			}

			for _, e := range sink.entries {
				if strings.Contains(fmt.Sprintf("%+v", e), plainSecret) {
					t.Errorf("entry holds a secret value: %+v", e)
				}
			}
		})
	}
}
