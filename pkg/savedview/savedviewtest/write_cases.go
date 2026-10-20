package savedviewtest

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// entriesAfterDelete is the number of audit entries after a create and a delete.
const entriesAfterDelete = 2

func writeCases() []suiteCase {
	return []suiteCase{
		{name: "create with a stale fence returns ErrFenced and stores nothing", test: testCreateStaleFence},
		{name: "create with the fence of another namespace returns ErrFenced", test: testCreateOtherNamespaceFence},
		{name: "delete with a stale fence returns ErrFenced and keeps the view", test: testDeleteStaleFence},
		{name: "create appends one audit entry in the same transaction", test: testCreateAudit},
		{name: "a create that fails appends no audit entry", test: testFailedCreateNoAudit},
		{name: "delete appends one audit entry in the same transaction", test: testDeleteAudit},
		{name: "delete of an unknown view returns not found and appends no audit entry", test: testDeleteUnknown},
		{name: "concurrent creates of one name store one view", test: testConcurrentSameName},
		{name: "concurrent creates at the limit store no more than the limit", test: testConcurrentLimit},
		{
			name: "cleanup of the views of a principal deletes them and audits each view by ID only",
			test: testCleanup,
		},
		{name: "cleanup with a stale fence returns ErrFenced and keeps the views", test: testCleanupStaleFence},
		{name: "cleanup of an owner with no view deletes nothing and appends no audit entry", test: testCleanupNone},
		{name: "cleanup with an owner that is not a principal ID deletes nothing", test: testCleanupBadOwner},
	}
}

func testCreateStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := staleFence(h.Namespace(t, "acme"))

	err := h.Store.Create(t.Context(), newView("acme", ownerA, "Payments"), fence, auditEntry("acme", "create"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence = %v, want ErrFenced", err)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 0 {
		t.Fatalf("list = %v, %v; want none", views, err)
	}

	if got := auditCount(t, h); got != 0 {
		t.Fatalf("audit entries = %d, want 0", got)
	}
}

func testCreateOtherNamespaceFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	beta := h.Namespace(t, "beta")

	err := h.Store.Create(t.Context(), newView("acme", ownerA, "Payments"), beta, auditEntry("acme", "create"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with the fence of another namespace = %v, want ErrFenced", err)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 0 {
		t.Fatalf("list = %v, %v; want none", views, err)
	}
}

func testDeleteStaleFence(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	stale := staleFence(h.Namespace(t, "acme"))

	err := h.Store.Delete(t.Context(), "acme", v.ID, ownerA, stale, auditEntry("acme", "delete"))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete with a stale fence = %v, want ErrFenced", err)
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view after the fenced delete: %v", err)
	}

	if got := auditCount(t, h); got != 1 {
		t.Fatalf("audit entries = %d, want 1 (the create)", got)
	}
}

func testCreateAudit(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	fence := h.Namespace(t, "acme")
	e := auditEntry("acme", "create")
	e.ObjectID = string(v.ID)

	if err := h.Store.Create(t.Context(), v, fence, e); err != nil {
		t.Fatalf("create: %v", err)
	}

	entries := h.Audit(t, "acme")
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}

	if entries[0].Action != "view:create" || entries[0].ObjectID != string(v.ID) {
		t.Fatalf("audit entry = %+v", entries[0])
	}

	// An entry that fails validation fails the create, and nothing is stored.
	bad := auditEntry("acme", "create")
	bad.Subject = ""

	err := h.Store.Create(t.Context(), newView("acme", ownerA, "Other"), fence, bad)
	if err == nil {
		t.Fatal("create with an invalid audit entry succeeded")
	}

	views, listErr := h.Store.List(t.Context(), "acme", ownerA)
	if listErr != nil || len(views) != 1 {
		t.Fatalf("list = %d views, %v; want 1", len(views), listErr)
	}
}

func testFailedCreateNoAudit(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, newView("acme", ownerA, "Payments"))

	fence := h.Namespace(t, "acme")

	failures := map[string]struct {
		view  savedview.View
		fence namespace.Fence
		want  error
	}{
		"same name":     {newView("acme", ownerA, "Payments"), fence, savedview.ErrExists},
		"invalid":       {newView("acme", ownerA, ""), fence, savedview.ErrInvalid},
		"stale fence":   {newView("acme", ownerA, "Other"), staleFence(fence), namespace.ErrFenced},
		"other fence":   {newView("acme", ownerA, "Other"), h.Namespace(t, "beta"), namespace.ErrFenced},
		"owner is name": {newView("acme", subjectAlice, "Other"), fence, savedview.ErrInvalid},
	}

	for name := range failures {
		tc := failures[name]

		err := h.Store.Create(t.Context(), tc.view, tc.fence, auditEntry("acme", "create"))
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: create = %v, want %v", name, err, tc.want)
		}
	}

	if got := auditCount(t, h); got != 1 {
		t.Fatalf("audit entries = %d, want 1: a failed create appends none", got)
	}
}

