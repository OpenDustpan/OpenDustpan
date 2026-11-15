package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func snapshotStore(t *testing.T) (*sqlite.Store, string) {
	t.Helper()

	path := t.TempDir() + "/dustpan.db"

	s, openErr := sqlite.Open(t.Context(), path)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		closeErr := s.Close()
		if closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return s, path
}

func snapshotEntry(at time.Time, ns namespace.Name) audit.Entry {
	return audit.Entry{
		At: at, Namespace: ns, Subject: "admin", IDPSubject: "", TokenID: "",
		Action: "test:setup", Object: ns.String(), ObjectID: "", Version: "",
		ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0,
		Reason: "", PrincipalKind: "", JobID: "", Origin: nil,
	}
}

func seedSnapshotStore(t *testing.T, s *sqlite.Store) (namespace.Fence, string) {
	t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           at,
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	createErr := s.CreateNamespace(t.Context(), ns)
	if createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	for _, name := range []string{"payments", "ledger"} {
		c := collection.Collection{
			CreatedAt: at,
			ID:        ident.NewID("col-"),
			Ref:       collection.Ref{Namespace: ns.Name, Name: collection.Name(name)},
		}
		collectionErr := s.CreateCollection(t.Context(), c, ns.Fence(), snapshotEntry(at, ns.Name))
		if collectionErr != nil {
			t.Fatalf("create collection %s: %v", name, collectionErr)
		}
	}

	ref, refErr := stack.ParseRef("acme", "web", "prod")
	if refErr != nil {
		t.Fatalf("parse stack reference: %v", refErr)
	}

	col, colErr := s.GetCollection(t.Context(), collection.Ref{Namespace: ns.Name, Name: "payments"})
	if colErr != nil {
		t.Fatalf("get collection: %v", colErr)
	}

	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   at,
		UpdatedAt:   at,
		Tags:        map[string]string{},
		Ref:         ref,
		ID:          stack.ID(ident.NewID("stk-")),
		Version:     0,
	}
	placeErr := s.CreateStackPlaced(t.Context(), st, stack.Placement{
		Collection:   col.Ref,
		CollectionID: col.ID,
		Entry:        snapshotEntry(at, ns.Name),
	}, ns.Fence())
	if placeErr != nil {
		t.Fatalf("create stack: %v", placeErr)
	}

	tagErr := s.UpdateStackTags(t.Context(), ref, map[string]string{"team": "core"}, at, ns.Fence())
	if tagErr != nil {
		t.Fatalf("tag stack: %v", tagErr)
	}

	stored, getErr := s.GetStack(t.Context(), ref)
	if getErr != nil {
		t.Fatalf("get stack: %v", getErr)
	}

	del := inventory.Deletion{Entry: snapshotEntry(at, ns.Name), PurgeAfter: at}
	deleteErr := s.ForceDeleteStack(t.Context(), ref, ns.Fence(), del)
	if deleteErr != nil {
		t.Fatalf("delete stack: %v", deleteErr)
	}

	return ns.Fence(), stored.ID.String()
}

// snapshotState is one consistent state of the deleted unit: the collection
// that holds it and the value of its team label.
type snapshotState struct {
	collectionKey string
	tag           string
}

// snapshotStates returns the two states of the deleted unit that a read must
// never mix. The seed writes the first.
func snapshotStates() []snapshotState {
	return []snapshotState{
		{collectionKey: collection.Name("payments").Key(), tag: "core"},
		{collectionKey: collection.Name("ledger").Key(), tag: "ops"},
	}
}

// errWriteRows is the error of a test write that does not change exactly one
// row. A row that is not there fails the test, so the snapshot test never
// passes on an unchanged state.
var errWriteRows = errors.New("no single row changed")

// execOne runs stmt and requires that it changes exactly one row, so a test
// that writes a row fails when the row is not there and never passes on an
// unchanged state.
func execOne(ctx context.Context, tx *sql.Tx, what, stmt string, args ...any) error {
	res, execErr := tx.ExecContext(ctx, stmt, args...)
	if execErr != nil {
		return fmt.Errorf("%s: %w", what, execErr)
	}

	rows, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		return fmt.Errorf("count %s: %w", what, rowsErr)
	}

	if rows != 1 {
		return fmt.Errorf("%s: %w: %d rows", what, errWriteRows, rows)
	}

	return nil
}

// commitSnapshotState writes the collection and the team label of the deleted
// unit in one transaction, so one commit switches both rows together. The
// store serves no move of a deleted unit, so the test writes the two rows
// that the fact read reads. Each write requires its one row, so a missing row
// fails the test instead of leaving the state unchanged.
func commitSnapshotState(t *testing.T, s *sqlite.Store, id string, st snapshotState) error {
	t.Helper()

	ctx := t.Context()

	tx, beginErr := sqlite.DB(s).BeginTx(ctx, nil)
	if beginErr != nil {
		return fmt.Errorf("begin the unit state: %w", beginErr)
	}

	collectionErr := execOne(ctx, tx, "write the unit collection",
		`UPDATE collection_members SET collection_key = ? WHERE unit_id = ?`, st.collectionKey, id)
	if collectionErr != nil {
		return errors.Join(collectionErr, tx.Rollback())
	}

	labelErr := execOne(ctx, tx, "write the unit label",
		`UPDATE stack_tags SET value = ? WHERE stack_id = ? AND name = ?`, st.tag, id, "team")
	if labelErr != nil {
		return errors.Join(labelErr, tx.Rollback())
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit the unit state: %w", commitErr)
	}

	return nil
}

