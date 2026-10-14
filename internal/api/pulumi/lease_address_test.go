package pulumi_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/baseurl"
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
		stack.Config{Store: stores, Namespaces: namespaces, Now: nil, State: nil},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	mem := newMemUpdateStore()

	updates, err := stack.NewUpdates(stack.UpdatesConfig{
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
// calling the gate: the scheme check runs first, then Resolve, then the gate.
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
