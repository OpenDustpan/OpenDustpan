package connectiontest

import (
	"bytes"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	actionPutRegistration = "git-host-app:put"
	actionAssign          = "git-host-installation:assign"
)

func registrationCases() []suiteCase {
	return []suiteCase{
		{"get of a missing registration is not found", testRegistrationMissing},
		{"put creates the registration with version 1 and get returns it", testRegistrationCreate},
		{"put replaces the registration, keeps the hook ID and bumps the version", testRegistrationReplace},
		{"put with another version is a conflict and changes nothing", testRegistrationVersion},
		{"put with a version for a missing registration is not found", testRegistrationVersionMissing},
		{"put of an invalid registration is invalid and writes nothing", testRegistrationInvalid},
		{"put with an entry of a namespace changes nothing", testRegistrationEntryScope},
		{"put writes one entry in the scope of the server", testRegistrationAudit},
		{"delete removes the registration", testRegistrationDelete},
		{"delete while an installation is assigned is refused", testRegistrationDeleteInUse},
		{"delete of a missing registration is not found", testRegistrationDeleteMissing},
		{"delete with another version is a conflict", testRegistrationDeleteVersion},
	}
}

func assignmentCases() []suiteCase {
	return []suiteCase{
		{"put assigns the installation and get returns the namespace", testAssignmentCreate},
		{"get of an unassigned installation is not found", testAssignmentMissing},
		{"put moves the installation to another namespace and bumps the version", testAssignmentMove},
		{"put to a namespace that does not exist is not found", testAssignmentNoNamespace},
		{"put of an invalid assignment is invalid and writes nothing", testAssignmentInvalid},
		{"put with another version is a conflict and changes nothing", testAssignmentVersion},
		{"put with a version for a missing assignment is not found", testAssignmentVersionMissing},
		{"put with an entry of a namespace changes nothing", testAssignmentEntryScope},
		{"list returns the assignments in numeric order", testAssignmentList},
		{"delete removes the assignment and writes one entry", testAssignmentDelete},
		{"delete of a missing assignment is not found", testAssignmentDeleteMissing},
		{"delete with another version is a conflict", testAssignmentDeleteVersion},
		{
			"an assignment does not need a connection and a connection does not need an assignment",
			testAssignmentIndependent,
		},
	}
}

func put(t *testing.T, h Harness, r forge.Registration, ifVersion int64) (forge.Registration, error) {
	t.Helper()

	return h.Store.PutRegistration(t.Context(), r, ifVersion, entry(audit.ServerScope, actionPutRegistration))
}

func mustPut(t *testing.T, h Harness, r forge.Registration) forge.Registration {
	t.Helper()

	got, err := put(t, h, r, 0)
	if err != nil {
		t.Fatalf("put registration: %v", err)
	}

	return got
}

func testRegistrationMissing(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("get of a missing registration = %v, want ErrNoRegistration", err)
	}
}

func testRegistrationCreate(t *testing.T, h Harness) {
	t.Helper()

	want := newRegistration(secretKeyA)
	got := mustPut(t, h, want)

	if got.Version != 1 {
		t.Fatalf("version = %d, want 1", got.Version)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if !sameRegistration(&read, &want) || read.Version != 1 {
		t.Fatalf("get = %+v, want %+v", read, want)
	}
}

// sameRegistration reports whether a and b hold the same values, apart from the
// version.
func sameRegistration(a, b *forge.Registration) bool {
	return a.AppID == b.AppID && a.BaseURL == b.BaseURL && a.HookID == b.HookID && a.KeySetID == b.KeySetID &&
		a.EventSecretSetID == b.EventSecretSetID && a.AddressDigest == b.AddressDigest &&
		bytes.Equal(a.SealedKey, b.SealedKey) && bytes.Equal(a.SealedEventSecret, b.SealedEventSecret) &&
		a.KeyCreatedAt.Equal(b.KeyCreatedAt) && a.EventSecretCreatedAt.Equal(b.EventSecretCreatedAt)
}

func testRegistrationReplace(t *testing.T, h Harness) {
	t.Helper()

	first := mustPut(t, h, newRegistration(secretKeyA))

	next := newRegistration(secretKeyB)
	next.AppID = "777"
	next.AddressDigest = next.Digest()

	got, err := put(t, h, next, first.Version)
	if err != nil {
		t.Fatalf("replace: %v", err)
	}

	if got.Version != 2 || got.HookID != first.HookID {
		t.Fatalf("replace = %+v, want version 2 and the hook ID %s", got, first.HookID)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || read.AppID != "777" || !bytes.Equal(read.SealedKey, []byte(secretKeyB)) ||
		read.HookID != first.HookID {
		t.Fatalf("get = %+v, %v; want the new app ID and key with the old hook ID", read, err)
	}
}

func testRegistrationVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustPut(t, h, newRegistration(secretKeyA))

	if _, err := put(t, h, newRegistration(secretKeyB), first.Version+1); !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("put with another version = %v, want ErrVersionConflict", err)
	}

	read, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub)
	if err != nil || !bytes.Equal(read.SealedKey, []byte(secretKeyA)) || read.Version != 1 {
		t.Fatalf("get = %+v, %v; want the first registration", read, err)
	}

	if n := count(t, h, audit.ServerScope); n != 1 {
		t.Fatalf("%d entries, want 1", n)
	}
}

