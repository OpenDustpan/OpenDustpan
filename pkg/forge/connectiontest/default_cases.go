package connectiontest

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func defaultCases() []suiteCase {
	return []suiteCase{
		{"the first connection of a namespace is the default and the next is not", testFirstIsDefault},
		{"the default flag of a record that create gets is ignored", testDefaultFlagIgnored},
		{"each namespace has its own default", testDefaultPerNamespace},
		{"delete of the default while another connection exists is refused", testDeleteDefaultRefused},
		{"delete of the only connection is allowed and the next one is the default", testDeleteOnlyConnection},
		{"delete of a connection that is not the default is allowed", testDeleteNonDefault},
		{"make default moves the flag in one step and bumps the version", testMakeDefault},
		{"concurrent make default calls leave one default and one entry each", testMakeDefaultConcurrent},
		{"make default of the default writes no entry", testMakeDefaultSame},
		{"make default of an unknown ID is not found", testMakeDefaultUnknown},
		{"make default with another version is a conflict and changes nothing", testMakeDefaultVersion},
		{"make default with a stale fence changes nothing", testMakeDefaultStaleFence},
	}
}

func testFirstIsDefault(t *testing.T, h Harness) {
	t.Helper()

	first := mustCreate(t, h, nsAcme, "101")
	second := mustCreate(t, h, nsAcme, "102")

	if !first.Default || second.Default {
		t.Fatalf("default flags = %v, %v; want true, false", first.Default, second.Default)
	}

	read, err := h.Store.GetConnection(t.Context(), nsAcme, second.ID)
	if err != nil || read.Default {
		t.Fatalf("get second = %+v, %v; want a connection that is not the default", read, err)
	}
}

func testDefaultFlagIgnored(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, nsAcme, "101")

	rec := newRecord(nsAcme, "102")
	rec.Default = true

	got, err := h.Store.CreateConnection(
		t.Context(), rec, h.Namespace(t, nsAcme.String()), entry(nsAcme, "git-host-connection:create"),
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if got.Default {
		t.Fatal("a second connection is the default because the record said so")
	}

	list, err := h.Store.ListConnections(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	defaults := 0

	for i := range list {
		if list[i].Default {
			defaults++
		}
	}

	if defaults != 1 {
		t.Fatalf("%d default connections, want 1", defaults)
	}
}

func testDefaultPerNamespace(t *testing.T, h Harness) {
	t.Helper()

	a := mustCreate(t, h, nsAcme, "101")
	b := mustCreate(t, h, nsBeta, "102")

	if !a.Default || !b.Default {
		t.Fatalf("default flags = %v, %v; want each namespace to have its own default", a.Default, b.Default)
	}
}

func testDeleteDefaultRefused(t *testing.T, h Harness) {
	t.Helper()

	first := mustCreate(t, h, nsAcme, "101")
	mustCreate(t, h, nsAcme, "102")

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, first.ID, 0, h.Namespace(t, nsAcme.String()), entry(nsAcme, "git-host-connection:delete"),
	)
	if !errors.Is(err, forge.ErrDefaultConnection) {
		t.Fatalf("delete of the default = %v, want ErrDefaultConnection", err)
	}

	if _, err = h.Store.GetConnection(t.Context(), nsAcme, first.ID); err != nil {
		t.Fatalf("the default connection is gone after a refused delete: %v", err)
	}

	const creates = 2

	if n := count(t, h, nsAcme); n != creates {
		t.Fatalf("%d audit entries, want the %d of the creates", n, creates)
	}
}

func testDeleteOnlyConnection(t *testing.T, h Harness) {
	t.Helper()

	only := mustCreate(t, h, nsAcme, "101")

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, only.ID, 0, h.Namespace(t, nsAcme.String()), entry(nsAcme, "git-host-connection:delete"),
	)
	if err != nil {
		t.Fatalf("delete of the only connection: %v", err)
	}

	next := mustCreate(t, h, nsAcme, "102")
	if !next.Default {
		t.Fatal("the first connection after the delete is not the default")
	}
}

func testDeleteNonDefault(t *testing.T, h Harness) {
	t.Helper()

	first := mustCreate(t, h, nsAcme, "101")
	second := mustCreate(t, h, nsAcme, "102")

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, second.ID, 0, h.Namespace(t, nsAcme.String()), entry(nsAcme, "git-host-connection:delete"),
	)
	if err != nil {
		t.Fatalf("delete of a connection that is not the default: %v", err)
	}

	read, err := h.Store.GetConnection(t.Context(), nsAcme, first.ID)
	if err != nil || !read.Default {
		t.Fatalf("get first = %+v, %v; want the default connection", read, err)
	}
}

