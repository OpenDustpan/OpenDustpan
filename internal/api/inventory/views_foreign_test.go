package inventory_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// foreignViewID is the ID of the view that the store serves in this file. It
// has the form of a view ID, and the store holds no row for it.
const foreignViewID = savedview.ID("view-00000000000000000000000000000001")

// viewOfOtherNamespace is a saved view store with a bug on purpose: its Get
// serves a view whose query holds the references refs, whatever their
// namespace. It lets a test send a stored reference of another namespace
// straight to the filter, as if the check of the store on read had failed.
type viewOfOtherNamespace struct {
	savedview.Store

	refs []string
}

func (s viewOfOtherNamespace) Get(
	_ context.Context, ns namespace.Name, id savedview.ID, owner string,
) (savedview.View, error) {
	return savedview.View{
		CreatedAt: time.UnixMilli(1_800_000_000_000), ID: id, Namespace: ns, Name: "foreign",
		Owner: owner, Query: savedview.Query{Labels: nil, References: s.refs},
	}, nil
}

// queryReader is an inventory reader that records the store query of each
// QueryUnits call.
type queryReader struct {
	*memReader

	mu      sync.Mutex
	queries []coreinventory.UnitQuery
}

func newQueryReader() *queryReader {
	return &queryReader{
		memReader: &memReader{entries: map[namespace.Name][]coreinventory.Entry{"acme": labeledEntries()}, err: nil},
		mu:        sync.Mutex{},
		queries:   nil,
	}
}

func (r *queryReader) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	r.mu.Lock()
	r.queries = append(r.queries, q)
	r.mu.Unlock()

	return r.memReader.QueryUnits(ctx, q)
}

// lastQuery returns the last store query of the unit list.
func (r *queryReader) lastQuery(t *testing.T) coreinventory.UnitQuery {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.queries) == 0 {
		t.Fatal("the unit list made no store query")
	}

	return r.queries[len(r.queries)-1]
}

// viewForeignCase is one stored query with references, and the result that the
// query must give.
type viewForeignCase struct {
	name string
	refs []string
	// want is the set of the unit names that the store query selects.
	want []coreinventory.UnitName
	// answer is the set of the references that the answer lists.
	answer []string
}

// viewForeignCases returns the cases of TestViewUnitsDropsReferencesOfOtherNamespaces.
func viewForeignCases() []viewForeignCase {
	webDev := coreinventory.UnitName{Tool: "pulumi", Name: "web/dev"}
	webProd := coreinventory.UnitName{Tool: "pulumi", Name: "web/prod"}

	return []viewForeignCase{
		{"only a foreign reference", []string{"pulumi:other/web/dev"}, nil, nil},
		{
			"a foreign and a local reference",
			[]string{"pulumi:other/web/dev", "pulumi:acme/web/prod"},
			[]coreinventory.UnitName{webProd},
			[]string{"pulumi:acme/web/prod"},
		},
		{
			"a local reference",
			[]string{"pulumi:acme/web/dev"},
			[]coreinventory.UnitName{webDev},
			[]string{"pulumi:acme/web/dev"},
		},
	}
}

// TestViewUnitsDropsReferencesOfOtherNamespaces proves that a stored view
// whose query holds a reference of another namespace never selects a unit of
// the namespace of the path with the same name: the store query selects no
// unit for it, and the answer lists no such unit.
func TestViewUnitsDropsReferencesOfOtherNamespaces(t *testing.T) {
	t.Parallel()

	for _, tc := range viewForeignCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkViewForeignReferences(t, tc)
		})
	}
}

// checkViewForeignReferences reads the units of the view that the misbehaving
// store serves, and checks the store query and the answer.
func checkViewForeignReferences(t *testing.T, tc viewForeignCase) {
	t.Helper()

	reader := newQueryReader()
	f := newViewFixtureWith(t, viewFixtureOptions{
		views: func(inner savedview.Store) savedview.Store {
			return viewOfOtherNamespace{Store: inner, refs: tc.refs}
		},
		units:         func(coreinventory.Reader) coreinventory.Reader { return reader },
		noIdempotency: false,
	})

	res := f.do(t, http.MethodGet, viewsPath+"/"+string(foreignViewID)+"/units", tokenAna, "")
	if res.status != http.StatusOK {
		t.Fatalf("units = %d (%s), want 200", res.status, res.body)
	}

	var page pageBody
	if err := json.Unmarshal([]byte(res.body), &page); err != nil {
		t.Fatalf("decode units: %v (%s)", err, res.body)
	}

	answer := make([]string, 0, len(page.Units))
	for i := range page.Units {
		answer = append(answer, page.Units[i].Reference)
	}

	if !slices.Equal(answer, tc.answer) {
		t.Errorf("answer = %v, want %v", answer, tc.answer)
	}

	q := reader.lastQuery(t)
	if !q.RestrictUnits {
		t.Error("the store query does not restrict the units")
	}

	if !slices.Equal(q.Units, tc.want) {
		t.Errorf("the store query selects %v, want %v", q.Units, tc.want)
	}
}
