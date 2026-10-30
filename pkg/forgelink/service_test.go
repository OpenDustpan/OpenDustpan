package forgelink_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/forgelink/forgelinktest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// people is the People of the tests: a fixed set of principals.
type people struct {
	byID map[string]principal.Principal
}

func (p *people) GetPrincipalBySubject(_ context.Context, subject string) (principal.Principal, error) {
	for _, v := range p.byID {
		if v.Subject == subject {
			return v, nil
		}
	}

	return principal.Principal{}, principal.ErrNotFound
}

func (p *people) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	v, ok := p.byID[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return v, nil
}

func person(id string) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: id, Subject: "user:" + id,
		DisplayName: "Display " + id, Kind: principal.KindPerson, Source: "", Namespace: "", GroupCount: 0,
		BreakGlass: false,
	}
}

func session(id string) identity.Principal {
	return identity.Principal{Subject: "user:" + id, Kind: identity.KindPerson, Credential: identity.CredentialSession}
}

// counter is a reader of bytes that never repeat a block of 32 bytes, so each token differs.
type counter struct{ n byte }

func (c *counter) Read(p []byte) (int, error) {
	c.n++

	for i := range p {
		p[i] = c.n + byte(i)
	}

	return len(p), nil
}

type fixture struct {
	svc    *forgelink.Service
	store  *forgelinktest.Mem
	people *people
	clock  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		store:  forgelinktest.NewMem(),
		people: &people{byID: map[string]principal.Principal{"ana": person("ana"), "bob": person("bob")}},
		clock:  time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
	}

	f.svc = forgelink.NewService(f.store, f.people, func() time.Time { return f.clock }, &counter{n: 0})

	return f
}

func entry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "",
		Subject:       "user:ana",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "caller:manage-git-host-links",
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "192.0.2.1",
		UserAgent:     "test",
		Channel:       audit.ChannelAPI,
		Kind:          "",
		Outcome:       "",
		RequestID:     "req",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

func gh(id string) forgelink.Account {
	return forgelink.Account{Kind: forge.KindGitHub, Host: "github.com", ID: id}
}

func proof(id, login string) forgelink.Proof {
	return forgelink.Proof{Account: gh(id), Login: login, DeliveryID: "d-" + id}
}

// link makes the link of the account id for the person who in a full flow.
func (f *fixture) link(t *testing.T, who, id, previous string) forgelink.Link {
	t.Helper()

	started, err := f.svc.Start(t.Context(), session(who))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := f.svc.Prove(t.Context(), started.Token, proof(id, "login-"+id)); err != nil {
		t.Fatalf("prove: %v", err)
	}

	l, _, err := f.svc.Accept(t.Context(), session(who), started.Token, previous, entry())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	return l
}

func TestBothSidesProve(t *testing.T) {
	t.Parallel()

	t.Run("the person alone makes no link", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		started, err := f.svc.Start(t.Context(), session("ana"))
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		_, _, err = f.svc.Accept(t.Context(), session("ana"), started.Token, "", entry())
		if !errors.Is(err, forgelink.ErrNotProved) {
			t.Fatalf("accept without a proof = %v, want ErrNotProved", err)
		}

		if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
			t.Fatalf("resolve = %v, want ErrNotLinked", err)
		}
	})

	t.Run("the account alone makes no link", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		started, err := f.svc.Start(t.Context(), session("ana"))
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		if _, err := f.svc.Prove(t.Context(), started.Token, proof("42", "ana-gh")); err != nil {
			t.Fatalf("prove: %v", err)
		}

		if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
			t.Fatalf("resolve = %v, want ErrNotLinked: a proof alone is no link", err)
		}

		if n := len(f.store.Entries(t.Context())); n != 0 {
			t.Fatalf("audit entries = %d, want none", n)
		}
	})

	t.Run("a proof with a token that no person started proves nothing", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		token := "tokenZtokenZtokenZtokenZtokenZtokenZtokenZtokenZ"

		_, err := f.svc.Prove(t.Context(), token, proof("42", "x"))
		if !errors.Is(err, forgelink.ErrNotFound) {
			t.Fatalf("prove = %v, want ErrNotFound", err)
		}

		_, err = f.svc.Prove(t.Context(), "short", proof("42", "x"))
		if !errors.Is(err, forgelink.ErrNotFound) {
			t.Fatalf("prove with a short token = %v, want ErrNotFound", err)
		}
	})

	t.Run("the person cannot name the account: the proof does", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)
		l := f.link(t, "ana", "42", "")

		if l.Account != gh("42") || l.Principal != "ana" {
			t.Fatalf("link = %+v, want the account of the proof for ana", l)
		}
	})

	t.Run("a person who did not start the request cannot show or accept it", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		started, err := f.svc.Start(t.Context(), session("ana"))
		if err != nil {
			t.Fatalf("start: %v", err)
		}

		if _, err := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); err != nil {
			t.Fatalf("prove: %v", err)
		}

		if _, err := f.svc.Show(t.Context(), session("bob"), started.Token); !errors.Is(err, forgelink.ErrNotFound) {
			t.Fatalf("show by another person = %v, want ErrNotFound", err)
		}

		_, _, err = f.svc.Accept(t.Context(), session("bob"), started.Token, "", entry())
		if !errors.Is(err, forgelink.ErrNotFound) {
			t.Fatalf("accept by another person = %v, want ErrNotFound", err)
		}
	})
}

