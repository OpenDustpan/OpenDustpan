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
		CreatedAt:      when,
		ExpiresAt:      when.Add(time.Hour),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    id,
		Subject:        subject,
		CreatedBy:      "operator",
		Reason:         "every administrator lost access",
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

	late := recovery("prn-late", "dave", at(10))
	late.Token.CreatedAt = late.Person.CreatedAt.Add(accesstoken.HostFirstTokenWindow + time.Millisecond)

	early := recovery("prn-early", "erin", at(10))
	early.Token.CreatedAt = early.Person.CreatedAt.Add(-time.Millisecond)

	cases := []struct {
		want error
		name string
		r    accesstoken.HostRecovery
	}{
		{name: "token past the record", r: long, want: accesstoken.ErrNotOwnToken},
		{name: "refused entry", r: bad, want: audit.ErrInvalidEntry},
		{name: "token after the window", r: late, want: accesstoken.ErrNotOwnToken},
		{name: "token before the person", r: early, want: accesstoken.ErrNotOwnToken},
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

func testConcurrentHostRecoveries(t *testing.T, s Store) {
	t.Helper()

	const writers = 2

	errs := make([]error, writers)

	var wg sync.WaitGroup

	for i := range writers {
		wg.Go(func() {
			r := recovery("prn-temp-"+strconv.Itoa(i), "alice", at(10))
			r.Digest = digest(i)
			errs[i] = s.CreateHostRecovery(t.Context(), &r)
		})
	}

	wg.Wait()

	won := 0

	for i, err := range errs {
		id := "prn-temp-" + strconv.Itoa(i)

		switch {
		case err == nil:
			won++

			equalToken(t, mustGet(t, s, "tok-"+id), recovery(id, "alice", at(10)).Token)
		case errors.Is(err, principal.ErrAlreadyExists):
			if _, getErr := s.GetToken(t.Context(), "tok-"+id); !errors.Is(getErr, accesstoken.ErrNotFound) {
				t.Fatalf("token of the refused recovery %d: %v, want ErrNotFound", i, getErr)
			}
		default:
			t.Fatalf("recovery %d: %v, want nil or principal.ErrAlreadyExists", i, err)
		}
	}

	if won != 1 {
		t.Fatalf("%d recoveries won, want 1", won)
	}
}
