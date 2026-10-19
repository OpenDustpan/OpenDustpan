package accesstokentest

import (
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// hostToken returns the first host token tok of principalID made at
// created, that expires an hour later.
func hostToken(id, principalID string, created time.Time) accesstoken.Token {
	return accesstoken.Token{
		CreatedAt:   created,
		ExpiresAt:   created.Add(time.Hour),
		LastUsedAt:  time.Time{},
		RevokedAt:   time.Time{},
		DisabledAt:  time.Time{},
		ID:          id,
		PrincipalID: principalID,
		Name:        tempadmin.HostTokenName,
		Kind:        accesstoken.KindPersonal,
		ReplacedBy:  "",
		CreatedBy:   tempadmin.HostSubject,
	}
}

// newTempAdminPerson makes the person id at made with a record that
// starts at made and works for a day.
func newTempAdminPerson(t *testing.T, s Store, id string, made time.Time) {
	t.Helper()

	mustPrincipalAt(t, s, id, principal.KindPerson, made)
	mustTempAdmin(t, s, id, id, made)
}

func testFirstHostToken(t *testing.T, s Store) {
	t.Helper()

	made := at(10)
	newTempAdminPerson(t, s, "prn-temp", made)

	tok := hostToken("tok-1", "prn-temp", made.Add(time.Minute))
	if err := s.CreateFirstHostToken(t.Context(), tok, digest(1), entryAt("tok-1", tok.CreatedAt)); err != nil {
		t.Fatalf("first host token: %v", err)
	}

	equalToken(t, mustGet(t, s, "tok-1"), tok)

	again := hostToken("tok-2", "prn-temp", made.Add(2*time.Minute))
	err := s.CreateFirstHostToken(t.Context(), again, digest(2), entryAt("tok-2", again.CreatedAt))
	wantErr(t, "second host token", err, accesstoken.ErrNotOwnToken)
}

func testFirstHostTokenRefusals(t *testing.T, s Store) {
	t.Helper()

	made := at(10)

	newTempAdminPerson(t, s, "prn-temp", made)
	mustPrincipalAt(t, s, "prn-plain", principal.KindPerson, made)
	mustPrincipalAt(t, s, "prn-svc", principal.KindService, made)
	mustTempAdmin(t, s, "prn-svc", "prn-svc", made)

	late := hostToken("tok-late", "prn-temp", made.Add(accesstoken.HostFirstTokenWindow+time.Millisecond))
	early := hostToken("tok-early", "prn-temp", made.Add(-time.Millisecond))
	long := hostToken("tok-long", "prn-temp", made.Add(time.Minute))
	long.ExpiresAt = made.Add(tempadmin.Lifetime + time.Millisecond)

	cases := []struct {
		tok  accesstoken.Token
		name string
	}{
		{name: "no record", tok: hostToken("tok-plain", "prn-plain", made.Add(time.Minute))},
		{name: "not a person", tok: hostToken("tok-svc", "prn-svc", made.Add(time.Minute))},
		{name: "after the window", tok: late},
		{name: "before the person", tok: early},
		{name: "past the record", tok: long},
		{name: "no principal", tok: hostToken("tok-none", "prn-none", made.Add(time.Minute))},
	}

	for i := range cases {
		c := &cases[i]
		err := s.CreateFirstHostToken(t.Context(), c.tok, digest(10+i), entryAt(c.tok.ID, c.tok.CreatedAt))
		wantErr(t, c.name, err, accesstoken.ErrNotOwnToken)

		if _, err := s.GetToken(t.Context(), c.tok.ID); !errors.Is(err, accesstoken.ErrNotFound) {
			t.Fatalf("%s: token read = %v, want ErrNotFound", c.name, err)
		}
	}

	ended := made.Add(tempadmin.Lifetime)
	expired := hostToken("tok-expired", "prn-temp", ended)
	expired.ExpiresAt = ended.Add(time.Hour)

	err := s.CreateFirstHostToken(t.Context(), expired, digest(30), entryAt("tok-expired", ended))
	wantErr(t, "expired record", err, accesstoken.ErrNotOwnToken)
}

func testConcurrentFirstHostTokens(t *testing.T, s Store) {
	t.Helper()

	made := at(10)
	newTempAdminPerson(t, s, "prn-temp", made)

	const writers = 2

	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			id := "tok-" + strconv.Itoa(i)
			tok := hostToken(id, "prn-temp", made.Add(time.Minute))
			errs[i] = s.CreateFirstHostToken(t.Context(), tok, digest(i), entryAt(id, tok.CreatedAt))
		})
	}

	wg.Wait()

	won := 0

	for i, err := range errs {
		switch {
		case err == nil:
			won++
		case errors.Is(err, accesstoken.ErrNotOwnToken):
		default:
			t.Fatalf("issue %d: %v, want nil or ErrNotOwnToken", i, err)
		}
	}

	if won != 1 {
		t.Fatalf("%d issues won, want 1", won)
	}
}

