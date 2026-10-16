package pulumi_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/internal/token"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// leaseAddressGate is a pulumi AddressGate stub that counts calls, records
// the namespace and answers err.
type leaseAddressGate struct {
	calls int
	ns    namespace.Name
	err   error
}

// CheckAddress implements pulumi.AddressGate.
func (g *leaseAddressGate) CheckAddress(_ context.Context, ns namespace.Name) error {
	g.calls++
	g.ns = ns

	return g.err
}

// newUpdateFixtureWithAddressGate builds an update fixture as
// newUpdateFixtureWithGate does, with the address gate set.
func newUpdateFixtureWithAddressGate(
	t *testing.T, authz identity.Authorizer, gate pulumi.AddressGate,
) updateFixture {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Tokens:             nil,
		Updates:            mem,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	handler, err := pulumi.NewHandler(pulumi.Config{
		DisablePulumiJournaling: false,
		Stacks:                  stacks,
		Updates:                 updates,
		Secrets:                 newTestSecrets(t, namespaces),
		Namespaces:              namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              authz,
		AddressGate:             gate,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   nil,
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return updateFixture{handler: handler, updates: updates, ref: ref}
}

// TestLeaseAddressGateDenies proves a denying address gate answers 403 on a
// lease route before the handler runs: even a bad body answers 403, not 400.
// A missing lease answers 401 and a bad path answers 400, both without
// calling the gate: the scheme check runs first, then Resolve, then the token
// check, then the gate.
func TestLeaseAddressGateDenies(t *testing.T) {
	t.Parallel()

	gate := &leaseAddressGate{calls: 0, ns: "", err: identity.ErrDenied}
	f := newUpdateFixtureWithAddressGate(t, allowAll(), gate)

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `not-json`))
	if status != http.StatusForbidden {
		t.Fatalf("denied renew answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}

	if gate.calls != 1 {
		t.Fatalf("gate calls = %d, want 1", gate.calls)
	}

	if gate.ns != "acme" {
		t.Fatalf("gate namespace = %q, want acme", gate.ns)
	}

	status, body = f.doUpdate(t, anonymousRequest(http.MethodPost, paths.Renew, `{"duration":60}`))
	if status != http.StatusUnauthorized {
		t.Fatalf("anonymous renew answers %d, want %d (%s)", status, http.StatusUnauthorized, body)
	}

	if gate.calls != 1 {
		t.Fatalf("gate calls after an anonymous request = %d, want 1", gate.calls)
	}

	bad := leaseRequest(
		http.MethodPost, "/api/stacks/bad!ns/web/prod/update/"+id.String()+"/renew_lease", lease, `{"duration":60}`,
	)

	status, body = f.doUpdate(t, bad)
	if status != http.StatusBadRequest {
		t.Fatalf("bad path renew answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	if gate.calls != 1 {
		t.Fatalf("gate calls after a bad path = %d, want 1", gate.calls)
	}
}

// TestLeaseAddressGateAllows proves an allowing gate changes nothing: a
// valid renewal answers 200 and a bad body answers 400 from the handler.
func TestLeaseAddressGateAllows(t *testing.T) {
	t.Parallel()

	gate := &leaseAddressGate{calls: 0, ns: "", err: nil}
	f := newUpdateFixtureWithAddressGate(t, allowAll(), gate)

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("allowed renew answers %d, want %d (%s)", status, http.StatusOK, body)
	}

	status, body = f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `not-json`))
	if status != http.StatusBadRequest {
		t.Fatalf("bad body renew answers %d, want %d (%s)", status, http.StatusBadRequest, body)
	}

	if gate.calls != 2 {
		t.Fatalf("gate calls = %d, want 2", gate.calls)
	}
}