func TestOnlyAPersonInASessionMakesALink(t *testing.T) {
	t.Parallel()

	disabled := person("dan")
	disabled.DisabledAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	glass := person("gus")
	glass.BreakGlass = true

	service := person("svc")
	service.Kind = principal.KindService

	tests := []struct {
		name   string
		caller identity.Principal
	}{
		{
			"an access token",
			identity.Principal{
				Subject:    "user:ana",
				Kind:       identity.KindPerson,
				Credential: identity.CredentialAccessToken,
			},
		},
		{"no credential", identity.Principal{Subject: "user:ana", Kind: identity.KindPerson, Credential: ""}},
		{
			"a job token",
			identity.Principal{Subject: "user:ana", Kind: identity.KindPerson, Credential: identity.CredentialJobToken},
		},
		{
			"a service principal",
			identity.Principal{Subject: "user:svc", Kind: identity.KindService, Credential: identity.CredentialSession},
		},
		{"a disabled person", session("dan")},
		{"a break-glass account", session("gus")},
		{"a service record behind a session", session("svc")},
		{"a subject with no record", session("nobody")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.people.byID["dan"], f.people.byID["gus"], f.people.byID["svc"] = disabled, glass, service

			if _, err := f.svc.Start(t.Context(), tc.caller); !errors.Is(err, forgelink.ErrNotPerson) {
				t.Errorf("start = %v, want ErrNotPerson", err)
			}

			if _, _, err := f.svc.Accept(
				t.Context(),
				tc.caller,
				"tokenZtokenZtokenZtokenZtokenZtokenZtokenZtokenZ",
				"",
				entry(),
			); !errors.Is(
				err,
				forgelink.ErrNotPerson,
			) {
				t.Errorf("accept = %v, want ErrNotPerson", err)
			}

			if err := f.svc.Delete(t.Context(), tc.caller, gh("42"), entry()); !errors.Is(err, forgelink.ErrNotPerson) {
				t.Errorf("delete = %v, want ErrNotPerson", err)
			}
		})
	}
}

func TestExactIDs(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "AbC", "")

	if p, err := f.svc.Resolve(t.Context(), gh("AbC")); err != nil || p.ID != "ana" {
		t.Fatalf("resolve exact = %v, %v, want ana", p.ID, err)
	}

	for name, a := range map[string]forgelink.Account{
		"ID in lowercase":     gh("abc"),
		"the login as the ID": gh("login-AbC"),
		"another host":        {Kind: forge.KindGitHub, Host: "ghe.example.com", ID: "AbC"},
		"another kind":        {Kind: forge.KindGitLab, Host: "github.com", ID: "AbC"},
		"an ID with a suffix": gh("AbC "),
		"an ID with a prefix": gh("0AbC"),
		"an empty ID":         gh(""),
		"a host in capitals":  {Kind: forge.KindGitHub, Host: "GitHub.com", ID: "AbC"},
	} {
		_, err := f.svc.Resolve(t.Context(), a)
		if !errors.Is(err, forgelink.ErrNotLinked) && !errors.Is(err, forgelink.ErrInvalid) {
			t.Errorf("%s: resolve = %v, want no link", name, err)
		}
	}
}