// recovery returns a host recovery of the person id, named by subject, at
// the time when.
func recovery(id, subject string, when time.Time) accesstoken.HostRecovery {
	p := principal.Principal{
		CreatedAt: when, UpdatedAt: when, DisabledAt: time.Time{},
		ID: id, Subject: subject, DisplayName: subject,
		Kind: principal.KindPerson, Source: principal.SourceLocal,
		Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	g := access.Grant{
		CreatedAt: when, Condition: nil, Object: identity.ServerObject(), ID: "grt-" + id,
		Role: access.RoleSiteAdministrator, CreatedBy: tempadmin.HostSubject,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	rec := tempadmin.TempAdmin{
		CreatedAt: when, ExpiresAt: when.Add(time.Hour), PrincipalID: id, Subject: subject,
		CreatedBy: "operator", Reason: "every administrator lost access",
	}

	tok := hostToken("tok-"+id, id, when)

	return accesstoken.HostRecovery{
		Person: p, PersonEntry: hostEntry(when, principal.Ref(id)),
		Grant: g, GrantEntry: hostEntry(when, access.GrantRef(g)),
		Record: rec, RecordEntry: hostEntry(when, tempadmin.Ref(id)),
		Token: tok, Digest: digest(1), TokenEntry: hostEntry(when, accesstoken.Ref(tok.ID)),
	}
}

// hostEntry returns a valid entry of the host for the object at when.
func hostEntry(when time.Time, object string) audit.Entry {
	e := entryAt("tok-x", when)
	e.Subject = tempadmin.HostSubject
	e.Object = object

	return e
}

func testHostRecovery(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()
	r := recovery("prn-temp", "alice", at(10))

	if err := s.CreateHostRecovery(ctx, &r); err != nil {
		t.Fatalf("host recovery: %v", err)
	}

	if p, err := s.GetPrincipal(ctx, "prn-temp"); err != nil || p.Subject != "alice" {
		t.Fatalf("person = %+v, %v; want alice", p, err)
	}

	rec, err := s.GetTempAdmin(ctx, "prn-temp")
	if err != nil || rec.Reason != r.Record.Reason || !rec.ExpiresAt.Equal(r.Record.ExpiresAt) {
		t.Fatalf("record = %+v, %v; want %+v", rec, err, r.Record)
	}

	equalToken(t, mustGet(t, s, r.Token.ID), r.Token)

	again := recovery("prn-other", "alice", at(11))
	again.Digest = digest(2)
	wantErr(t, "recovery of a taken subject", s.CreateHostRecovery(ctx, &again), principal.ErrAlreadyExists)
}

func testHostRecoveryRollsBack(t *testing.T, s Store) {
	t.Helper()

	ctx := t.Context()

	long := recovery("prn-long", "bob", at(10))
	long.Token.ExpiresAt = long.Record.ExpiresAt.Add(time.Millisecond)

	bad := recovery("prn-bad", "carol", at(10))
	bad.TokenEntry.Subject = ""

	cases := []struct {
		want error
		name string
		r    accesstoken.HostRecovery
	}{
		{name: "token past the record", r: long, want: accesstoken.ErrNotOwnToken},
		{name: "refused entry", r: bad, want: audit.ErrInvalidEntry},
	}

	for i := range cases {
		c := &cases[i]
		wantErr(t, c.name, s.CreateHostRecovery(ctx, &c.r), c.want)

		if _, err := s.GetPrincipal(ctx, c.r.Person.ID); !errors.Is(err, principal.ErrNotFound) {
			t.Fatalf("%s: person read = %v, want ErrNotFound", c.name, err)
		}

		if _, err := s.GetTempAdmin(ctx, c.r.Person.ID); !errors.Is(err, tempadmin.ErrNotFound) {
			t.Fatalf("%s: record read = %v, want ErrNotFound", c.name, err)
		}

		if _, err := s.GetToken(ctx, c.r.Token.ID); !errors.Is(err, accesstoken.ErrNotFound) {
			t.Fatalf("%s: token read = %v, want ErrNotFound", c.name, err)
		}
	}
}
