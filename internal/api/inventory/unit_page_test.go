package inventory_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// recordingReader is an inventory reader over memReader that records each
// unit query, and that can answer a page of its own instead of the page of
// inventory.PageUnits.
type recordingReader struct {
	*memReader

	// page, when set, answers each query from every entry of the namespace.
	page    func(all []coreinventory.Entry, q coreinventory.UnitQuery) []coreinventory.Entry
	mu      sync.Mutex
	queries []coreinventory.UnitQuery
}

func (r *recordingReader) QueryUnits(ctx context.Context, q coreinventory.UnitQuery) ([]coreinventory.Entry, error) {
	r.mu.Lock()
	r.queries = append(r.queries, q)
	r.mu.Unlock()

	if r.page == nil {
		return r.memReader.QueryUnits(ctx, q)
	}

	all, err := r.ListUnits(ctx, q.Namespace)
	if err != nil {
		return nil, err
	}

	return r.page(all, q), nil
}

// recorded returns a copy of the queries that the reader got.
func (r *recordingReader) recorded() []coreinventory.UnitQuery {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.queries)
}

// manyUnits returns a reader of n Pulumi stacks of acme, unit-000 and up,
// the even ones with the label team=a, and a handler over it with authz.
func manyUnits(t *testing.T, n int, authz identity.Authorizer) (*recordingReader, *apiinventory.Handler) {
	t.Helper()

	nsStore := &memNamespaceStore{namespaces: map[namespace.Name]namespace.Namespace{}}
	mem := &memReader{entries: map[namespace.Name][]coreinventory.Entry{}, err: nil}
	seedEntries(nsStore, mem)

	ns := namespace.Name("acme")
	entries := make([]coreinventory.Entry, 0, n)

	for i := range n {
		labels := map[string]string{}
		if i%2 == 0 {
			labels["team"] = "a"
		}

		entries = append(entries, coreinventory.Entry{
			ManualLock: nil, Collection: "", LastChanged: changeTime(), Namespace: ns, Tool: "pulumi",
			Name: fmt.Sprintf("web/s%03d", i), UnitID: fmt.Sprintf("unit-%03d", i),
			DisplayName: fmt.Sprintf("web/s%03d", i), Status: "", ResourceCount: nil, Lock: nil, Labels: labels,
		})
	}

	mem.entries[ns] = entries

	reader := &recordingReader{memReader: mem, page: nil, mu: sync.Mutex{}, queries: nil}

	return reader, newBehaviorHandlerWith(t, nsStore, reader, authz, behaviorWiring{
		sessions: nil, lifecycles: testLifecycles(), publicURL: "",
	})
}

// everyFifth allows unit:read on the stacks whose number is a multiple of 5,
// and every other action.
type everyFifth struct{}

func (everyFifth) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if action != identity.ActionUnitRead {
		return nil
	}

	var n int

	_, err := fmt.Sscanf(obj.UnitName(), "web/s%03d", &n)
	if err != nil || n%5 != 0 {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func (everyFifth) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// TestUnitListReadsMoreStorePages proves that a caller who may read few units
// gets a full page: the handler reads more store pages, each one larger than
// the one before up to the cap, until the page is full.
func TestUnitListReadsMoreStorePages(t *testing.T) {
	t.Parallel()

	reader, handler := manyUnits(t, 200, everyFifth{})

	status, page := getPageFrom(t, handler, "acme", "pageSize=4")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	want := []string{"unit-000", "unit-005", "unit-010", "unit-015"}
	if got := unitIDsOf(page.Units); !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}

	if !page.hasNext {
		t.Fatal("page has no nextPageToken, want one: unit-020 is readable")
	}

	recorded := reader.recorded()

	limits := make([]int, 0, len(recorded))
	for _, q := range recorded {
		limits = append(limits, q.Limit)
	}

	if wantLimits := []int{5, 10, 20}; !slices.Equal(limits, wantLimits) {
		t.Fatalf("store limits = %v, want %v", limits, want)
	}
}

// TestUnitListSendsFilterToStore proves that the store gets the filters of
// the request, the page size plus one, and the cursor of the token.
func TestUnitListSendsFilterToStore(t *testing.T) {
	t.Parallel()

	reader, handler := manyUnits(t, 10, selectiveAuthorizer{deny: nil})

	query := "label=team%3Da&reference=pulumi%3Aacme%2Fweb%2Fs002&reference=pulumi%3Aacme%2Fweb%2Fs004" +
		"&state=locked&pageSize=1"

	if status, _ := getPageFrom(t, handler, "acme", query); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	got := reader.recorded()
	if len(got) != 1 {
		t.Fatalf("store queries = %d, want 1", len(got))
	}

	q := got[0]
	units := slices.Clone(q.Units)
	slices.SortFunc(units, func(a, b coreinventory.UnitName) int { return strings.Compare(a.Name, b.Name) })

	wantUnits := []coreinventory.UnitName{{Tool: "pulumi", Name: "web/s002"}, {Tool: "pulumi", Name: "web/s004"}}
	if q.Namespace != "acme" || q.After != "" || q.Limit != 2 ||
		!slices.Equal(q.Labels, []coreinventory.UnitLabel{{Key: "team", Value: "a"}}) ||
		!q.RestrictUnits || !slices.Equal(units, wantUnits) ||
		!q.RestrictStates || !slices.Equal(q.States, []coreinventory.State{coreinventory.StateLocked}) {
		t.Fatalf("store query = %+v, want the filters of the request", q)
	}
}

// TestUnitListRechecksStoreFilter proves the check of the filter in the
// handler alone: a store that ignores the label filter answers every unit,
// and the page still holds only the units with the label.
func TestUnitListRechecksStoreFilter(t *testing.T) {
	t.Parallel()

	reader, handler := manyUnits(t, 10, selectiveAuthorizer{deny: nil})
	reader.page = func(all []coreinventory.Entry, q coreinventory.UnitQuery) []coreinventory.Entry {
		q.Labels = nil

		return coreinventory.PageUnits(all, q)
	}

	status, page := getPageFrom(t, handler, "acme", "label=team%3Da")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	want := []string{"unit-000", "unit-002", "unit-004", "unit-006", "unit-008"}
	if got := unitIDsOf(page.Units); !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
}

// TestUnitListRechecksStoreAuthorization proves that the handler checks
// unit:read on each unit that the store answers: the store does not know the
// caller, and the page holds only the units that the caller may read.
func TestUnitListRechecksStoreAuthorization(t *testing.T) {
	t.Parallel()

	_, handler := manyUnits(t, 12, everyFifth{})

	status, page := getPageFrom(t, handler, "acme", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	if got, want := unitIDsOf(page.Units), []string{"unit-000", "unit-005", "unit-010"}; !slices.Equal(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
}

// TestUnitListRefusesStoreOutOfOrder proves the check of the order in the
// handler alone: a store that ignores the cursor and answers the same full
// page again fails the request with 500, and the loop ends.
func TestUnitListRefusesStoreOutOfOrder(t *testing.T) {
	t.Parallel()

	reader, handler := manyUnits(t, 20, everyFifth{})
	reader.page = func(all []coreinventory.Entry, q coreinventory.UnitQuery) []coreinventory.Entry {
		q.After = ""

		return coreinventory.PageUnits(all, q)
	}

	if status, _ := getPageFrom(t, handler, "acme", "pageSize=3"); status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}

	if n := len(reader.recorded()); n != 2 {
		t.Fatalf("store queries = %d, want 2: the second answer repeats a unit", n)
	}
}