func testDeleteAudit(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	e := auditEntry("acme", "delete")
	e.ObjectID = string(v.ID)

	if err := h.Store.Delete(t.Context(), "acme", v.ID, ownerA, h.Namespace(t, "acme"), e); err != nil {
		t.Fatalf("delete: %v", err)
	}

	entries := h.Audit(t, "acme")
	if len(entries) != 2 || entries[1].Action != "view:delete" || entries[1].ObjectID != string(v.ID) {
		t.Fatalf("audit entries = %+v; want the create and the delete", entries)
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}

	// A second delete finds nothing and appends nothing.
	err := h.Store.Delete(t.Context(), "acme", v.ID, ownerA, h.Namespace(t, "acme"), e)
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}

	if got := auditCount(t, h); got != entriesAfterDelete {
		t.Fatalf("audit entries = %d, want %d", got, entriesAfterDelete)
	}
}

func testDeleteUnknown(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")

	err := h.Store.Delete(t.Context(), "acme", savedview.NewID(), ownerA, fence, auditEntry("acme", "delete"))
	if !errors.Is(err, savedview.ErrNotFound) {
		t.Fatalf("delete of an unknown view = %v, want ErrNotFound", err)
	}

	if got := auditCount(t, h); got != 0 {
		t.Fatalf("audit entries = %d, want 0", got)
	}
}

func testConcurrentSameName(t *testing.T, h Harness) {
	t.Helper()

	const callers = 8

	fence := h.Namespace(t, "acme")
	results := make(chan error, callers)

	var wg sync.WaitGroup

	for range callers {
		wg.Go(func() {
			results <- h.Store.Create(t.Context(), newView("acme", ownerA, "Payments"), fence, auditEntry("acme", "create"))
		})
	}

	wg.Wait()
	close(results)

	stored := 0

	for err := range results {
		switch {
		case err == nil:
			stored++
		case errors.Is(err, savedview.ErrExists):
		default:
			t.Errorf("concurrent create = %v, want nil or ErrExists", err)
		}
	}

	if stored != 1 {
		t.Fatalf("%d creates succeeded, want 1", stored)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != 1 {
		t.Fatalf("list = %d views, %v; want 1", len(views), err)
	}

	if got := auditCount(t, h); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}
}

func testConcurrentLimit(t *testing.T, h Harness) {
	t.Helper()

	const (
		seeded  = savedview.MaxPerOwner - 10
		callers = 30
	)

	for i := range seeded {
		mustCreate(t, h, newView("acme", ownerA, fmt.Sprintf("seed %03d", i)))
	}

	fence := h.Namespace(t, "acme")
	results := make(chan error, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Go(func() {
			v := newView("acme", ownerA, fmt.Sprintf("race %03d", i))
			results <- h.Store.Create(t.Context(), v, fence, auditEntry("acme", "create"))
		})
	}

	wg.Wait()
	close(results)

	stored := 0

	for err := range results {
		switch {
		case err == nil:
			stored++
		case errors.Is(err, savedview.ErrLimit):
		default:
			t.Errorf("concurrent create = %v, want nil or ErrLimit", err)
		}
	}

	if stored != savedview.MaxPerOwner-seeded {
		t.Fatalf("%d creates succeeded, want %d", stored, savedview.MaxPerOwner-seeded)
	}

	views, err := h.Store.List(t.Context(), "acme", ownerA)
	if err != nil || len(views) != savedview.MaxPerOwner {
		t.Fatalf("list = %d views, %v; want %d", len(views), err, savedview.MaxPerOwner)
	}
}

func testCleanup(t *testing.T, h Harness) {
	t.Helper()

	const privateName = "Reorg of the payments team"

	mine := []savedview.View{
		newView("acme", ownerA, privateName), newView("acme", ownerA, "Second"), newView("acme", ownerA, "Third"),
	}
	keep := []savedview.View{newView("acme", ownerB, "Payments"), newView("beta", ownerA, "Other namespace")}

	for _, list := range [][]savedview.View{mine, keep} {
		for i := range list {
			mustCreate(t, h, list[i])
		}
	}

	before := len(h.Audit(t, "acme"))
	e := auditEntry("acme", "delete")
	e.Subject = "@server"

	n, err := h.Store.DeleteOwned(t.Context(), "acme", ownerA, h.Namespace(t, "acme"), e)
	if err != nil || n != len(mine) {
		t.Fatalf("cleanup = %d, %v; want %d", n, err, len(mine))
	}

	checkCleanedViews(t, h, mine, keep)
	checkCleanupEntries(t, h.Audit(t, "acme")[before:], mine, privateName)
}

