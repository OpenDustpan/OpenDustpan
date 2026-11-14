package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/objectcontext"
	"opendustpan.org/opendustpan/pkg/objectcontext/objectcontexttest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// contextNamespace makes the namespace name in s and returns its fence.
func contextNamespace(t *testing.T, s *sqlite.Store, name string) namespace.Fence {
	t.Helper()

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                namespace.Name(name),
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace %s: %v", name, err)
	}

	return ns.Fence()
}

// contextUnit makes the OpenTofu workspace with the ID u.ID, named like its
// ID, in the namespace of u: the unit is live in the inventory.
func contextUnit(t *testing.T, s *sqlite.Store, u graph.Unit) {
	t.Helper()

	n, err := s.GetNamespace(t.Context(), u.Namespace)
	if err != nil {
		t.Fatalf("get namespace %s: %v", u.Namespace, err)
	}

	name, err := workspace.ParseName(u.ID)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", u.ID, err)
	}

	w := workspace.Workspace{
		NamespaceID: "",
		CreatedAt:   n.CreatedAt, UpdatedAt: n.CreatedAt,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(),
		ID:          workspace.ID(u.ID),
		Ref:         workspace.Ref{Namespace: n.Name, Name: name},
		Overrides:   namespace.Overrides{Forced: false, Unlocked: false},
		Revision:    1, Origin: identity.StateOrigin{Job: "", Commit: ""},
	}

	if err = s.CreateWorkspace(t.Context(), w, n.Fence()); err != nil {
		t.Fatalf("create workspace %s: %v", u.ID, err)
	}
}

// contextPurge deletes the workspace with the ID u.ID and purges it at once,
// so the inventory holds the unit neither live nor deleted.
func contextPurge(t *testing.T, s *sqlite.Store, u graph.Unit) {
	t.Helper()

	n, err := s.GetNamespace(t.Context(), u.Namespace)
	if err != nil {
		t.Fatalf("get namespace %s: %v", u.Namespace, err)
	}

	name, err := workspace.ParseName(u.ID)
	if err != nil {
		t.Fatalf("parse workspace name %q: %v", u.ID, err)
	}

	e := audit.Entry{
		At: n.CreatedAt, Namespace: n.Name, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "workspace:delete", Object: u.Namespace.String() + "/" + u.ID, Version: "", ValuePath: "",
		ValueCount: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0, Changes: nil,
		ObjectID: "", FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
	d := inventory.Deletion{Entry: e, PurgeAfter: e.At}
	ref := workspace.Ref{Namespace: n.Name, Name: name}

	if err = s.DeleteWorkspace(t.Context(), ref, n.Fence(), d); err != nil {
		t.Fatalf("delete workspace %s: %v", u.ID, err)
	}

	if err = s.PurgeWorkspace(t.Context(), workspace.ID(u.ID), n.Fence(), time.Time{}, e); err != nil {
		t.Fatalf("purge workspace %s: %v", u.ID, err)
	}
}

// contextHarness returns the conformance harness over s.
func contextHarness(s *sqlite.Store) objectcontexttest.Harness {
	return objectcontexttest.Harness{
		Store: s,
		Namespace: func(t *testing.T, name string) namespace.Fence {
			t.Helper()

			return contextNamespace(t, s, name)
		},
		Collection: func(t *testing.T, ns, name string) {
			t.Helper()

			n, err := s.GetNamespace(t.Context(), namespace.Name(ns))
			if err != nil {
				t.Fatalf("get namespace %s: %v", ns, err)
			}

			c := collection.Collection{
				CreatedAt: n.CreatedAt,
				ID:        ident.NewID("col-"),
				Ref:       collection.Ref{Namespace: n.Name, Name: mustCollectionName(t, name)},
			}
			e := audit.Entry{
				At: n.CreatedAt, Namespace: n.Name, Subject: "admin", IDPSubject: "", TokenID: "",
				Action: "collection:create", Object: ns + "/" + name, Version: "", ValuePath: "", ValueCount: nil,
				SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
				Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0,
				Changes: nil, ObjectID: "", FenceVersion: 0,
				Reason:        "",
				PrincipalKind: "",
				JobID:         "",
			}

			if err := s.CreateCollection(t.Context(), c, n.Fence(), e); err != nil {
				t.Fatalf("create collection %s: %v", name, err)
			}
		},
		Unit: func(t *testing.T, u graph.Unit) {
			t.Helper()

			contextUnit(t, s, u)
		},
		Purge: func(t *testing.T, u graph.Unit) {
			t.Helper()

			contextPurge(t, s, u)
		},
		Audited: func(t *testing.T, ns string) []audit.Entry {
			t.Helper()

			listing, err := s.List(t.Context(), namespace.Name(ns), audit.Page{Number: 1, Size: audit.MaxPageSize})
			if err != nil {
				t.Fatalf("list audit: %v", err)
			}

			var out []audit.Entry

			for i := range listing.Entries {
				if listing.Entries[i].Action == "unit:set-context" {
					out = append(out, listing.Entries[i])
				}
			}

			slices.SortFunc(out, func(a, b audit.Entry) int { return int(a.Sequence - b.Sequence) })

			return out
		},
	}
}

func TestObjectContextConformance(t *testing.T) {
	t.Parallel()

	objectcontexttest.Run(t, func(t *testing.T) objectcontexttest.Harness {
		t.Helper()

		return contextHarness(open(t))
	})
}

// TestObjectContextOwnerRows checks that the owner rows equal the owners of
// the stored entries after each write.
func TestObjectContextOwnerRows(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s := openPath(t, path)
	h := contextHarness(s)
	fence := h.Namespace(t, "acme")
	ref := objectcontext.UnitRef(graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-1"})

	contextUnit(t, s, ref.Unit)

	raw, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw database: %v", err)
		}
	})

	for _, owners := range [][]string{{"group:a", "group:b"}, {"group:b"}, {"group:c", "group:c"}, nil} {
		p := objectcontext.Patch{Owners: &owners, Summary: nil, Runbook: nil, OnCall: nil, Links: nil}

		e := audit.Entry{
			At: time.UnixMilli(1_800_000_000_000).UTC(), Namespace: "acme", Subject: "user:ada", IDPSubject: "",
			TokenID: "", Action: "unit:set-context", Object: "unit:ws-1", Version: "", ValuePath: "",
			ValueCount: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
			Outcome: audit.OutcomeSuccess, RequestID: "", PrevHash: "", Hash: "", Sequence: 0, Changes: nil,
			ObjectID: "", FenceVersion: 0,
			Reason:        "",
			PrincipalKind: "",
			JobID:         "",
		}

		got, err := s.SetAPI(t.Context(), ref, p, "user:ada", fence, e)
		if err != nil {
			t.Fatalf("SetAPI %v: %v", owners, err)
		}

		want := objectcontext.OwnerSet(got.Fields)
		if rows := ownerRows(t, raw); !slices.Equal(rows, want) {
			t.Errorf("owner rows after %v = %v, want %v", owners, rows, want)
		}
	}
}

// ownerRows returns the owners in the owner index, sorted.
func ownerRows(t *testing.T, conn *sql.DB) []string {
	t.Helper()

	rows, err := conn.QueryContext(t.Context(), "SELECT owner FROM object_context_owners ORDER BY owner")
	if err != nil {
		t.Fatalf("select owner rows: %v", err)
	}

	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()

	var out []string

	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			t.Fatalf("scan owner row: %v", err)
		}

		out = append(out, owner)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("read owner rows: %v", err)
	}

	return out
}