// TestLeaseNilGateUnchanged proves a nil address gate changes nothing: a
// valid renewal answers 200 as before.
func TestLeaseNilGateUnchanged(t *testing.T) {
	t.Parallel()

	f := newUpdateFixture(t, allowAll())

	id, lease := f.mintStarted(t)
	paths := mintedPaths(id)

	status, body := f.doUpdate(t, leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`))
	if status != http.StatusOK {
		t.Fatalf("renew without a gate answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}

// sharedLeaseSetup holds one namespace service, stack service, update
// service and reference that several handlers share, so one update ID works
// on every gate.
type sharedLeaseSetup struct {
	namespaces *namespace.Service
	stacks     *stack.Service
	updates    *stack.Updates
	ref        stack.Ref
}

// newSharedLeaseServices builds the shared services for the gate order test.
func newSharedLeaseServices(t *testing.T) sharedLeaseSetup {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Region: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stores := &recordingStackStore{
		created:   nil,
		renamed:   nil,
		tags:      nil,
		deleted:   nil,
		forced:    nil,
		entries:   nil,
		deleteErr: nil,
	}

	stacks, err := stack.NewService(
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil, Retention: 0},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
		Tokens:             nil,
		Updates:            mem,
		Checkpoints:        mem,
		Events:             mem,
		Namespaces:         namespaces,
		Stacks:             stacks,
		Blobs:              newMemBlob(),
		Now:                nil,
		Logger:             nil,
		MaxCheckpointBytes: 0,
		Journal:            nil,
		Replayer:           nil,
	})
	if err != nil {
		t.Fatalf("updates service: %v", err)
	}

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	return sharedLeaseSetup{namespaces: namespaces, stacks: stacks, updates: updates, ref: ref}
}

// newSharedLeaseFixture builds a handler over the shared services with the
// given gate.
func newSharedLeaseFixture(t *testing.T, setup sharedLeaseSetup, gate pulumi.AddressGate) updateFixture {
	t.Helper()

	handler, err := pulumi.NewHandler(pulumi.Config{
		DisablePulumiJournaling: false,
		Stacks:                  setup.stacks,
		Updates:                 setup.updates,
		Secrets:                 newTestSecrets(t, setup.namespaces),
		Namespaces:              setup.namespaces,
		Authenticator:           tokenAuthenticator{},
		Authorizer:              allowAll(),
		AddressGate:             gate,
		Logger:                  slog.New(slog.DiscardHandler),
		BaseURL:                 baseurl.Resolver{},
		MaxBodyBytesInFlight:    0,
		Gate:                    nil,
		Audit:                   nil,
		OnAuditFailure:          nil,
		CheckpointDeltaCutoff:   0,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return updateFixture{handler: handler, updates: setup.updates, ref: setup.ref}
}

// TestLeaseTokenBeforeAddressGate proves the token check runs before the
// address gate: a bad lease token gets the same status and body with a
// denying gate, an allowing gate and no gate, without calling either gate. A
// valid token with a denying gate gets 403, and with an allowing gate it
// passes.
func TestLeaseTokenBeforeAddressGate(t *testing.T) {
	t.Parallel()

	setup := newSharedLeaseServices(t)

	denying := &leaseAddressGate{calls: 0, ns: "", err: identity.ErrDenied}
	allowing := &leaseAddressGate{calls: 0, ns: "", err: nil}

	denied := newSharedLeaseFixture(t, setup, denying)
	allowed := newSharedLeaseFixture(t, setup, allowing)
	plain := newSharedLeaseFixture(t, setup, nil)

	id, lease := denied.mintStarted(t)
	paths := mintedPaths(id)

	foreign, err := token.New()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}

	bad := leaseRequest(http.MethodPost, paths.Renew, foreign.Reveal(), `{"duration":60}`)

	deniedStatus, deniedBody := denied.doUpdate(t, bad)
	allowedStatus, allowedBody := allowed.doUpdate(t, bad)
	plainStatus, plainBody := plain.doUpdate(t, bad)

	if deniedStatus != http.StatusUnauthorized {
		t.Fatalf(
			"bad token with denying gate answers %d, want %d (%s)",
			deniedStatus,
			http.StatusUnauthorized,
			deniedBody,
		)
	}

	if allowedStatus != deniedStatus || allowedBody != deniedBody {
		t.Fatalf(
			"bad token with allowing gate answers %d %q, want %d %q",
			allowedStatus,
			allowedBody,
			deniedStatus,
			deniedBody,
		)
	}

	if plainStatus != deniedStatus || plainBody != deniedBody {
		t.Fatalf(
			"bad token without a gate answers %d %q, want %d %q",
			plainStatus,
			plainBody,
			deniedStatus,
			deniedBody,
		)
	}

	if denying.calls != 0 {
		t.Fatalf("denying gate calls after bad token = %d, want 0", denying.calls)
	}

	if allowing.calls != 0 {
		t.Fatalf("allowing gate calls after bad token = %d, want 0", allowing.calls)
	}

	good := leaseRequest(http.MethodPost, paths.Renew, lease, `{"duration":60}`)

	status, body := denied.doUpdate(t, good)
	if status != http.StatusForbidden {
		t.Fatalf("valid token with denying gate answers %d, want %d (%s)", status, http.StatusForbidden, body)
	}

	status, body = allowed.doUpdate(t, good)
	if status != http.StatusOK {
		t.Fatalf("valid token with allowing gate answers %d, want %d (%s)", status, http.StatusOK, body)
	}
}
