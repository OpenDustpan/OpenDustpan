package profiletest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

func testCreateAndGet(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "payments/aws-prod")

	if p.Latest != 1 || p.Name != "payments/aws-prod" || p.Namespace != ns.Name {
		t.Fatalf("created = %+v", p)
	}

	byName, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByName(p.Name))
	if err != nil || byName.ID != p.ID || !byName.CreatedAt.Equal(epoch()) || byName.CreatedBy != subject {
		t.Errorf("Get by name = %+v, %v", byName, err)
	}

	byID, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(p.ID))
	if err != nil || byID.Name != p.Name {
		t.Errorf("Get by ID = %+v, %v", byID, err)
	}

	checkFirstRevision(t, s, p)
}

func checkFirstRevision(t *testing.T, s Store, p profile.Profile) {
	t.Helper()

	r, err := s.Profiles.Revision(t.Context(), p.Namespace, p.ID, profile.At{Number: 1})
	if err != nil || r.Number != 1 || r.Author != subject || r.Profile != p.ID {
		t.Errorf("Revision 1 = %+v, %v", r, err)
	}
}

func testDuplicateName(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	create(t, s, ns, "aws-prod")

	p, r := newProfile(ns.Name, "aws-prod")
	_, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "duplicate create", err, profile.ErrConflict)
}

func testNameInTwoNamespaces(t *testing.T, s Store) {
	t.Helper()

	a, b := newNamespace(t, s, "acme"), newNamespace(t, s, "globex")
	create(t, s, a, "aws-prod")
	create(t, s, b, "aws-prod")
}

func testOtherNamespace(t *testing.T, s Store) {
	t.Helper()

	a, b := newNamespace(t, s, "acme"), newNamespace(t, s, "globex")
	p := create(t, s, a, "aws-prod")

	_, err := s.Profiles.Get(t.Context(), b.Name, profile.RefByID(p.ID))
	wantErr(t, "Get in other namespace", err, profile.ErrNotFound)

	_, err = s.Profiles.Revision(t.Context(), b.Name, p.ID, profile.At{Number: 1})
	wantErr(t, "Revision in other namespace", err, profile.ErrNotFound)

	_, err = s.Profiles.Get(t.Context(), a.Name, profile.RefByName("missing"))
	wantErr(t, "Get of a missing name", err, profile.ErrNotFound)

	err = s.Profiles.Delete(t.Context(), anchor(b.Name, p.ID, 0), b.Fence(), entries(b.Name, "profile:manage"))
	wantErr(t, "Delete in other namespace", err, profile.ErrNotFound)
}

func testFirstRevisionNumber(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p, r := newProfile(ns.Name, "aws-prod")
	r.Number = 2

	_, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "create with revision 2", err, profile.ErrInvalid)

	p, r = newProfile(ns.Name, "aws-prod")
	r.Profile = profile.NewID()

	_, err = s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "create with a revision of another profile", err, profile.ErrInvalid)
}

func testFenced(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")

	stale := ns.Fence()
	stale.FailoverVersion = ns.FailoverVersion - 1

	np, nr := newProfile(ns.Name, "other")
	_, err := s.Profiles.Create(t.Context(), np, nr, stale, entries(ns.Name, "profile:write"))
	wantErr(t, "Create", err, namespace.ErrFenced)

	_, err = s.Profiles.Save(t.Context(), anchor(ns.Name, p.ID, 1), nextRevision(p, doc), stale,
		entries(ns.Name, "profile:write"))
	wantErr(t, "Save", err, namespace.ErrFenced)

	err = s.Profiles.Delete(t.Context(), anchor(ns.Name, p.ID, 0), stale, entries(ns.Name, "profile:manage"))
	wantErr(t, "Delete", err, namespace.ErrFenced)

	got, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(p.ID))
	if err != nil || got.Latest != 1 {
		t.Errorf("after refused writes: %+v, %v", got, err)
	}

	_, err = s.Profiles.Get(t.Context(), ns.Name, profile.RefByName("other"))
	wantErr(t, "profile after the refused create", err, profile.ErrNotFound)
}

