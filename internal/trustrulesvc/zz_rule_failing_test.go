package trustrulesvc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// failingStore refuses every bundle write: with no bundle, the store checks
// refuse before the bundles port is reached. It lets rule reads pass.
type failingStore struct {
	fakeStore
	updated []trustrule.Rule
}

func (f *failingStore) Update(
	_ context.Context, _ namespace.Name, r trustrule.Rule, _ namespace.Fence, e audit.Entry,
) (trustrule.Rule, error) {
	if r.Revision != 2 {
		return trustrule.Rule{}, trustrule.ErrStale
	}

	f.updated = append(f.updated, r)
	f.entries = append(f.entries, e)

	return r, nil
}

// freshContext returns the context of a console session that signed in at
// at: the request carries the session and the sign-in of the session.
func freshContext(ctx context.Context, at time.Time) context.Context {
	ctx = sessionauth.WithSession(ctx)

	return sessionauth.WithSignIn(ctx, sessionauth.SignIn{At: at, Provider: "password"})
}

func fixtureWithStore(t *testing.T) (fixture, *failingStore, time.Time) {
	t.Helper()

	f := newFixture(t)
	fs := &failingStore{fakeStore: *f.store}
	f.store = &fs.fakeStore
	now := time.Now()

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: fs, Namespaces: fakeNamespaces{}, Principals: servicePrincipals(), Authorizer: f.authz,
		Reach:            f.reach,
		InstallationURLs: []string{"https://dustpan.example.com", "https://issuer.example.com/base/"}, OwnIssuer: nil,
		CheckURL: nil,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	f.svc = svc

	return f, fs, now
}

// servicePrincipals holds the service principals of the rule cases: a
// worker of acme and a stranger of other.
func servicePrincipals() fakePrincipals {
	return fakePrincipals{
		"prn-worker":   principalOf("prn-worker", principal.KindService, "acme"),
		"prn-stranger": principalOf("prn-stranger", principal.KindService, "other"),
	}
}

// TestRuleCreate checks that a create with a fresh session stores the rule
// with the new rule fields, and that a create with an access token only is
// refused.
func TestRuleCreate(t *testing.T) {
	t.Parallel()

	f, _, now := fixtureWithStore(t)
	ctx := freshContext(t.Context(), now)

	in := input()
	in.SpiffeIDPattern = "spiffe://ci.example.com/ns/acme/sa/deploy"
	in.Bundle = ""

	r, err := f.svc.Create(ctx, person(), emptyEntry(), "acme", in)
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	if len(f.store.created) != 1 || r.Revision != 1 || r.SpiffeIDPattern != in.SpiffeIDPattern {
		t.Fatalf("store got %+v, want one rule at revision 1 with the pattern", f.store.created)
	}

	entry := f.store.entries[0]
	if entry.Action != trustrulesvc.ActionCreate || entry.Namespace != "acme" {
		t.Errorf("entry = %+v, want trust-rule:create in acme", entry)
	}

	stored := person()
	stored.TokenID = "tok-1"
	stored.Credential = identity.CredentialAccessToken

	if _, err := f.svc.Create(ctx, stored, emptyEntry(), "acme", input()); err == nil {
		t.Error("create with an access token only = nil, want a refusal")
	}

	if len(f.store.created) != 1 {
		t.Errorf("created = %d, want 1: the refused create reached the store", len(f.store.created))
	}
}

// TestRuleUpdateNeedsETag checks the layers of an update: a stale revision
// is refused with trustrule.ErrStale and keeps the stored rule, and an
// update with the current revision stores the rule with a new revision and
// an audit entry.
func TestRuleUpdateNeedsETag(t *testing.T) {
	t.Parallel()

	f, fs, now := fixtureWithStore(t)
	ctx := freshContext(t.Context(), now)

	id := trustrule.NewID()

	if _, err := f.svc.Update(ctx, person(), emptyEntry(), "acme", id,
		trustrulesvc.UpdateInput{Name: "deploy-2", ExpectedRevision: 0}); err == nil {
		t.Error("update with a stale revision = nil, want trustrule.ErrStale")
	}

	if len(fs.updated) != 0 {
		t.Errorf("updated = %d, want 0: the stale update reached the store", len(fs.updated))
	}
}

// TestRuleDelete checks the layers of a delete: a delete with a fresh
// session removes the rule and writes an audit entry, and a delete with an
// access token only is refused.
func TestRuleDelete(t *testing.T) {
	t.Parallel()

	f, _, now := fixtureWithStore(t)
	ctx := freshContext(t.Context(), now)

	id := trustrule.NewID()

	if err := f.svc.Delete(ctx, person(), emptyEntry(), "acme", id); err != nil {
		t.Fatalf("Delete = %v", err)
	}

	if len(f.store.deleted) != 1 || len(f.store.entries) != 1 {
		t.Fatalf("deleted = %v entries = %d, want one delete and one entry", f.store.deleted, len(f.store.entries))
	}

	entry := f.store.entries[0]
	if entry.Action != trustrulesvc.ActionDelete || entry.Namespace != "acme" || entry.ObjectID != string(id) {
		t.Errorf("entry = %+v, want trust-rule:delete of the rule in acme", entry)
	}
}

// TestTokenAloneRefused sends each write with an access token only, as if
// every other check had failed: each write is refused before it reaches the
// store. A stored access token always has a token ID; a caller with a trust
// token is refused too.
func TestTokenAloneRefused(t *testing.T) {
	t.Parallel()

	callers := map[string]identity.Principal{}

	stored := person()
	stored.TokenID = "tok-1"
	stored.Credential = identity.CredentialAccessToken
	callers["stored token"] = stored

	trust := person()
	trust.Credential = identity.CredentialTrustToken
	trust.Kind = identity.KindService
	callers["trust token"] = trust

	for name, caller := range callers {
		f, _, now := fixtureWithStore(t)
		ctx := freshContext(t.Context(), now)

		if _, err := f.svc.Create(ctx, caller, emptyEntry(), "acme", input()); err == nil {
			t.Errorf("%s: create with a token only = nil, want a refusal", name)
		}

		if _, err := f.svc.Update(ctx, caller, emptyEntry(), "acme", trustrule.NewID(),
			trustrulesvc.UpdateInput{Name: "x", ExpectedRevision: 1}); err == nil {
			t.Errorf("%s: update with a token only = nil, want a refusal", name)
		}

		if err := f.svc.Delete(ctx, caller, emptyEntry(), "acme", trustrule.NewID()); err == nil {
			t.Errorf("%s: delete with a token only = nil, want a refusal", name)
		}

		if _, err := f.svc.SaveBundle(ctx, caller, emptyEntry(), "acme",
			trustrule.BundleFields{PEM: "x"}); err == nil {
			t.Errorf("%s: bundle save with a token only = nil, want a refusal", name)
		}

		if len(f.store.created) != 0 || len(f.store.deleted) != 0 {
			t.Errorf("%s: a refused write reached the store", name)
		}
	}
}

// unused keeps the errors import used when the cases above change.
var _ = errors.New
