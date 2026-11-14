package forgelinksvc_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/forgelinksvc"
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
	for id := range p.byID {
		if v := p.byID[id]; v.Subject == subject {
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
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-" + id, Subject: "user:" + id,
		DisplayName: "Display " + id, Kind: principal.KindPerson, Source: "", Namespace: "", GroupCount: 0,
		BreakGlass: false,
	}
}

// callerOf returns a caller with the subject, the kind and the credential.
func callerOf(subject string, kind identity.Kind, credential identity.Credential) identity.Principal {
	var p identity.Principal

	p.Subject, p.Kind, p.Credential = subject, kind, credential

	return p
}

func session(id string) identity.Principal {
	return callerOf("user:"+id, identity.KindPerson, identity.CredentialSession)
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
	svc    *forgelinksvc.Service
	store  *forgelinktest.Mem
	people *people
	clock  time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	f := &fixture{
		svc:    nil,
		store:  forgelinktest.NewMem(),
		people: &people{byID: map[string]principal.Principal{"prn-ana": person("ana"), "prn-bob": person("bob")}},
		clock:  time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
	}

	f.svc = forgelinksvc.NewService(f.store, f.people, func() time.Time { return f.clock }, &counter{n: 0})

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

// link makes the link of the account id for the person ana in a full flow.
func (f *fixture) link(t *testing.T, id, previous string) forgelink.Link {
	t.Helper()

	started, err := f.svc.Start(t.Context(), session("ana"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, proveErr := f.svc.Prove(t.Context(), started.Token, proof(id, "login-"+id)); proveErr != nil {
		t.Fatalf("prove: %v", proveErr)
	}

	l, _, err := f.svc.Accept(t.Context(), session("ana"), started.Token, previous, entry())
	if err != nil {
		t.Fatalf("accept: %v", err)
	}

	return l
}

func TestAccountAloneMakesNoLink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	started, err := f.svc.Start(t.Context(), session("ana"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, proveErr := f.svc.Prove(t.Context(), started.Token, proof("42", "ana-gh")); proveErr != nil {
		t.Fatalf("prove: %v", proveErr)
	}

	if _, resolveErr := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(resolveErr, forgelink.ErrNotLinked) {
		t.Fatalf("resolve = %v, want ErrNotLinked: a proof alone is no link", resolveErr)
	}

	if n := len(f.store.Entries(t.Context())); n != 0 {
		t.Fatalf("audit entries = %d, want none", n)
	}
}

func TestPersonAloneMakesNoLink(t *testing.T) {
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

	if _, resolveErr := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(resolveErr, forgelink.ErrNotLinked) {
		t.Fatalf("resolve = %v, want ErrNotLinked", resolveErr)
	}
}

func TestProofNeedsARequestOfAPerson(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for name, token := range map[string]string{
		"a token that no person started": "tokenZtokenZtokenZtokenZtokenZtokenZtokenZtokenZ",
		"a short token":                  "short",
	} {
		if _, err := f.svc.Prove(t.Context(), token, proof("42", "x")); !errors.Is(err, forgelink.ErrNotFound) {
			t.Errorf("prove with %s = %v, want ErrNotFound", name, err)
		}
	}
}

func TestThePersonCannotNameTheAccount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	l := f.link(t, "42", "")

	if l.Account != gh("42") || l.Principal != "prn-ana" {
		t.Fatalf("link = %+v, want the account of the proof for ana", l)
	}
}

func TestAnotherPersonCannotShowOrAcceptARequest(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	started, err := f.svc.Start(t.Context(), session("ana"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, proveErr := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); proveErr != nil {
		t.Fatalf("prove: %v", proveErr)
	}

	_, showErr := f.svc.Show(t.Context(), session("bob"), started.Token)
	if !errors.Is(showErr, forgelink.ErrNotFound) {
		t.Fatalf("show by another person = %v, want ErrNotFound", showErr)
	}

	_, _, err = f.svc.Accept(t.Context(), session("bob"), started.Token, "", entry())
	if !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("accept by another person = %v, want ErrNotFound", err)
	}
}

// unknownToken is a token that no request holds.
const unknownToken = "tokenZtokenZtokenZtokenZtokenZtokenZtokenZtokenZ"

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
		{"an access token", callerOf("user:ana", identity.KindPerson, identity.CredentialAccessToken)},
		{"no credential", callerOf("user:ana", identity.KindPerson, "")},
		{"a job token", callerOf("user:ana", identity.KindPerson, identity.CredentialJobToken)},
		{"a service principal", callerOf("user:svc", identity.KindService, identity.CredentialSession)},
		{"a disabled person", session("dan")},
		{"a break-glass account", session("gus")},
		{"a service record behind a session", session("svc")},
		{"a subject with no record", session("nobody")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.people.byID["prn-dan"], f.people.byID["prn-gus"], f.people.byID["prn-svc"] = disabled, glass, service

			if _, err := f.svc.Start(t.Context(), tc.caller); !errors.Is(err, forgelink.ErrNotPerson) {
				t.Errorf("start = %v, want ErrNotPerson", err)
			}

			_, _, acceptErr := f.svc.Accept(t.Context(), tc.caller, unknownToken, "", entry())
			if !errors.Is(acceptErr, forgelink.ErrNotPerson) {
				t.Errorf("accept = %v, want ErrNotPerson", acceptErr)
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
	f.link(t, "AbC", "")

	if p, err := f.svc.Resolve(t.Context(), gh("AbC")); err != nil || p.ID != "prn-ana" {
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
	f.link(t, "42", "")

	// The account renames its login. A proof carries the new login. The ID is the same.
	l := f.link(t, "42", "prn-ana")
	if l.Account.ID != "42" || l.Login != "login-42" {
		t.Fatalf("link = %+v", l)
	}

	if p, err := f.svc.Resolve(t.Context(), gh("42")); err != nil || p.ID != "prn-ana" {
		t.Fatalf("resolve = %v, %v", p.ID, err)
	}
}

func TestNoSilentReplace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	started, err := f.svc.Start(t.Context(), session("bob"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, proveErr := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); proveErr != nil {
		t.Fatalf("prove: %v", proveErr)
	}

	view, err := f.svc.Show(t.Context(), session("bob"), started.Token)
	if err != nil {
		t.Fatalf("show: %v", err)
	}

	if view.Current.Principal != "prn-ana" || view.CurrentName != "Display ana" {
		t.Fatalf("show = %+v, want the link of ana", view)
	}

	_, _, err = f.svc.Accept(t.Context(), session("bob"), started.Token, "", entry())
	if !errors.Is(err, forgelink.ErrConflict) {
		t.Fatalf("accept without the previous principal = %v, want ErrConflict", err)
	}

	if p, resolveErr := f.svc.Resolve(t.Context(), gh("42")); resolveErr != nil || p.ID != "prn-ana" {
		t.Fatalf("the link of ana changed: %v, %v", p.ID, resolveErr)
	}

	l, replaced, err := f.svc.Accept(t.Context(), session("bob"), started.Token, "prn-ana", entry())
	if err != nil || l.Principal != "prn-bob" || replaced != "prn-ana" {
		t.Fatalf("accept with the previous principal = %+v, %q, %v", l, replaced, err)
	}
}

func TestResolveRefusesAPrincipalThatCannotAct(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	p := f.people.byID["prn-ana"]
	p.DisabledAt = f.clock
	f.people.byID["prn-ana"] = p

	if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
		t.Fatalf("resolve for a disabled principal = %v, want ErrNotLinked", err)
	}

	delete(f.people.byID, "prn-ana")

	if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
		t.Fatalf("resolve for a deleted principal = %v, want ErrNotLinked", err)
	}
}

func TestDeleteOwnLinkOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	if err := f.svc.Delete(t.Context(), session("bob"), gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("delete by another person = %v, want ErrNotFound", err)
	}

	if err := f.svc.DeleteFor(t.Context(), "prn-bob", gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("delete for another principal = %v, want ErrNotFound", err)
	}

	links, err := f.svc.List(t.Context(), session("ana"))
	if err != nil || len(links) != 1 {
		t.Fatalf("links = %v, %v", links, err)
	}

	if err := f.svc.Delete(t.Context(), session("ana"), gh("42"), entry()); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := f.svc.DeleteFor(t.Context(), "prn-ana", gh("42"), entry()); !errors.Is(err, forgelink.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestAuditEntriesNameTheAccount(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	got := f.store.Entries(t.Context())
	if len(got) != 1 {
		t.Fatalf("entries = %d, want 1", len(got))
	}

	e := got[0]
	if e.Namespace != audit.ServerScope || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess ||
		e.Object != principal.Ref("prn-ana") {
		t.Fatalf("entry = %+v", e)
	}

	keys := map[string]string{}
	for _, c := range e.Changes {
		keys[c.Key] = c.New
	}

	for k, want := range map[string]string{
		"forgeLink.kind": "github", "forgeLink.host": "github.com", "forgeLink.accountId": "42",
		"forgeLink.principal": "prn-ana", "forgeLink.deliveryId": "d-42",
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

// countingStore counts the calls of AcceptRequest, so a test sees whether the
// service reached the store.
type countingStore struct {
	forgelink.Store

	accepts int
}

func (c *countingStore) AcceptRequest(
	ctx context.Context, a forgelink.Acceptance, e audit.Entry,
) (forgelink.Accepted, error) {
	c.accepts++

	return c.Store.AcceptRequest(ctx, a, e)
}

// TestAcceptChecksBeforeItReachesTheStore proves the check of the service: a
// request that no account proved does not reach the store at all, so the store
// is not the only layer that refuses it.
func TestAcceptChecksBeforeItReachesTheStore(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	counting := &countingStore{Store: f.store, accepts: 0}
	svc := forgelinksvc.NewService(counting, f.people, func() time.Time { return f.clock }, &counter{n: 0})

	started, err := svc.Start(t.Context(), session("ana"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	_, _, err = svc.Accept(t.Context(), session("ana"), started.Token, "", entry())
	if !errors.Is(err, forgelink.ErrNotProved) || counting.accepts != 0 {
		t.Fatalf("accept = %v after %d store calls, want ErrNotProved before the store", err, counting.accepts)
	}

	f.clock = f.clock.Add(forgelink.RequestLifetime)

	_, _, err = svc.Accept(t.Context(), session("ana"), started.Token, "", entry())
	if !errors.Is(err, forgelink.ErrExpired) || counting.accepts != 0 {
		t.Fatalf("accept after the expiry = %v after %d store calls, want ErrExpired before the store", err,
			counting.accepts)
	}
}

// TestOnlyAPersonRecordsAreListed proves the check of personRecord: a caller that
// does not act as a person gets nothing, whatever record its subject names.
func TestOnlyAPersonRecordsAreListed(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	for name, caller := range map[string]identity.Principal{
		"a service principal": callerOf("user:ana", identity.KindService, identity.CredentialSession),
		"a workload":          callerOf("user:ana", identity.KindWorkload, identity.CredentialSession),
		"a job token":         callerOf("user:ana", identity.KindPerson, identity.CredentialJobToken),
		"a trust token":       callerOf("user:ana", identity.KindPerson, identity.CredentialTrustToken),
	} {
		links, err := f.svc.List(t.Context(), caller)
		if !errors.Is(err, forgelink.ErrNotPerson) || len(links) != 0 {
			t.Errorf("list for %s = %v, %v, want ErrNotPerson", name, links, err)
		}
	}
}

// TestResolveRefusesAccountsThatCannotAct proves the checks of Resolve, each
// alone: a break-glass account, a service record and a disabled person.
func TestResolveRefusesAccountsThatCannotAct(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(*principal.Principal){
		"a break-glass account": func(p *principal.Principal) { p.BreakGlass = true },
		"a service record":      func(p *principal.Principal) { p.Kind = principal.KindService },
		"a disabled person":     func(p *principal.Principal) { p.DisabledAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) },
	} {
		f := newFixture(t)
		f.link(t, "42", "")

		p := f.people.byID["prn-ana"]
		change(&p)
		f.people.byID["prn-ana"] = p

		if _, err := f.svc.Resolve(t.Context(), gh("42")); !errors.Is(err, forgelink.ErrNotLinked) {
			t.Errorf("resolve for %s = %v, want ErrNotLinked", name, err)
		}
	}
}

// TestReplacedLinkWritesTheLossForTheOldHolder proves that the principal that
// loses a link finds an entry under its own object, with the intent of the
// notice.
func TestReplacedLinkWritesTheLossForTheOldHolder(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.link(t, "42", "")

	started, err := f.svc.Start(t.Context(), session("bob"))
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, proveErr := f.svc.Prove(t.Context(), started.Token, proof("42", "x")); proveErr != nil {
		t.Fatalf("prove: %v", proveErr)
	}

	if _, _, err = f.svc.Accept(t.Context(), session("bob"), started.Token, "prn-ana", entry()); err != nil {
		t.Fatalf("accept: %v", err)
	}

	split := splitLinkEntries(f.store.Entries(t.Context()))
	linked, lost := split.linked, split.lost

	// The new link is under the new holder, and the loss is under the old holder
	// and names the new holder. The two objects differ, and neither is the
	// invalid reference that an ID with no prefix would give.
	if len(linked) != 1 || len(lost) != 1 {
		t.Fatalf("entries of the link %d and of the loss %d, want one each under prn-bob and prn-ana",
			len(linked), len(lost))
	}

	if lost[0].Object != principal.Ref("prn-ana") || principal.Ref("prn-ana") == principal.Ref("prn-bob") {
		t.Fatalf("the loss is under %q, want %q", lost[0].Object, principal.Ref("prn-ana"))
	}

	text := audit.EncodeChanges(lost[0].Changes)
	for _, want := range []string{`"key":"forgeLink.replacedBy","new":"prn-bob"`, `"key":"forgeLink.notice"`, `"new":"42"`} {
		if !strings.Contains(text, want) {
			t.Errorf("entry of the loss lacks %s: %s", want, text)
		}
	}
}

// splitLinkEntries returns the entries of a new link under prn-bob and the
// entries of a replaced link.
// linkEntries holds the entries of a new link and of a replaced link.
type linkEntries struct {
	linked []audit.Entry
	lost   []audit.Entry
}

func splitLinkEntries(all []audit.Entry) linkEntries {
	var out linkEntries

	for i := range all {
		text := audit.EncodeChanges(all[i].Changes)

		switch {
		case strings.Contains(text, `"key":"forgeLink.change","new":"replaced"`):
			out.lost = append(out.lost, all[i])
		case strings.Contains(text, `"key":"forgeLink.change","new":"linked"`) && all[i].Object == principal.Ref("prn-bob"):
			out.linked = append(out.linked, all[i])
		default:
		}
	}

	return out
}