func testAuditRollsBack(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	bad := entry(ns.Name, "profile:write")
	bad.Subject = ""

	p, r := newProfile(ns.Name, "aws-prod")
	_, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), []audit.Entry{bad})
	wantErr(t, "create with an invalid entry", err, audit.ErrInvalidEntry)

	_, err = s.Profiles.Get(t.Context(), ns.Name, profile.RefByName("aws-prod"))
	wantErr(t, "profile after the failed create", err, profile.ErrNotFound)

	_, err = s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries("globex", "profile:write"))
	wantErr(t, "create with an entry of another namespace", err, audit.ErrInvalidEntry)

	q := create(t, s, ns, "ok")

	_, err = s.Profiles.Save(
		t.Context(),
		anchor(ns.Name, q.ID, 1),
		nextRevision(q, doc),
		ns.Fence(),
		[]audit.Entry{bad},
	)
	wantErr(t, "save with an invalid entry", err, audit.ErrInvalidEntry)

	got, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(q.ID))
	if err != nil || got.Latest != 1 {
		t.Errorf("latest after the failed save = %d, %v, want 1", got.Latest, err)
	}

	err = s.Profiles.Delete(t.Context(), anchor(ns.Name, q.ID, 0), ns.Fence(), []audit.Entry{bad})
	wantErr(t, "delete with an invalid entry", err, audit.ErrInvalidEntry)

	if _, err = s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(q.ID)); err != nil {
		t.Errorf("profile after the failed delete: %v", err)
	}
}

func testAuditAppended(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")
	p = save(t, s, ns, p, doc+"  more: 1\n")
	remove(t, s, ns, p)

	l, err := s.Audit.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: bigPage})
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}

	actions := make([]string, 0, len(l.Entries))
	for i := range l.Entries {
		actions = append(actions, l.Entries[i].Action)
	}

	got := strings.Join(actions, ",")
	if !strings.Contains(got, "profile:manage") || strings.Count(got, "profile:write") != 2 {
		t.Errorf("audit actions (newest first) = %v, want two profile:write and one profile:manage", actions)
	}
}

func testAuditMany(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p, r := newProfile(ns.Name, "aws-prod")
	two := []audit.Entry{entry(ns.Name, "profile:write"), entry(ns.Name, "profile:open")}

	if _, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), two); err != nil {
		t.Fatalf("Create with two entries: %v", err)
	}

	l, err := s.Audit.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: bigPage})
	if err != nil {
		t.Fatalf("audit List: %v", err)
	}

	if len(l.Entries) != 2 || l.Entries[0].Action != "profile:open" || l.Entries[1].Action != "profile:write" {
		t.Errorf("audit entries (newest first) = %+v, want profile:open then profile:write", l.Entries)
	}
}

func testAuditNone(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p, r := newProfile(ns.Name, "aws-prod")

	_, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), nil)
	wantErr(t, "create with no entry", err, profile.ErrInvalid)

	_, err = s.Profiles.Get(t.Context(), ns.Name, profile.RefByName("aws-prod"))
	wantErr(t, "profile after the failed create", err, profile.ErrNotFound)

	q := create(t, s, ns, "ok")

	_, err = s.Profiles.Save(t.Context(), anchor(ns.Name, q.ID, 1), nextRevision(q, doc), ns.Fence(), []audit.Entry{})
	wantErr(t, "save with no entry", err, profile.ErrInvalid)

	err = s.Profiles.Delete(t.Context(), anchor(ns.Name, q.ID, 0), ns.Fence(), nil)
	wantErr(t, "delete with no entry", err, profile.ErrInvalid)

	got, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(q.ID))
	if err != nil || got.Latest != 1 {
		t.Errorf("profile after the refused writes = %+v, %v", got, err)
	}
}

func testListPages(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	for _, n := range []string{"c", "a", "team/b", "b"} {
		create(t, s, ns, n)
	}

	var names []string

	after := profile.Name("")

	for range pageSize {
		page, err := s.Profiles.List(t.Context(), filter(ns.Name, "", after, smallPage))
		if err != nil {
			t.Fatalf("List: %v", err)
		}

		for _, p := range page.Profiles {
			names = append(names, string(p.Name))
		}

		if page.Next == "" {
			break
		}

		after = page.Next
	}

	if got := strings.Join(names, ","); got != "a,b,c,team/b" {
		t.Errorf("names = %q, want a,b,c,team/b", got)
	}

	_, err := s.Profiles.List(t.Context(), filter(ns.Name, "", "", 0))
	wantErr(t, "List with limit 0", err, profile.ErrInvalid)

	empty, err := s.Profiles.List(t.Context(), filter("unknown", "", "", pageSize))
	if err != nil || len(empty.Profiles) != 0 || empty.Next != "" {
		t.Errorf("List of an unknown namespace = %+v, %v", empty, err)
	}
}