// checkCleanedViews checks that the views in gone are gone and the views in
// kept stay.
func checkCleanedViews(t *testing.T, h Harness, gone, kept []savedview.View) {
	t.Helper()

	for i := range gone {
		if _, err := h.Store.Get(t.Context(), "acme", gone[i].ID, ownerA); !errors.Is(err, savedview.ErrNotFound) {
			t.Errorf("get %s after cleanup = %v, want ErrNotFound", gone[i].ID, err)
		}
	}

	for i := range kept {
		if _, err := h.Store.Get(t.Context(), kept[i].Namespace, kept[i].ID, kept[i].Owner); err != nil {
			t.Errorf("view %s of another owner or namespace after cleanup: %v", kept[i].ID, err)
		}
	}
}

// checkCleanupEntries checks that the cleanup appended one entry for each view
// in gone, with the view ID and without the name.
func checkCleanupEntries(t *testing.T, added []audit.Entry, gone []savedview.View, name string) {
	t.Helper()

	if len(added) != len(gone) {
		t.Fatalf("cleanup appended %d audit entries, want %d (one for each view)", len(added), len(gone))
	}

	seen := map[string]bool{}

	for i := range added {
		seen[added[i].ObjectID] = true

		assertEntryHoldsNoName(t, added[i], name)
	}

	for i := range gone {
		if !seen[string(gone[i].ID)] {
			t.Errorf("no audit entry for the view %s", gone[i].ID)
		}
	}
}

// assertEntryHoldsNoName fails when any text field of e holds name.
func assertEntryHoldsNoName(t *testing.T, e audit.Entry, name string) {
	t.Helper()

	if e.Action != "view:delete" || e.ObjectID == "" || len(e.Changes) != 0 ||
		strings.Contains(fmt.Sprintf("%+v", e), name) {
		t.Fatalf("audit entry of a cleanup = %+v: want the action, the view ID, no change and no name", e)
	}
}

func testCleanupStaleFence(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	stale := staleFence(h.Namespace(t, "acme"))

	n, err := h.Store.DeleteOwned(t.Context(), "acme", ownerA, stale, auditEntry("acme", "delete"))
	if !errors.Is(err, namespace.ErrFenced) || n != 0 {
		t.Fatalf("cleanup with a stale fence = %d, %v; want 0 and ErrFenced", n, err)
	}

	other := h.Namespace(t, "beta")

	n, err = h.Store.DeleteOwned(t.Context(), "acme", ownerA, other, auditEntry("acme", "delete"))
	if !errors.Is(err, namespace.ErrFenced) || n != 0 {
		t.Fatalf("cleanup with the fence of another namespace = %d, %v; want 0 and ErrFenced", n, err)
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view after the fenced cleanup: %v", err)
	}
}

func testCleanupNone(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, newView("acme", ownerB, "Payments"))

	before := auditCount(t, h)

	n, err := h.Store.DeleteOwned(t.Context(), "acme", ownerA, h.Namespace(t, "acme"), auditEntry("acme", "delete"))
	if err != nil || n != 0 {
		t.Fatalf("cleanup = %d, %v; want 0 and no error", n, err)
	}

	if got := auditCount(t, h); got != before {
		t.Fatalf("audit entries = %d, want %d", got, before)
	}
}

// testCleanupBadOwner calls the cleanup with owners that are not a principal
// ID. An empty owner or a LIKE pattern must not match every view.
func testCleanupBadOwner(t *testing.T, h Harness) {
	t.Helper()

	v := newView("acme", ownerA, "Payments")
	mustCreate(t, h, v)

	fence := h.Namespace(t, "acme")

	for _, owner := range []string{"", "prn-%", "%", subjectAlice, "' OR '1'='1"} {
		n, err := h.Store.DeleteOwned(t.Context(), "acme", owner, fence, auditEntry("acme", "delete"))
		if n != 0 || err == nil {
			t.Errorf("cleanup of owner %q = %d, %v; want 0 and an error", owner, n, err)
		}
	}

	if _, err := h.Store.Get(t.Context(), "acme", v.ID, ownerA); err != nil {
		t.Fatalf("view after the hostile cleanups: %v", err)
	}
}
