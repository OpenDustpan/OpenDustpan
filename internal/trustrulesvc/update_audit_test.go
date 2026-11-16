package trustrulesvc_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// seededStore serves one stored rule and records the entries of writes.
type seededStore struct {
	*fakeStore

	stored trustrule.Rule
}

func (s *seededStore) Get(
	_ context.Context, _ namespace.Name, _ trustrule.ID,
) (trustrule.Rule, error) {
	return s.stored, nil
}

// auditFixture returns a service over a store that serves stored.
func auditFixture(t *testing.T, stored trustrule.Rule) fixture {
	t.Helper()

	underlying := &fakeStore{created: nil, entries: nil, deleted: nil}
	store := &seededStore{fakeStore: underlying, stored: stored}
	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	reach := &fakeReach{checked: nil, err: nil}
	people := fakePrincipals{"prn-worker": principalOf("prn-worker", principal.KindService, "acme")}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Principals: people, Authorizer: authz, Reach: reach,
		InstallationURLs: []string{"https://dustpan.example.com"}, OwnIssuer: nil, CheckURL: nil, Bundles: nil,
		Sessions: nil, Now: exchangeNow,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return fixture{svc: svc, store: underlying, authz: authz, reach: reach}
}

// seededRule returns a stored rule at revision 1 with no mTLS fields.
func seededRule() trustrule.Rule {
	return trustrule.Rule{
		CreatedAt: exchangeNow(), ID: trustrule.NewID(), Namespace: "acme", Name: "deploy",
		Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", Revision: 1,
		PrincipalID: "prn-worker", CreatedBy: "ada",
		Conditions: trustrule.SortConditions([]trustrule.Condition{
			{Claim: "sub", Values: []string{"repo:acme/infra"}},
			{Claim: "repository", Values: []string{"acme/infra"}},
		}),
		SpiffeIDPattern: "", Bundle: "",
	}
}

// changeOf returns the change recorded under key, or nil when the entry
// holds no change with that name.
func changeOf(e audit.Entry, key string) *audit.Change {
	for i := range e.Changes {
		if e.Changes[i].Key == key {
			return &e.Changes[i]
		}
	}

	return nil
}

// changeKeys returns the keys of the changes of the entry, in order.
func changeKeys(e audit.Entry) []string {
	keys := make([]string, 0, len(e.Changes))
	for _, c := range e.Changes {
		keys = append(keys, c.Key)
	}

	return keys
}

// TestUpdateAuditNamesEachChangedField changes one field at a time and checks
// that the audit entry names only that change: conditions record the old and
// new text, static keys record a SHA-256 hex digest and never the key text.
func TestUpdateAuditNamesEachChangedField(t *testing.T) {
	t.Parallel()

	newBundle := "trb-0123456789abcdef0123456789abcdef"

	oldStatic := staticKeys(t)
	newStatic := staticKeys(t)

	tests := []struct {
		name   string
		stored trustrule.Rule
		change func(*trustrulesvc.UpdateInput)
		key    string
		check  func(t *testing.T, c audit.Change)
	}{
		{
			name:   "conditions only",
			stored: seededRule(),
			change: func(in *trustrulesvc.UpdateInput) {
				in.ConditionsSet = true
				in.Conditions = []trustrule.Condition{
					{Claim: "sub", Values: []string{"repo:acme/other"}},
					{Claim: "repository", Values: []string{"acme/infra"}},
				}
			},
			key:   "conditions",
			check: checkConditionsChange,
		},
		{
			name: "static keys only",
			stored: func() trustrule.Rule {
				r := seededRule()
				r.Keys = trustrule.KeySourceStatic
				r.Issuer = "https://token.actions.example.com"
				r.StaticKeys = oldStatic

				return r
			}(),
			change: func(in *trustrulesvc.UpdateInput) {
				in.StaticKeys = new(newStatic)
			},
			key:   "staticKeys",
			check: checkStaticKeysChange(t, oldStatic, newStatic),
		},
		{
			name:   "spiffePattern only",
			stored: seededRule(),
			change: func(in *trustrulesvc.UpdateInput) {
				in.SpiffeIDPattern = new("spiffe://example.org/ns/acme/sa/deploy")
			},
			key:   "spiffePattern",
			check: checkSpiffeIDPatternChange,
		},
		{
			name:   "bundle only",
			stored: seededRule(),
			change: func(in *trustrulesvc.UpdateInput) {
				in.Bundle = new(newBundle)
			},
			key:   "bundle",
			check: checkBundleChange(newBundle),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runUpdateAuditCase(t, tc.stored, tc.change, tc.key, tc.check)
		})
	}
}