func testListFilters(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	create(t, s, ns, "team/one")
	two := create(t, s, ns, "team/two")
	create(t, s, ns, "solo")
	create(t, s, ns, "teams/other")
	remove(t, s, ns, two)

	page, err := s.Profiles.List(t.Context(), filter(ns.Name, "team", "", bigPage))
	if err != nil || len(page.Profiles) != 1 || page.Profiles[0].Name != "team/one" {
		t.Errorf("folder team = %+v, %v", page.Profiles, err)
	}

	all, err := s.Profiles.List(t.Context(), filter(ns.Name, "", "", bigPage))
	if err != nil || len(all.Profiles) != 3 {
		t.Errorf("all = %d profiles, %v, want 3", len(all.Profiles), err)
	}
}

func testSave(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")
	updated := save(t, s, ns, p, doc+"  b: 2\n")

	if updated.Latest != 2 || updated.ID != p.ID || !updated.UpdatedAt.After(p.UpdatedAt) {
		t.Errorf("saved profile = %+v", updated)
	}

	got, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(p.ID))
	if err != nil || got.Latest != 2 {
		t.Errorf("Get after save = %+v, %v", got, err)
	}

	r, err := s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: 0})
	if err != nil || r.Number != 2 || r.Author != "bob" || !strings.Contains(string(r.Document), "b: 2") {
		t.Errorf("newest revision = %+v, %v", r, err)
	}

	old, err := s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: 1})
	if err != nil || strings.Contains(string(old.Document), "b: 2") {
		t.Errorf("revision 1 changed: %+v, %v", old, err)
	}
}

func testSaveStale(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")
	save(t, s, ns, p, doc+"  b: 2\n")

	_, err := s.Profiles.Save(t.Context(), anchor(ns.Name, p.ID, 1), nextRevision(p, doc+"  c: 3\n"), ns.Fence(),
		entries(ns.Name, "profile:write"))
	wantErr(t, "save on base 1", err, profile.ErrConflict)

	_, err = s.Profiles.Save(t.Context(), anchor(ns.Name, profile.NewID(), 1), nextRevision(p, doc), ns.Fence(),
		entries(ns.Name, "profile:write"))
	wantErr(t, "save of an unknown profile", err, profile.ErrNotFound)
}

func testSaveNumber(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")

	r := nextRevision(p, doc)
	r.Number = pageSize

	_, err := s.Profiles.Save(t.Context(), anchor(ns.Name, p.ID, 1), r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "save with number 5", err, profile.ErrInvalid)

	r = nextRevision(p, doc)
	r.Profile = profile.NewID()

	_, err = s.Profiles.Save(t.Context(), anchor(ns.Name, p.ID, 1), r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "save with another profile in the revision", err, profile.ErrInvalid)
}

func testRevisionRead(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")

	_, err := s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: secondRevision})
	wantErr(t, "revision 2", err, profile.ErrNotFound)

	_, err = s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: -1})
	wantErr(t, "revision -1", err, profile.ErrInvalid)

	r, err := s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: 0})
	if err != nil || r.Number != 1 {
		t.Errorf("revision 0 = %+v, %v", r, err)
	}
}

func testRevisionsList(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")

	for range 3 {
		p = save(t, s, ns, p, doc)
	}

	revs, err := s.Profiles.Revisions(t.Context(), ns.Name, p.ID, 0, firstPage)
	if err != nil || len(revs) != 3 || revs[0].Number != 4 || revs[2].Number != 2 {
		t.Fatalf("first page = %+v, %v", revs, err)
	}

	revs, err = s.Profiles.Revisions(t.Context(), ns.Name, p.ID, secondRevision, bigPage)
	if err != nil || len(revs) != 1 || revs[0].Number != 1 {
		t.Errorf("second page = %+v, %v", revs, err)
	}

	_, err = s.Profiles.Revisions(t.Context(), ns.Name, profile.NewID(), 0, firstPage)
	wantErr(t, "unknown profile", err, profile.ErrNotFound)

	_, err = s.Profiles.Revisions(t.Context(), ns.Name, p.ID, 0, 0)
	wantErr(t, "limit 0", err, profile.ErrInvalid)
}