func testRegistrationVersionMissing(t *testing.T, h Harness) {
	t.Helper()

	if _, err := put(t, h, newRegistration(secretKeyA), 1); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("put with a version for a missing registration = %v, want ErrNoRegistration", err)
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries, want 0", n)
	}
}

func testRegistrationInvalid(t *testing.T, h Harness) {
	t.Helper()

	for name, edit := range map[string]func(r *forge.Registration){
		"a digest of another address": func(r *forge.Registration) {
			r.AddressDigest = forge.AddressDigest(r.Kind, "https://evil.example", r.AppID)
		},
		"a bad app ID":           func(r *forge.Registration) { r.AppID = "x" },
		"no sealed key":          func(r *forge.Registration) { r.SealedKey = nil },
		"no sealed event secret": func(r *forge.Registration) { r.SealedEventSecret = nil },
		"a bad hook ID":          func(r *forge.Registration) { r.HookID = "hook" },
		"another kind":           func(r *forge.Registration) { r.Kind = forge.KindGitLab },
	} {
		r := newRegistration(secretKeyA)
		edit(&r)

		if _, err := put(t, h, r, 0); !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("put with %s = %v, want ErrInvalid", name, err)
		}
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries after refused puts, want 0", n)
	}
}

func testRegistrationEntryScope(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	_, err := h.Store.PutRegistration(
		t.Context(), newRegistration(secretKeyA), 0, entry(nsAcme, actionPutRegistration),
	)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("put with the entry of a namespace = %v, want ErrInvalidEntry", err)
	}

	if _, err = h.Store.GetRegistration(t.Context(), forge.KindGitHub); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("get after a refused put = %v, want ErrNoRegistration", err)
	}
}

func testRegistrationAudit(t *testing.T, h Harness) {
	t.Helper()

	mustPut(t, h, newRegistration(secretKeyA))

	entries := h.Audit(t, audit.ServerScope)
	if len(entries) != 1 || entries[0].Action != actionPutRegistration {
		t.Fatalf("entries = %+v, want one put entry", entries)
	}
}

func testRegistrationDelete(t *testing.T, h Harness) {
	t.Helper()

	mustPut(t, h, newRegistration(secretKeyA))

	err := h.Store.DeleteRegistration(
		t.Context(), forge.KindGitHub, 0, entry(audit.ServerScope, "git-host-app:delete"),
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := h.Store.GetRegistration(t.Context(), forge.KindGitHub); !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("get after delete = %v, want ErrNoRegistration", err)
	}

	const putAndDelete = 2

	if n := count(t, h, audit.ServerScope); n != putAndDelete {
		t.Fatalf("%d entries, want put and delete", n)
	}
}

func testRegistrationDeleteInUse(t *testing.T, h Harness) {
	t.Helper()

	mustPut(t, h, newRegistration(secretKeyA))
	h.Namespace(t, nsAcme.String())

	_, err := h.Store.PutAssignment(
		t.Context(), newAssignment("101", nsAcme), 0, entry(audit.ServerScope, actionAssign),
	)
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	err = h.Store.DeleteRegistration(t.Context(), forge.KindGitHub, 0, entry(audit.ServerScope, "git-host-app:delete"))
	if !errors.Is(err, forge.ErrRegistrationInUse) {
		t.Fatalf("delete while assigned = %v, want ErrRegistrationInUse", err)
	}

	if _, err = h.Store.GetRegistration(t.Context(), forge.KindGitHub); err != nil {
		t.Fatalf("the registration is gone after a refused delete: %v", err)
	}
}