// checkSnapshotFacts reads the facts of the deleted unit and fails t when
// the collection and the labels come from two different commits.
func checkSnapshotFacts(t *testing.T, s *sqlite.Store, fence namespace.Fence, id string) {
	t.Helper()

	got, factsErr := s.GetDeletedUnitFacts(t.Context(), fence.Namespace, "pulumi", id)
	if factsErr != nil {
		t.Fatalf("GetDeletedUnitFacts() = %v", factsErr)
	}

	switch got.Collection {
	case "payments":
		if !reflect.DeepEqual(got.Labels, map[string]string{"team": "core"}) {
			t.Fatalf("collection = payments with labels %v, want one commit team=core", got.Labels)
		}
	case "ledger":
		if !reflect.DeepEqual(got.Labels, map[string]string{"team": "ops"}) {
			t.Fatalf("collection = ledger with labels %v, want one commit team=ops", got.Labels)
		}
	default:
		t.Fatalf("GetDeletedUnitFacts().Collection = %q, want payments or ledger", got.Collection)
	}
}

// TestDeletedUnitFactsSnapshot reads the facts of a deleted unit while
// another connection commits a change of both the collection and the labels.
// Every read sees the facts of one commit: a collection from before a commit
// never comes back with labels from after it, because the call reads in one
// read-only transaction.
//
// The hook commits the next state between the collection read and the labels
// read of every call, and the test counts the commit. A commit therefore runs
// while every read runs, and the test fails when a read saw no commit between
// its reads.
func TestDeletedUnitFactsSnapshot(t *testing.T) {
	t.Parallel()

	s, _ := snapshotStore(t)
	fence, id := seedSnapshotStore(t, s)

	states := snapshotStates()

	committed := 0

	sqlite.SetBetweenDeletedFactsReads(s, func(context.Context) {
		if err := commitSnapshotState(t, s, id, states[(committed+1)%len(states)]); err != nil {
			t.Errorf("commit the unit state between the reads: %v", err)

			return
		}

		committed++
	})

	for i := range 50 {
		before := committed

		checkSnapshotFacts(t, s, fence, id)

		if committed != before+1 {
			t.Fatalf("read %d saw %d commits between its reads, want one commit in flight during every read",
				i, committed-before)
		}
	}
}

// TestDeletedUnitFactsReadFailure forces the read-only transaction to fail
// and reads an unknown unit: the call returns no partial facts, only the zero
// value and the store error. A canceled context fails the begin; an unknown
// unit fails closed on no rows.
func TestDeletedUnitFactsReadFailure(t *testing.T) {
	t.Parallel()

	s, _ := snapshotStore(t)
	fence, id := seedSnapshotStore(t, s)

	cases := []struct {
		name     string
		canceled bool
		id       string
		want     error
	}{
		{"canceled context", true, id, context.Canceled},
		{"unknown unit", false, "unknown-id", inventory.ErrNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tc.canceled {
				canceled, cancel := context.WithCancel(ctx)
				cancel()

				ctx = canceled
			}

			got, factsErr := s.GetDeletedUnitFacts(ctx, fence.Namespace, "pulumi", tc.id)
			if !errors.Is(factsErr, tc.want) {
				t.Fatalf("GetDeletedUnitFacts() = %v, want %v", factsErr, tc.want)
			}

			if !reflect.DeepEqual(got, inventory.DeletedFacts{Collection: "", Labels: nil}) {
				t.Fatalf("GetDeletedUnitFacts() = %+v, want zero facts on failure", got)
			}
		})
	}
}

// TestDeletedUnitFactsSelectFailure breaks the labels table behind the store,
// so the select of the facts fails: the call closes the transaction and
// returns the select error, and the store stays usable.
func TestDeletedUnitFactsSelectFailure(t *testing.T) {
	t.Parallel()

	s, path := snapshotStore(t)
	fence, id := seedSnapshotStore(t, s)

	raw, rawErr := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if rawErr != nil {
		t.Fatalf("open the database file: %v", rawErr)
	}

	_, dropErr := raw.ExecContext(t.Context(), `DROP TABLE stack_tags`)
	if dropErr != nil {
		t.Fatalf("drop the labels table: %v", dropErr)
	}

	rawCloseErr := raw.Close()
	if rawCloseErr != nil {
		t.Fatalf("close the database file: %v", rawCloseErr)
	}

	got, factsErr := s.GetDeletedUnitFacts(t.Context(), fence.Namespace, "pulumi", id)
	if factsErr == nil || !strings.Contains(factsErr.Error(), "select labels of deleted unit") {
		t.Fatalf("GetDeletedUnitFacts() = %+v, %v, want the select error", got, factsErr)
	}

	if !reflect.DeepEqual(got, inventory.DeletedFacts{Collection: "", Labels: nil}) {
		t.Fatalf("GetDeletedUnitFacts() = %+v, want zero facts on failure", got)
	}

	_, againErr := s.GetDeletedUnitFacts(t.Context(), fence.Namespace, "pulumi", id)
	if againErr == nil {
		t.Fatal("GetDeletedUnitFacts() again = nil, want the select error")
	}

	_, nsErr := s.GetNamespace(t.Context(), fence.Namespace)
	if nsErr != nil {
		t.Fatalf("GetNamespace() after failure = %v, want the transaction closed", nsErr)
	}
}