func testDelete(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")
	remove(t, s, ns, p)

	_, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByID(p.ID))
	wantErr(t, "Get after delete", err, profile.ErrNotFound)

	_, err = s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: 1})
	wantErr(t, "Revision after delete", err, profile.ErrNotFound)

	err = s.Profiles.Delete(t.Context(), anchor(ns.Name, p.ID, 0), ns.Fence(), entries(ns.Name, "profile:manage"))
	wantErr(t, "second Delete", err, profile.ErrNotFound)

	again := create(t, s, ns, "aws-prod")
	if again.ID == p.ID {
		t.Error("a new profile with a free name got the ID of the deleted one")
	}
}

func testDeleteStale(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := create(t, s, ns, "aws-prod")
	save(t, s, ns, p, doc)

	err := s.Profiles.Delete(t.Context(), anchor(ns.Name, p.ID, 1), ns.Fence(), entries(ns.Name, "profile:manage"))
	wantErr(t, "delete if latest is 1", err, profile.ErrConflict)

	err = s.Profiles.Delete(
		t.Context(),
		anchor(ns.Name, p.ID, secondRevision),
		ns.Fence(),
		entries(ns.Name, "profile:manage"),
	)
	if err != nil {
		t.Errorf("delete if latest is 2: %v", err)
	}
}

func testDocumentBytes(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p, r := newProfile(ns.Name, "aws-prod")
	r.Document = []byte("schemaVersion: 1\r\n# ünïcode ✓\r\nvalues:\r\n  a: \"b\"\r\n")
	r.Reason = "first"

	if _, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := s.Profiles.Revision(t.Context(), ns.Name, p.ID, profile.At{Number: 1})
	if err != nil || !bytes.Equal(got.Document, r.Document) || got.SHA256 != r.SHA256 || got.Reason != "first" {
		t.Errorf("revision = %+v, %v, want the document, the hash and the reason as stored", got, err)
	}
}

func testLimit(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	for i := range profile.MaxProfiles {
		create(t, s, ns, fmt.Sprintf("p%04d", i))
	}

	p, r := newProfile(ns.Name, "one-more")
	_, err := s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write"))

	if !errors.Is(err, profile.ErrLimit) {
		t.Errorf("profile %d: error = %v, want ErrLimit", profile.MaxProfiles+1, err)
	}

	first, err := s.Profiles.Get(t.Context(), ns.Name, profile.RefByName("p0000"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	remove(t, s, ns, first)

	if _, err = s.Profiles.Create(t.Context(), p, r, ns.Fence(), entries(ns.Name, "profile:write")); err != nil {
		t.Errorf("create after a delete: %v", err)
	}
}

// pendingRequest starts a replay record in s and returns a context that
// carries the Completion of the request, with the Completion. The answer
// that the Completion builds holds the status and the text of body.
func pendingRequest(
	t *testing.T, s Store, key string, build func(result any) (idempotency.Answer, error),
) (context.Context, *idempotency.Completion, idempotency.Ref) {
	t.Helper()

	ref := idempotency.Ref{Principal: subject, Method: "POST", Path: "/profiles", Key: idempotency.Key(key)}

	res, err := s.Records.Begin(t.Context(), ref, []byte("body"), epoch())
	if err != nil || res.Outcome != idempotency.Started {
		t.Fatalf("Begin: outcome = %d, err = %v, want Started", res.Outcome, err)
	}

	c := idempotency.NewCompletion(ref, res.Token, build)

	return idempotency.WithCompletion(t.Context(), c), c, ref
}

// replayOf returns the answer that a repeat of the request finds.
func replayOf(t *testing.T, s Store, ref idempotency.Ref) idempotency.Answer {
	t.Helper()

	res, err := s.Records.Begin(t.Context(), ref, []byte("body"), epoch())
	if err != nil || res.Outcome != idempotency.Replayed {
		t.Fatalf("Begin after the write: outcome = %d, err = %v, want Replayed", res.Outcome, err)
	}

	return res.Answer
}

func testCompletionStored(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	p := completeCreate(t, s, ns)
	completeSave(t, s, ns, p)
	completeDelete(t, s, ns, p)
}

// completeCreate checks that a create passes the profile and its first
// revision, and returns the created profile.
func completeCreate(t *testing.T, s Store, ns namespace.Namespace) profile.Profile {
	t.Helper()

	ctx, c, ref := pendingRequest(t, s, "create", func(result any) (idempotency.Answer, error) {
		w, ok := result.(profile.Written)
		if !ok || w.Revision == nil {
			return idempotency.Answer{}, errNotWritten
		}

		body := fmt.Sprintf("%s/%d", w.Profile.ID, w.Revision.Number)

		return idempotency.Answer{Status: statusCreated, Headers: nil, Body: []byte(body)}, nil
	})

	p, r := newProfile(ns.Name, "aws-prod")
	if _, err := s.Profiles.Create(ctx, p, r, ns.Fence(), entries(ns.Name, "profile:write")); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if want := fmt.Sprintf("%s/1", p.ID); !c.Stored() || string(replayOf(t, s, ref).Body) != want {
		t.Errorf("create: stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, s, ref).Body, want)
	}

	return p
}

// completeSave checks that a save passes the profile after the write.
func completeSave(t *testing.T, s Store, ns namespace.Namespace, p profile.Profile) {
	t.Helper()

	ctx, c, ref := pendingRequest(t, s, "save", func(result any) (idempotency.Answer, error) {
		w, ok := result.(profile.Written)
		if !ok {
			return idempotency.Answer{}, errNotWritten
		}

		return idempotency.Answer{
			Status: statusCreated, Headers: nil, Body: []byte(fmt.Sprintf("latest %d", w.Profile.Latest)),
		}, nil
	})

	_, err := s.Profiles.Save(ctx, anchor(ns.Name, p.ID, 1), nextRevision(p, doc), ns.Fence(),
		entries(ns.Name, "profile:write"))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	if !c.Stored() || string(replayOf(t, s, ref).Body) != "latest 2" {
		t.Errorf("save: stored = %t, replay = %q, want %q", c.Stored(), replayOf(t, s, ref).Body, "latest 2")
	}
}

// completeDelete checks that a delete stores the answer of its request.
func completeDelete(t *testing.T, s Store, ns namespace.Namespace, p profile.Profile) {
	t.Helper()

	ctx, c, ref := pendingRequest(t, s, "delete", func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Status: statusNoContent, Headers: nil, Body: nil}, nil
	})

	err := s.Profiles.Delete(ctx, anchor(ns.Name, p.ID, 0), ns.Fence(), entries(ns.Name, "profile:manage"))
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if got := replayOf(t, s, ref); !c.Stored() || got.Status != statusNoContent {
		t.Errorf("delete: stored = %t, replay status = %d, want true, 204", c.Stored(), got.Status)
	}
}

func testCompletionKeptPending(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")
	create(t, s, ns, "aws-prod")

	ctx, c, ref := pendingRequest(t, s, "dup", func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Status: statusCreated, Headers: nil, Body: nil}, nil
	})

	p, r := newProfile(ns.Name, "aws-prod")
	_, err := s.Profiles.Create(ctx, p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "duplicate create with a completion", err, profile.ErrConflict)

	res, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), epoch())
	if beginErr != nil || res.Outcome != idempotency.InProgress || c.Stored() {
		t.Errorf("after a failed write: outcome = %d, err = %v, stored = %t, want InProgress, nil, false",
			res.Outcome, beginErr, c.Stored())
	}
}