func testMakeDefault(t *testing.T, h Harness) {
	t.Helper()

	first := mustCreate(t, h, nsAcme, "101")
	second := mustCreate(t, h, nsAcme, "102")
	fence := h.Namespace(t, nsAcme.String())

	got, err := h.Store.MakeDefault(
		t.Context(), nsAcme, second.ID, 0, fence, entry(nsAcme, "git-host-connection:make-default"),
	)
	if err != nil {
		t.Fatalf("make default: %v", err)
	}

	if !got.Default || got.Version != second.Version+1 {
		t.Fatalf("make default = %+v, want the default flag and version %d", got, second.Version+1)
	}

	oldDefault, err := h.Store.GetConnection(t.Context(), nsAcme, first.ID)
	if err != nil || oldDefault.Default || oldDefault.Version != first.Version+1 {
		t.Fatalf("old default = %+v, %v; want no flag and version %d", oldDefault, err, first.Version+1)
	}

	entries := h.Audit(t, nsAcme)
	if len(entries) != 3 || entries[2].Action != "git-host-connection:make-default" {
		t.Fatalf("entries = %+v, want two creates and one make-default", entries)
	}
}

func testMakeDefaultSame(t *testing.T, h Harness) {
	t.Helper()

	first := mustCreate(t, h, nsAcme, "101")

	got, err := h.Store.MakeDefault(
		t.Context(),
		nsAcme,
		first.ID,
		0,
		h.Namespace(t, nsAcme.String()),
		entry(nsAcme, "git-host-connection:make-default"),
	)
	if err != nil || got != first {
		t.Fatalf("make default of the default = %+v, %v; want the same record", got, err)
	}

	if n := count(t, h, nsAcme); n != 1 {
		t.Fatalf("%d audit entries, want only the create", n)
	}
}

func testMakeDefaultUnknown(t *testing.T, h Harness) {
	t.Helper()

	_, err := h.Store.MakeDefault(
		t.Context(), nsAcme, forge.NewConnectionID(), 0, h.Namespace(t, nsAcme.String()),
		entry(nsAcme, "git-host-connection:make-default"),
	)
	if !errors.Is(err, forge.ErrConnectionNotFound) {
		t.Fatalf("make default of an unknown ID = %v, want ErrConnectionNotFound", err)
	}
}

func testMakeDefaultVersion(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, nsAcme, "101")
	second := mustCreate(t, h, nsAcme, "102")

	_, err := h.Store.MakeDefault(
		t.Context(), nsAcme, second.ID, second.Version+1, h.Namespace(t, nsAcme.String()),
		entry(nsAcme, "git-host-connection:make-default"),
	)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("make default with another version = %v, want ErrVersionConflict", err)
	}

	read, err := h.Store.GetConnection(t.Context(), nsAcme, second.ID)
	if err != nil || read.Default {
		t.Fatalf("get second = %+v, %v; want a connection that is still not the default", read, err)
	}
}

func testMakeDefaultStaleFence(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, nsAcme, "101")
	second := mustCreate(t, h, nsAcme, "102")

	_, err := h.Store.MakeDefault(
		t.Context(), nsAcme, second.ID, 0, staleFence(h.Namespace(t, nsAcme.String())),
		entry(nsAcme, "git-host-connection:make-default"),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("make default with a stale fence = %v, want ErrFenced", err)
	}

	read, err := h.Store.GetConnection(t.Context(), nsAcme, second.ID)
	if err != nil || read.Default {
		t.Fatalf("get second = %+v, %v; want a connection that is still not the default", read, err)
	}
}

// testMakeDefaultConcurrent starts one make-default call for each of several
// connections at the same time. Each call must succeed, the namespace must
// end with one default, and the audit chain must hold one entry for each call
// with no repeated sequence number.
func testMakeDefaultConcurrent(t *testing.T, h Harness) {
	t.Helper()

	const callers = 6

	first := mustCreate(t, h, nsAcme, "100")
	ids := make([]string, 0, callers)

	for i := range callers {
		ids = append(ids, mustCreate(t, h, nsAcme, "20"+string(rune('0'+i))).ID)
	}

	fence := h.Namespace(t, nsAcme.String())
	errs := make([]error, callers)

	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, errs[i] = h.Store.MakeDefault(
				t.Context(), nsAcme, id, 0, fence, entry(nsAcme, "git-host-connection:make-default"),
			)
		}()
	}

	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("make default of %s = %v, want no error", ids[i], err)
		}
	}

	list, err := h.Store.ListConnections(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	defaults := 0

	for i := range list {
		if list[i].Default {
			defaults++
		}
	}

	if defaults != 1 {
		t.Fatalf("%d default connections after the calls, want 1 (first was %s)", defaults, first.ID)
	}

	entries := h.Audit(t, nsAcme)
	if want := 1 + callers + callers; len(entries) != want {
		t.Fatalf("%d audit entries, want %d", len(entries), want)
	}

	seen := make(map[int64]bool, len(entries))

	for i := range entries {
		if seen[entries[i].Sequence] {
			t.Fatalf("audit sequence %d appears twice", entries[i].Sequence)
		}

		seen[entries[i].Sequence] = true
	}
}