func testRegistrationDeleteMissing(t *testing.T, h Harness) {
	t.Helper()

	err := h.Store.DeleteRegistration(t.Context(), forge.KindGitHub, 0, entry(audit.ServerScope, "git-host-app:delete"))
	if !errors.Is(err, forge.ErrNoRegistration) {
		t.Fatalf("delete of a missing registration = %v, want ErrNoRegistration", err)
	}
}

func testRegistrationDeleteVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustPut(t, h, newRegistration(secretKeyA))

	err := h.Store.DeleteRegistration(
		t.Context(), forge.KindGitHub, first.Version+1, entry(audit.ServerScope, "git-host-app:delete"),
	)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("delete with another version = %v, want ErrVersionConflict", err)
	}

	if _, err = h.Store.GetRegistration(t.Context(), forge.KindGitHub); err != nil {
		t.Fatalf("the registration is gone after a refused delete: %v", err)
	}
}

func assign(t *testing.T, h Harness, id string, ns namespace.Name, ifVersion int64) (forge.Assignment, error) {
	t.Helper()

	return h.Store.PutAssignment(t.Context(), newAssignment(id, ns), ifVersion, entry(audit.ServerScope, actionAssign))
}

func mustAssign(t *testing.T, h Harness, id string, ns namespace.Name) forge.Assignment {
	t.Helper()

	h.Namespace(t, ns.String())

	got, err := assign(t, h, id, ns, 0)
	if err != nil {
		t.Fatalf("assign %s to %s: %v", id, ns, err)
	}

	return got
}

func testAssignmentCreate(t *testing.T, h Harness) {
	t.Helper()

	got := mustAssign(t, h, "101", nsAcme)
	if got.Version != 1 || got.Namespace != nsAcme || got.InstallationID != "101" {
		t.Fatalf("assign = %+v, want version 1 in acme for 101", got)
	}

	read, err := h.Store.GetAssignment(t.Context(), "101")
	if err != nil || read.Namespace != nsAcme || read.Version != 1 {
		t.Fatalf("get = %+v, %v; want acme at version 1", read, err)
	}

	entries := h.Audit(t, audit.ServerScope)
	if len(entries) != 1 || entries[0].Action != actionAssign {
		t.Fatalf("entries = %+v, want one assign entry", entries)
	}
}

func testAssignmentMissing(t *testing.T, h Harness) {
	t.Helper()

	if _, err := h.Store.GetAssignment(t.Context(), "101"); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get of an unassigned installation = %v, want ErrAssignmentNotFound", err)
	}
}

func testAssignmentMove(t *testing.T, h Harness) {
	t.Helper()

	first := mustAssign(t, h, "101", nsAcme)
	h.Namespace(t, nsBeta.String())

	got, err := assign(t, h, "101", nsBeta, first.Version)
	if err != nil {
		t.Fatalf("move: %v", err)
	}

	if got.Version != 2 || got.Namespace != nsBeta {
		t.Fatalf("move = %+v, want version 2 in beta", got)
	}

	read, err := h.Store.GetAssignment(t.Context(), "101")
	if err != nil || read.Namespace != nsBeta {
		t.Fatalf("get after the move = %+v, %v; want beta", read, err)
	}
}

func testAssignmentNoNamespace(t *testing.T, h Harness) {
	t.Helper()

	if _, err := assign(t, h, "101", "nowhere", 0); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("assign to a missing namespace = %v, want namespace.ErrNotFound", err)
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries, want 0", n)
	}
}

func testAssignmentInvalid(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	for name, edit := range map[string]func(a *forge.Assignment){
		"a bad installation ID": func(a *forge.Assignment) { a.InstallationID = "1; DROP TABLE" },
		"an empty installation": func(a *forge.Assignment) { a.InstallationID = "" },
		"no namespace":          func(a *forge.Assignment) { a.Namespace = "" },
		"no version":            func(a *forge.Assignment) { a.Version = 0 },
	} {
		a := newAssignment("101", nsAcme)
		edit(&a)

		_, err := h.Store.PutAssignment(t.Context(), a, 0, entry(audit.ServerScope, actionAssign))
		if !errors.Is(err, forge.ErrInvalid) {
			t.Errorf("assign with %s = %v, want ErrInvalid", name, err)
		}
	}

	if n := count(t, h, audit.ServerScope); n != 0 {
		t.Fatalf("%d entries after refused assigns, want 0", n)
	}
}

func testAssignmentVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustAssign(t, h, "101", nsAcme)
	h.Namespace(t, nsBeta.String())

	if _, err := assign(t, h, "101", nsBeta, first.Version+1); !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("assign with another version = %v, want ErrVersionConflict", err)
	}

	read, err := h.Store.GetAssignment(t.Context(), "101")
	if err != nil || read.Namespace != nsAcme {
		t.Fatalf("get = %+v, %v; want acme", read, err)
	}
}

func testAssignmentVersionMissing(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	if _, err := assign(t, h, "101", nsAcme, 1); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("assign with a version for a missing assignment = %v, want ErrAssignmentNotFound", err)
	}
}

func testAssignmentEntryScope(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, nsAcme.String())

	_, err := h.Store.PutAssignment(t.Context(), newAssignment("101", nsAcme), 0, entry(nsAcme, actionAssign))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("assign with the entry of a namespace = %v, want ErrInvalidEntry", err)
	}

	if _, err = h.Store.GetAssignment(t.Context(), "101"); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get after a refused assign = %v, want ErrAssignmentNotFound", err)
	}
}

func testAssignmentList(t *testing.T, h Harness) {
	t.Helper()

	mustAssign(t, h, "9", nsAcme)
	mustAssign(t, h, "100", nsBeta)
	mustAssign(t, h, "20", nsAcme)

	list, err := h.Store.ListAssignments(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	got := make([]string, 0, len(list))

	for i := range list {
		got = append(got, list[i].InstallationID)
	}

	if len(got) != 3 || got[0] != "9" || got[1] != "20" || got[2] != "100" {
		t.Fatalf("list = %v, want 9, 20, 100", got)
	}
}

func testAssignmentDelete(t *testing.T, h Harness) {
	t.Helper()

	mustAssign(t, h, "101", nsAcme)

	err := h.Store.DeleteAssignment(
		t.Context(), "101", 0, entry(audit.ServerScope, "git-host-installation:unassign"),
	)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := h.Store.GetAssignment(t.Context(), "101"); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get after delete = %v, want ErrAssignmentNotFound", err)
	}

	const assignAndUnassign = 2

	if n := count(t, h, audit.ServerScope); n != assignAndUnassign {
		t.Fatalf("%d entries, want assign and unassign", n)
	}
}

func testAssignmentDeleteMissing(t *testing.T, h Harness) {
	t.Helper()

	err := h.Store.DeleteAssignment(t.Context(), "101", 0, entry(audit.ServerScope, "git-host-installation:unassign"))
	if !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("delete of a missing assignment = %v, want ErrAssignmentNotFound", err)
	}
}

func testAssignmentDeleteVersion(t *testing.T, h Harness) {
	t.Helper()

	first := mustAssign(t, h, "101", nsAcme)

	err := h.Store.DeleteAssignment(
		t.Context(), "101", first.Version+1, entry(audit.ServerScope, "git-host-installation:unassign"),
	)
	if !errors.Is(err, forge.ErrVersionConflict) {
		t.Fatalf("delete with another version = %v, want ErrVersionConflict", err)
	}

	if _, err = h.Store.GetAssignment(t.Context(), "101"); err != nil {
		t.Fatalf("the assignment is gone after a refused delete: %v", err)
	}
}

// testAssignmentIndependent shows that the store keeps no link between a
// connection and the map: the service reads the map at each Open, so a
// reassignment takes effect at once.
func testAssignmentIndependent(t *testing.T, h Harness) {
	t.Helper()

	conn := mustCreate(t, h, nsAcme, "101")

	if _, err := h.Store.GetAssignment(t.Context(), "101"); !errors.Is(err, forge.ErrAssignmentNotFound) {
		t.Fatalf("get of an assignment that no one made = %v, want ErrAssignmentNotFound", err)
	}

	mustAssign(t, h, "101", nsBeta)

	read, err := h.Store.GetConnection(t.Context(), nsAcme, conn.ID)
	if err != nil || read.InstallationID != "101" {
		t.Fatalf("get connection = %+v, %v; want the connection to stay as it was", read, err)
	}
}