func testCompletionLost(t *testing.T, s Store) {
	t.Helper()

	ns := newNamespace(t, s, "acme")

	ctx, c, ref := pendingRequest(t, s, "lost", func(any) (idempotency.Answer, error) {
		return idempotency.Answer{Status: statusCreated, Headers: nil, Body: nil}, nil
	})

	// The record expires and a retry takes the key.
	late := epoch().Add(idempotency.PendingTTL + time.Second)
	retry, beginErr := s.Records.Begin(t.Context(), ref, []byte("body"), late)
	if beginErr != nil || retry.Outcome != idempotency.Started {
		t.Fatalf("retry: outcome = %d, err = %v, want Started", retry.Outcome, beginErr)
	}

	p, r := newProfile(ns.Name, "aws-prod")
	_, err := s.Profiles.Create(ctx, p, r, ns.Fence(), entries(ns.Name, "profile:write"))
	wantErr(t, "create with a lost record", err, idempotency.ErrLost)

	_, err = s.Profiles.Get(t.Context(), ns.Name, profile.RefByName("aws-prod"))
	wantErr(t, "Get after a lost record", err, profile.ErrNotFound)

	list, listErr := s.Audit.List(t.Context(), ns.Name, audit.Page{Number: 1, Size: bigPage})
	if listErr != nil || list.Total != 0 || c.Stored() {
		t.Errorf("after a lost record: audit total = %d, err = %v, stored = %t, want 0, nil, false",
			list.Total, listErr, c.Stored())
	}
}

// The statuses of the answers that the cases build.
const (
	statusCreated   = 201
	statusNoContent = 204
)

var errNotWritten = errors.New("the store did not pass a written profile")
