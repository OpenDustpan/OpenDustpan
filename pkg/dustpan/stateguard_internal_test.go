package dustpan

import (
	"context"
	"errors"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errReaderDown is the error of an inventory reader that does not answer.
var errReaderDown = errors.New("inventory down")

// fakeInventory serves the entries of one namespace.
type fakeInventory struct {
	entries []inventory.Entry
	err     error
}

func (f fakeInventory) ListUnits(_ context.Context, _ namespace.Name) ([]inventory.Entry, error) {
	return f.entries, f.err
}

func (f fakeInventory) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	if f.err != nil {
		return inventory.Entry{}, f.err
	}

	for i := range f.entries {
		if f.entries[i].UnitID == unitID {
			return f.entries[i], nil
		}
	}

	return inventory.Entry{}, inventory.ErrUnitNotFound
}

func gateTestEntry() inventory.Entry {
	return inventory.Entry{
		UnitID: "ws-1", Tool: "opentofu", Namespace: "acme", Name: "web", Collection: "prod",
		Labels: map[string]string{"team": "core", "env": "prod"},
	}
}

func gateTestUnit(t *testing.T, withID bool) identity.Object {
	t.Helper()

	u, err := identity.UnitObject("opentofu", "acme", "web")
	if err != nil {
		t.Fatal(err)
	}

	if withID {
		return u.WithUnitID("ws-1")
	}

	return u
}

func TestInventoryFactsGiveCollectionAndSortedLabels(t *testing.T) {
	t.Parallel()

	for _, withID := range []bool{true, false} {
		f := inventoryFacts{reader: fakeInventory{entries: []inventory.Entry{gateTestEntry()}, err: nil}}

		coll, err := f.Collection(t.Context(), gateTestUnit(t, withID))
		if err != nil || coll != collection.Name("prod") {
			t.Fatalf("Collection(withID %v) = %q, %v, want prod", withID, coll, err)
		}

		labels, err := f.Labels(t.Context(), gateTestUnit(t, withID))
		if err != nil || !slices.Equal(labels, []string{"env=prod", "team=core"}) {
			t.Fatalf("Labels(withID %v) = %v, %v, want the sorted labels", withID, labels, err)
		}
	}
}

func TestInventoryFactsOfAnUnknownUnit(t *testing.T) {
	t.Parallel()

	for _, withID := range []bool{true, false} {
		f := inventoryFacts{reader: fakeInventory{entries: nil, err: nil}}

		if _, err := f.Collection(t.Context(), gateTestUnit(t, withID)); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("Collection(withID %v) = %v, want gate.ErrNotFound", withID, err)
		}

		if _, err := f.Labels(t.Context(), gateTestUnit(t, withID)); !errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("Labels(withID %v) = %v, want gate.ErrNotFound", withID, err)
		}
	}
}

func TestInventoryFactsOfAUnitInNoCollection(t *testing.T) {
	t.Parallel()

	e := gateTestEntry()
	e.Collection = ""
	f := inventoryFacts{reader: fakeInventory{entries: []inventory.Entry{e}, err: nil}}

	if _, err := f.Collection(t.Context(), gateTestUnit(t, true)); !errors.Is(err, gate.ErrNotFound) {
		t.Fatalf("Collection() = %v, want gate.ErrNotFound", err)
	}
}

func TestInventoryFactsFailClosedOnAReaderError(t *testing.T) {
	t.Parallel()

	f := inventoryFacts{reader: fakeInventory{entries: nil, err: errReaderDown}}

	for _, withID := range []bool{true, false} {
		_, err := f.Labels(t.Context(), gateTestUnit(t, withID))
		if !errors.Is(err, errReaderDown) || errors.Is(err, gate.ErrNotFound) {
			t.Fatalf("Labels(withID %v) = %v, want the reader error and no ErrNotFound", withID, err)
		}
	}
}

func TestPrincipalSubjects(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		kind       identity.Kind
		credential identity.Credential
		wantKind   gate.PrincipalKind
		wantCred   gate.CredentialKind
	}{
		{"unknown kind counts as a person", "", "", gate.PrincipalPerson, gate.CredentialAccessToken},
		{"person with a session", identity.KindPerson, identity.CredentialSession, gate.PrincipalPerson, gate.CredentialSession},
		{"service with an access token", identity.KindService, identity.CredentialAccessToken, gate.PrincipalService, gate.CredentialAccessToken},
		{"workload with a trust token", identity.KindWorkload, identity.CredentialTrustToken, gate.PrincipalService, gate.CredentialTrustToken},
		{"service with a job token", identity.KindService, identity.CredentialJobToken, gate.PrincipalService, gate.CredentialJobToken},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := identity.Principal{Subject: "alice", Kind: tc.kind, Credential: tc.credential}

			s, err := principalSubjects{}.Subject(t.Context(), p)
			if err != nil || s.ID != "alice" || s.Kind != tc.wantKind || s.Credential != tc.wantCred ||
				s.BreakGlassAccount || !s.AuthAt.IsZero() {
				t.Fatalf("Subject() = %+v, %v, want kind %s and credential %s", s, err, tc.wantKind, tc.wantCred)
			}
		})
	}
}

func TestPrincipalSubjectsRefuseAnEmptySubject(t *testing.T) {
	t.Parallel()

	for _, subject := range []string{"", "  "} {
		if _, err := (principalSubjects{}).Subject(t.Context(), identity.Principal{Subject: subject}); !errors.Is(err, identity.ErrDenied) {
			t.Fatalf("Subject(%q) = %v, want identity.ErrDenied", subject, err)
		}
	}
}

func TestNoGateServiceWithoutGateStoreOrInventory(t *testing.T) {
	t.Parallel()

	svc, err := newGateService(&settings{}, nil)
	if svc != nil || err != nil {
		t.Fatalf("newGateService(no store) = %v, %v, want nil, nil: no write is checked", svc, err)
	}
}
