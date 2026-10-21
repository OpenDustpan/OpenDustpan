package connectiontest

import (
	"errors"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// firstBulkInstallation is the installation ID of the first connection that the
// limit case makes. The next ones count up from it.
const firstBulkInstallation = 1000

func connectionCases() []suiteCase {
	return []suiteCase{
		{"create stores the connection and get returns it", testCreateAndGet},
		{"create writes one audit entry in the namespace", testCreateAudit},
		{"create of an invalid record is invalid and writes nothing", testCreateInvalid},
		{"create of a second connection to one installation is refused", testCreateDuplicateInstallation},
		{"create above the limit of a namespace is refused", testCreateLimit},
		{"create with a stale fence changes nothing", testCreateStaleFence},
		{"create with the fence of another namespace changes nothing", testCreateOtherFence},
		{"create with an entry of another scope changes nothing", testCreateOtherEntry},
		{"get of an unknown ID is not found", testGetUnknown},
		{"a connection is not visible in another namespace", testGetOtherNamespace},
		{"list returns the connections of the namespace in order", testList},
		{"delete removes the connection and writes one entry", testDelete},
		{"delete of an unknown ID is not found", testDeleteUnknown},
		{"delete with a stale fence changes nothing", testDeleteStaleFence},
		{"delete with another version is a conflict and changes nothing", testDeleteVersion},
	}
}

func testCreateAndGet(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")

	read, err := h.Store.GetConnection(t.Context(), nsAcme, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if read != got {
		t.Fatalf("get = %+v, want the stored record %+v", read, got)
	}

	if read.InstallationID != "101" || read.Kind != forge.KindGitHub || read.Auth != forge.AuthApp ||
		read.Scope != forge.AppScopeInstallation || read.Version != 1 {
		t.Fatalf("get = %+v: wrong fields", read)
	}
}

func testCreateAudit(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")

	entries := h.Audit(t, nsAcme)
	if len(entries) != 1 || entries[0].Action != "git-host-connection:create" {
		t.Fatalf("entries = %+v, want one create entry", entries)
	}

	if got.ID == "" {
		t.Fatal("the stored record has no ID")
	}
}

func testCreateInvalid(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme.String())

	for name, edit := range map[string]func(c *forge.ConnectionRecord){
		"a bad ID":              func(c *forge.ConnectionRecord) { c.ID = "x" },
		"a bad installation":    func(c *forge.ConnectionRecord) { c.InstallationID = "12a" },
		"another kind":          func(c *forge.ConnectionRecord) { c.Kind = forge.KindGitLab },
		"the token auth kind":   func(c *forge.ConnectionRecord) { c.Auth = forge.AuthToken },
		"no version":            func(c *forge.ConnectionRecord) { c.Version = 0 },
		"an empty installation": func(c *forge.ConnectionRecord) { c.InstallationID = "" },
	} {
		rec := newRecord(nsAcme, "101")
		edit(&rec)

		_, err := h.Store.CreateConnection(t.Context(), rec, fence, entry(nsAcme, "git-host-connection:create"))
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("create with %s = %v, want ErrInvalid", name, err)
		}
	}

	if n := count(t, h, nsAcme); n != 0 {
		t.Fatalf("%d audit entries after refused creates, want 0", n)
	}
}

func testCreateDuplicateInstallation(t *testing.T, h Harness) {
	t.Helper()

	mustCreate(t, h, nsAcme, "101")

	fence := h.Namespace(t, nsAcme.String())

	_, err := h.Store.CreateConnection(
		t.Context(), newRecord(nsAcme, "101"), fence, entry(nsAcme, "git-host-connection:create"),
	)
	if !errors.Is(err, forge.ErrConnectionExists) {
		t.Fatalf("second connection to installation 101 = %v, want ErrConnectionExists", err)
	}

	// Another namespace may hold a connection to the same number: the map, not
	// the store, decides who may use an installation.
	h.Namespace(t, nsBeta.String())

	_, err = h.Store.CreateConnection(
		t.Context(), newRecord(nsBeta, "101"), h.Namespace(t, nsBeta.String()),
		entry(nsBeta, "git-host-connection:create"),
	)
	if err != nil {
		t.Fatalf("connection to installation 101 in another namespace: %v", err)
	}

	if n := count(t, h, nsAcme); n != 1 {
		t.Fatalf("%d audit entries in acme, want 1", n)
	}
}

func testCreateLimit(t *testing.T, h Harness) {
	t.Helper()

	for i := range forge.MaxConnectionsPerNamespace {
		mustCreate(t, h, nsAcme, strconv.Itoa(firstBulkInstallation+i))
	}

	fence := h.Namespace(t, nsAcme.String())

	_, err := h.Store.CreateConnection(
		t.Context(), newRecord(nsAcme, "9999"), fence, entry(nsAcme, "git-host-connection:create"),
	)
	if !errors.Is(err, forge.ErrConnectionLimit) {
		t.Fatalf("create above the limit = %v, want ErrConnectionLimit", err)
	}

	list, err := h.Store.ListConnections(t.Context(), nsAcme)
	if err != nil || len(list) != forge.MaxConnectionsPerNamespace {
		t.Fatalf("list = %d connections, %v; want %d", len(list), err, forge.MaxConnectionsPerNamespace)
	}

	if n := count(t, h, nsAcme); n != forge.MaxConnectionsPerNamespace {
		t.Fatalf("%d audit entries, want %d", n, forge.MaxConnectionsPerNamespace)
	}
}

func testCreateStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := staleFence(h.Namespace(t, nsAcme.String()))

	_, err := h.Store.CreateConnection(
		t.Context(), newRecord(nsAcme, "101"), fence, entry(nsAcme, "git-host-connection:create"),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create with a stale fence = %v, want ErrFenced", err)
	}

	assertEmpty(t, h)
}

func testCreateOtherFence(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())
	fence := h.Namespace(t, nsBeta.String())

	_, err := h.Store.CreateConnection(
		t.Context(), newRecord(nsAcme, "101"), fence, entry(nsAcme, "git-host-connection:create"),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("create under the fence of another namespace = %v, want ErrFenced", err)
	}

	assertEmpty(t, h)
}

func testCreateOtherEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme.String())

	for name, scope := range map[string]namespace.Name{
		"the entry of another namespace": nsBeta,
		"the entry of the server scope":  audit.ServerScope,
	} {
		e := entry(scope, "git-host-connection:create")

		_, err := h.Store.CreateConnection(t.Context(), newRecord(nsAcme, "101"), fence, e)
		if !errors.Is(err, audit.ErrInvalidEntry) {
			t.Errorf("create with %s = %v, want ErrInvalidEntry", name, err)
		}
	}

	assertEmpty(t, h)
}

func testGetUnknown(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	_, err := h.Store.GetConnection(t.Context(), nsAcme, forge.NewConnectionID())
	if !errors.Is(err, forge.ErrConnectionNotFound) {
		t.Fatalf("get of an unknown ID = %v, want ErrConnectionNotFound", err)
	}
}

func testGetOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")
	h.Namespace(t, nsBeta.String())

	_, err := h.Store.GetConnection(t.Context(), nsBeta, got.ID)
	if !errors.Is(err, forge.ErrConnectionNotFound) {
		t.Fatalf("get in another namespace = %v, want ErrConnectionNotFound", err)
	}

	list, err := h.Store.ListConnections(t.Context(), nsBeta)
	if err != nil || len(list) != 0 {
		t.Fatalf("list in another namespace = %v, %v; want none", list, err)
	}
}

func testList(t *testing.T, h Harness) {
	t.Helper()

	a := mustCreate(t, h, nsAcme, "101")
	b := mustCreate(t, h, nsAcme, "102")
	mustCreate(t, h, nsBeta, "103")

	list, err := h.Store.ListConnections(t.Context(), nsAcme)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	const want = 2

	if len(list) != want {
		t.Fatalf("list = %d connections, want %d", len(list), want)
	}

	// Equal creation times fall back to the ID, so the order is stable.
	first, second := a, b
	if second.ID < first.ID {
		first, second = second, first
	}

	if list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("list order = %s, %s; want %s, %s", list[0].ID, list[1].ID, first.ID, second.ID)
	}
}

func testDelete(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")
	fence := h.Namespace(t, nsAcme.String())

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, got.ID, 0, fence, entry(nsAcme, "git-host-connection:delete"),
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := h.Store.GetConnection(t.Context(), nsAcme, got.ID); !errors.Is(err, forge.ErrConnectionNotFound) {
		t.Fatalf("get after delete = %v, want ErrConnectionNotFound", err)
	}

	entries := h.Audit(t, nsAcme)
	if len(entries) != 2 || entries[1].Action != "git-host-connection:delete" {
		t.Fatalf("entries = %+v, want create and delete", entries)
	}
}

func testDeleteUnknown(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, nsAcme.String())

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, forge.NewConnectionID(), 0, fence, entry(nsAcme, "git-host-connection:delete"),
	)
	if !errors.Is(err, forge.ErrConnectionNotFound) {
		t.Fatalf("delete of an unknown ID = %v, want ErrConnectionNotFound", err)
	}

	assertEmpty(t, h)
}

func testDeleteStaleFence(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")
	fence := staleFence(h.Namespace(t, nsAcme.String()))

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, got.ID, 0, fence, entry(nsAcme, "git-host-connection:delete"),
	)
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("delete with a stale fence = %v, want ErrFenced", err)
	}

	if _, err = h.Store.GetConnection(t.Context(), nsAcme, got.ID); err != nil {
		t.Fatalf("the connection is gone after a refused delete: %v", err)
	}

	if n := count(t, h, nsAcme); n != 1 {
		t.Fatalf("%d audit entries, want 1", n)
	}
}

func testDeleteVersion(t *testing.T, h Harness) {
	t.Helper()

	got := mustCreate(t, h, nsAcme, "101")
	fence := h.Namespace(t, nsAcme.String())

	err := h.Store.DeleteConnection(
		t.Context(), nsAcme, got.ID, got.Version+1, fence, entry(nsAcme, "git-host-connection:delete"),
	)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("delete with another version = %v, want ErrVersionConflict", err)
	}

	if _, err = h.Store.GetConnection(t.Context(), nsAcme, got.ID); err != nil {
		t.Fatalf("the connection is gone after a refused delete: %v", err)
	}

	err = h.Store.DeleteConnection(
		t.Context(), nsAcme, got.ID, got.Version, fence, entry(nsAcme, "git-host-connection:delete"),
	)
	if err != nil {
		t.Fatalf("delete with the stored version: %v", err)
	}
}

// assertEmpty checks that the namespace acme holds no connection and no audit
// entry.
func assertEmpty(t *testing.T, h Harness) {
	t.Helper()

	list, err := h.Store.ListConnections(t.Context(), nsAcme)
	if err != nil || len(list) != 0 {
		t.Fatalf("list = %v, %v; want no connection", list, err)
	}

	if n := count(t, h, nsAcme); n != 0 {
		t.Fatalf("%d audit entries in %s, want 0", n, nsAcme)
	}
}
