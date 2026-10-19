// Package profiletest is the conformance suite for profile.Store. Every
// built-in store runs it, and a custom store should too:
//
//	func TestProfileConformance(t *testing.T) {
//		t.Parallel()
//		profiletest.Run(t, func(t *testing.T) profiletest.Store { return newMyStore(t) })
//	}
package profiletest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// Store is every persistence port the suite exercises. The ports are fields
// because a store type cannot hold the List method of two of them.
type Store struct {
	Profiles   profile.Store
	Namespaces namespace.Store
	Audit      audit.Store
	// Records is the store of replay records that completes in the
	// transaction of a write.
	Records idempotency.Store
}

// Factory returns a fresh, empty store for one subtest.
type Factory func(t *testing.T) Store

// Run runs every case of the suite against the stores that newStore returns.
func Run(t *testing.T, newStore Factory) {
	t.Helper()

	cases := []struct {
		name string
		test func(t *testing.T, s Store)
	}{
		{"create and read by name and by ID", testCreateAndGet},
		{"a name in use is a conflict", testDuplicateName},
		{"the same name in two namespaces is allowed", testNameInTwoNamespaces},
		{"a profile of another namespace is not found", testOtherNamespace},
		{"create refuses a first revision that is not number 1", testFirstRevisionNumber},
		{"a stale fence refuses every write", testFenced},
		{"a failed audit write leaves no profile", testAuditRollsBack},
		{"each write appends its audit entry", testAuditAppended},
		{"a write appends every entry in order", testAuditMany},
		{"a write with no audit entry is invalid and changes nothing", testAuditNone},
		{"list is ordered by name and pages", testListPages},
		{"list filters by folder and hides deleted profiles", testListFilters},
		{"save adds the next revision and moves the newest", testSave},
		{"save on a stale base is a conflict", testSaveStale},
		{"save refuses a revision number that is not base plus one", testSaveNumber},
		{"revision 0 is the newest and a missing revision is not found", testRevisionRead},
		{"revisions list newest first and page", testRevisionsList},
		{"delete frees the name and hides the profile", testDelete},
		{"delete on a stale revision is a conflict", testDeleteStale},
		{"the document is stored byte for byte", testDocumentBytes},
		{"a namespace holds at most the limit of profiles", testLimit},
		{"a write stores the answer of its request in its transaction", testCompletionStored},
		{"a failed write leaves the record of its request pending", testCompletionKeptPending},
		{"a write whose record is gone changes nothing", testCompletionLost},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newStore(t))
		})
	}
}

// epochMillis is the time of every write of the suite, in Unix milliseconds.
const epochMillis = 1_700_000_000_000

// subject is the principal of every write of the suite.
const subject = "alice"

// doc is a small valid document.
const doc = "schemaVersion: 1\nvalues:\n  region: eu-west-1\n"

// Sizes of the pages that the cases ask for.
const (
	smallPage = 2
	firstPage = 3
	pageSize  = 5
	bigPage   = 10
)

// secondRevision is the number of the revision after the first.
const secondRevision = 2

func epoch() time.Time { return time.UnixMilli(epochMillis).UTC() }

func sha(n int) string { return fmt.Sprintf("%064x", n) }

func newNamespace(t *testing.T, s Store, name string) namespace.Namespace {
	t.Helper()

	ns := namespace.Namespace{
		ID:              namespace.NewID(),
		CreatedAt:       epoch(),
		Name:            namespace.Name(name),
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.Namespaces.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	return ns
}

func entry(ns namespace.Name, action string) audit.Entry {
	return audit.Entry{
		At: epoch(), Namespace: ns, Subject: subject, IDPSubject: "", TokenID: "", Action: action,
		Object: "namespace:" + ns.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
		PrincipalKind: "", JobID: "",
	}
}

// entries is the audit entries of a write that appends one entry.
func entries(ns namespace.Name, action string) []audit.Entry {
	return []audit.Entry{entry(ns, action)}
}

func newProfile(ns namespace.Name, name string) (profile.Profile, profile.Revision) {
	id := profile.NewID()

	p := profile.Profile{
		ID: id, Namespace: ns, Name: profile.Name(name), Latest: 1, CreatedAt: epoch(), CreatedBy: subject,
		UpdatedAt: epoch(),
	}
	r := profile.Revision{
		Profile: id, Number: 1, Document: []byte(doc), SHA256: sha(1), Author: subject, CreatedAt: epoch(), Reason: "",
	}

	return p, r
}

func anchor(ns namespace.Name, id profile.ID, latest int) profile.Anchor {
	return profile.Anchor{Namespace: ns, ID: id, Latest: latest}
}

func filter(ns namespace.Name, folder string, after profile.Name, limit int) profile.Filter {
	return profile.Filter{Namespace: ns, Folder: folder, After: after, Limit: limit}
}

func create(t *testing.T, s Store, ns namespace.Namespace, name string) profile.Profile {
	t.Helper()

	p, r := newProfile(ns.Name, name)

	got, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	if err != nil {
		t.Fatalf("Create(%q): %v", name, err)
	}

	return got
}

func nextRevision(p profile.Profile, text string) profile.Revision {
	n := p.Latest + 1

	return profile.Revision{
		Profile: p.ID, Number: n, Document: []byte(text), SHA256: sha(n), Author: "bob",
		CreatedAt: epoch().Add(time.Second), Reason: "",
	}
}

func save(t *testing.T, s Store, ns namespace.Namespace, p profile.Profile, text string) profile.Profile {
	t.Helper()

	got, err := s.Profiles.Save(t.Context(), anchor(ns.Name, p.ID, p.Latest), nextRevision(p, text), ns.Fence(),
		entries(ns.Name, "profile:write"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	return got
}

func remove(t *testing.T, s Store, ns namespace.Namespace, p profile.Profile) {
	t.Helper()

	err := s.Profiles.Delete(t.Context(), anchor(ns.Name, p.ID, 0), ns.Fence(), entries(ns.Name, "profile:manage"))
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Errorf("%s: error = %v, want %v", what, err, want)
	}
}