// runUpdateAuditCase updates the stored rule with change and checks that the
// audit entry names only the change with key, which check then checks.
func runUpdateAuditCase(
	t *testing.T, stored trustrule.Rule, change func(*trustrulesvc.UpdateInput),
	key string, check func(t *testing.T, c audit.Change),
) {
	t.Helper()

	f := auditFixture(t, stored)

	in := renameInput(stored.ID, stored.Name, 1)
	change(&in)

	updated, err := f.svc.Update(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if err != nil {
		t.Fatalf("Update = %v", err)
	}

	if updated.Revision != 2 {
		t.Fatalf("revision = %d, want 2", updated.Revision)
	}

	if len(f.store.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(f.store.entries))
	}

	e := f.store.entries[0]
	if e.Action != trustrulesvc.ActionUpdate {
		t.Fatalf("action = %q, want %q", e.Action, trustrulesvc.ActionUpdate)
	}

	keys := changeKeys(e)
	if len(keys) != 1 || keys[0] != key {
		t.Fatalf("change keys = %v, want only %q", keys, key)
	}

	check(t, e.Changes[0])
}

// checkConditionsChange checks that the conditions change records the stored
// text as old and the updated text as new.
func checkConditionsChange(t *testing.T, c audit.Change) {
	t.Helper()

	if c.Old == nil || !strings.Contains(*c.Old, "repo:acme/infra") {
		t.Errorf("old conditions = %v, want the stored text", c.Old)
	}

	if !strings.Contains(c.New, "repo:acme/other") {
		t.Errorf("new conditions = %q, want the updated text", c.New)
	}
}

// checkStaticKeysChange returns a check that the static keys change records
// the SHA-256 hex digests of old and current, never the key text.
func checkStaticKeysChange(t *testing.T, old, current string) func(t *testing.T, c audit.Change) {
	t.Helper()

	oldSum := sha256.Sum256([]byte(old))
	currentSum := sha256.Sum256([]byte(current))

	return func(t *testing.T, c audit.Change) {
		t.Helper()

		if c.Old == nil || *c.Old != hex.EncodeToString(oldSum[:]) ||
			c.New != hex.EncodeToString(currentSum[:]) {
			t.Fatalf("static keys change = %+v, want the old and new digests", c)
		}

		for _, v := range []string{*c.Old, c.New} {
			if len(v) != 64 || strings.Contains(v, "kty") {
				t.Errorf("static keys value = %q, want a hex digest and never the key text", v)
			}
		}
	}
}

// checkSpiffeIDPatternChange checks that the pattern change records the
// empty old pattern and the updated pattern as new.
func checkSpiffeIDPatternChange(t *testing.T, c audit.Change) {
	t.Helper()

	if c.Old == nil || *c.Old != "" || c.New != "spiffe://example.org/ns/acme/sa/deploy" {
		t.Errorf("spiffePattern change = %+v, want the old and new pattern", c)
	}
}

// checkBundleChange returns a check that the bundle change records the empty
// old bundle and want as new.
func checkBundleChange(want string) func(t *testing.T, c audit.Change) {
	return func(t *testing.T, c audit.Change) {
		t.Helper()

		if c.Old == nil || *c.Old != "" || c.New != want {
			t.Errorf("bundle change = %+v, want the old and new bundle ID", c)
		}
	}
}

// TestCreateAuditNamesBundleAndPattern creates a rule with the mTLS fields and
// checks that the audit entry records the SPIFFE ID pattern and the bundle.
func TestCreateAuditNamesBundleAndPattern(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	in := input()
	in.SpiffeIDPattern = "spiffe://example.org/ns/acme/*"
	in.Bundle = "trb-0123456789abcdef0123456789abcdef"

	r, err := f.svc.Create(freshSessionContext(t), person(), emptyEntry(), "acme", in)
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	if len(f.store.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(f.store.entries))
	}

	e := f.store.entries[0]

	pattern := changeOf(e, "spiffePattern")
	if pattern == nil || pattern.New != in.SpiffeIDPattern || pattern.Old != nil {
		t.Errorf("spiffePattern change = %+v, want the new pattern", pattern)
	}

	bundle := changeOf(e, "bundle")
	if bundle == nil || bundle.New != in.Bundle || bundle.Old != nil {
		t.Errorf("bundle change = %+v, want the new bundle ID", bundle)
	}

	if r.SpiffeIDPattern != in.SpiffeIDPattern || r.Bundle != in.Bundle {
		t.Errorf("rule = %+v, want the mTLS fields stored", r)
	}
}