func TestRenamedLoginKeepsTheLink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "42", "")

	// The account renames its login. A proof carries the new login. The ID is the same.
	l := f.link(t, "ana", "42", "ana")
	if l.Account.ID != "42" || l.Login != "login-42" {
		t.Fatalf("link = %+v", l)
	}

	if p, err := f.svc.Resolve(t.Context(), gh("42")); err != nil || p.ID != "ana" {
		t.Fatalf("resolve = %v, %v", p.ID, err)
	}
}

func TestNoSilentReplace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "42", "")

	started, err := f.svc.Start(t.Context(), session("bob"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); err != nil {
		t.Fatalf("prove: %v", err)
	}

	view, err := f.svc.Show(t.Context(), session("bob"), started.Token)
	if err != nil || !view.HasCurrent || view.Current.Principal != "ana" || view.CurrentName != "Display ana" {
		t.Fatalf("show = %+v, %v, want the link of ana", view, err)
	}

	_, _, err = f.svc.Accept(t.Context(), session("bob"), started.Token, "", entry())
	if !errors.Is(err, forgelink.ErrConflict) {
		t.Fatalf("accept without the previous principal = %v, want ErrConflict", err)
	}

	if p, err := f.svc.Resolve(t.Context(), gh("42")); err != nil || p.ID != "ana" {
		t.Fatalf("the link of ana changed: %v, %v", p.ID, err)
	}

	l, replaced, err := f.svc.Accept(t.Context(), session("bob"), started.Token, "ana", entry())
	if err != nil || l.Principal != "bob" || replaced != "ana" {
		t.Fatalf("accept with the previous principal = %+v, %q, %v", l, replaced, err)
	}
}

func TestResolveRefusesAPrincipalThatCannotAct(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "42", "")

	p := f.people.byID["ana"]
	p.DisabledAt = f.clock
	f.people.byID["ana"] = p

	if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
		t.Fatalf("resolve for a disabled principal = %v, want ErrNotLinked", err)
	}

	delete(f.people.byID, "ana")

	if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
		t.Fatalf("resolve for a deleted principal = %v, want ErrNotLinked", err)
	}
}

func TestDeleteOwnLinkOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "42", "")

	if err := f.svc.Delete(t.Context(), session("bob"), gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("delete by another person = %v, want ErrNotFound", err)
	}

	if err := f.svc.DeleteFor(t.Context(), "bob", gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("delete for another principal = %v, want ErrNotFound", err)
	}

	links, err := f.svc.List(t.Context(), session("ana"))
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %v, %v", links, err)
	}

	if err := f.svc.Delete(t.Context(), session("ana"), gh("42"), entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := f.svc.DeleteFor(t.Context(), "ana", gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestAuditEntriesNameTheAccount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "ana", "42", "")

	got := f.store.Entries(t.Context())
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}

	e := got[0]
	if e.Namespace != audit.ServerScope || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess ||
		e.Object != principal.Ref("ana") {
		t.Fatalf("entry = %+v", e)
	}

	keys := map[string]string{}
	for _, c := range e.Changes {
		keys[c.Key] = c.New
	}

	for k, want := range map[string]string{
		"forgeLink.kind": "github", "forgeLink.host": "github.com", "forgeLink.accountId": "42",
		"forgeLink.principal": "ana",
	} {
		if keys[k] != want {
			t.Errorf("change %s = %q, want %q", k, keys[k], want)
		}
	}
}

func TestExpiredRequest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	started, err := f.svc.Start(t.Context(), session("ana"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	f.clock = f.clock.Add(forgelink.RequestLifetime)

	if _, err := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); !errors.Is(err, forgelink.ErrExpired) {
		t.Fatalf("prove after the expiry = %v, want ErrExpired", err)
	}

	if _, err := f.svc.Show(t.Context(), session("ana"), started.Token); !errors.Is(err, forgelink.ErrExpired) {
		t.Fatalf("show after the expiry = %v, want ErrExpired", err)
	}
}
