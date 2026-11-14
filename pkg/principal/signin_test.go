package principal_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// signInFixture is a service over a store that a test can also write to.
type signInFixture struct {
	store *sqlite.Store
	svc   *principal.Service
}

func newSignInFixture(t *testing.T) signInFixture {
	t.Helper()

	st := openStore(t)

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: nil,
			Store:      st,
			Guard:      principal.AdminOnlyGuard{},
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return signInFixture{store: st, svc: svc}
}

// put stores p as it is, with the store, so a test can make a principal
// that the service never makes.
func (f signInFixture) put(t *testing.T, p principal.Principal) {
	t.Helper()

	e := testEntry()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = p.CreatedAt, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, principal.Ref(p.ID)

	if err := f.store.CreatePrincipal(t.Context(), p, e); err != nil {
		t.Fatalf("create principal %s: %v", p.Subject, err)
	}
}

// stored returns a person principal with the subject, as the store keeps it.
func stored(id, subject string) principal.Principal {
	at := time.Date(2026, time.October, 16, 9, 0, 0, 0, time.UTC)

	return principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: id, Subject: subject, DisplayName: subject,
		Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}
}

func signIn(subject string, create bool, names ...string) principal.SignInInput {
	return principal.SignInInput{Subject: subject, Source: "", Names: names, Create: create}
}

func TestSignInPrincipalCreates(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	p, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:248289761001", true, "Alice Doe"))
	if err != nil || !created {
		t.Fatalf("SignInPrincipal = created %v, %v; want a new principal", created, err)
	}

	if p.Kind != principal.KindPerson || p.Source != principal.SourceOIDC || p.DisplayName != "Alice Doe" ||
		p.Subject != "corp:248289761001" || p.GroupCount != 0 || p.BreakGlass {
		t.Errorf("principal = %+v, want an oidc person named Alice Doe", p)
	}

	again, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:248289761001", true, "Other"))
	if err != nil || created || again.ID != p.ID {
		t.Fatalf("second SignInPrincipal = %s, created %v, %v; want %s, not created", again.ID, created, err, p.ID)
	}
}

// TestSignInPrincipalSource proves that the first sign-in through a plain
// OAuth 2.0 provider makes a principal with the source oauth.
func TestSignInPrincipalSource(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)
	in := signIn("hub:12345678901", true, "Mona")
	in.Source = principal.SourceOAuth

	p, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), in)
	if err != nil || !created || p.Source != principal.SourceOAuth {
		t.Fatalf("SignInPrincipal = %+v, created %v, %v; want a new principal with the source oauth", p, created, err)
	}
}

func TestSignInPrincipalWritesAudit(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	p, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:1", true))
	if err != nil {
		t.Fatalf("SignInPrincipal: %v", err)
	}

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: 10})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	for _, e := range listing.Entries {
		if e.Object == principal.Ref(p.ID) && e.Outcome == audit.OutcomeSuccess {
			return
		}
	}

	t.Fatalf("no audit entry for %s in %+v", principal.Ref(p.ID), listing.Entries)
}

func TestSignInPrincipalWithoutCreate(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	_, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:1", false))
	if !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("SignInPrincipal error = %v, want ErrNotFound", err)
	}

	if _, getErr := f.store.GetPrincipalBySubject(t.Context(), "corp:1"); !errors.Is(getErr, principal.ErrNotFound) {
		t.Fatalf("a refused sign-in made a principal: %v", getErr)
	}

	// A principal that an administrator made first signs in.
	f.put(t, stored("prn-made", "corp:2"))

	p, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:2", false))
	if err != nil || created || p.ID != "prn-made" {
		t.Fatalf("SignInPrincipal = %s, created %v, %v; want prn-made", p.ID, created, err)
	}
}

// disabledStore answers every principal with a disable time, as the
// inactivity sweep leaves it.
type disabledStore struct {
	*sqlite.Store
}

func (s disabledStore) GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error) {
	p, err := s.Store.GetPrincipalBySubject(ctx, subject)
	p.DisabledAt = p.CreatedAt.Add(time.Hour)

	return p, err
}

func TestSignInPrincipalRefusesDisabled(t *testing.T) {
	t.Parallel()

	st := openStore(t)

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store: disabledStore{
			Store: st,
		},
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		GroupReach: nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	signInFixture{store: st, svc: svc}.put(t, stored("prn-disabled", "corp:disabled"))

	for _, create := range []bool{true, false} {
		_, created, err := svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:disabled", create))
		if !errors.Is(err, principal.ErrDisabled) || created {
			t.Errorf("SignInPrincipal(create %v) = created %v, %v; want ErrDisabled", create, created, err)
		}
	}
}

func TestSignInPrincipalRefusesTargets(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	breakGlass := stored("prn-break", "corp:break")
	breakGlass.BreakGlass = true

	service := stored("prn-svc", "corp:svc")
	service.Kind = principal.KindService

	for _, p := range []principal.Principal{breakGlass, service} {
		f.put(t, p)
	}

	for _, tc := range []struct {
		subject string
		want    error
	}{
		{subject: "corp:break", want: principal.ErrNotSignInTarget},
		{subject: "corp:svc", want: principal.ErrNotSignInTarget},
	} {
		for _, create := range []bool{true, false} {
			_, created, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(tc.subject, create))
			if !errors.Is(err, tc.want) || created {
				t.Errorf(
					"SignInPrincipal(%s, create %v) = created %v, %v; want %v",
					tc.subject,
					create,
					created,
					err,
					tc.want,
				)
			}
		}
	}
}

func TestSignInPrincipalRefusesSubject(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for _, subject := range []string{"", "corp:has space", "corp:" + strings.Repeat("x", 300), "admin"} {
		_, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(subject, true))
		if !errors.Is(err, principal.ErrInvalid) && !errors.Is(err, principal.ErrReservedSubject) {
			t.Errorf("SignInPrincipal(%q) error = %v, want ErrInvalid or ErrReservedSubject", subject, err)
		}
	}
}

func TestSignInPrincipalDisplayName(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	for i, tc := range []struct {
		names []string
		want  string
	}{
		{names: []string{"Alice Doe", "alice"}, want: "Alice Doe"},
		{names: []string{"", "alice", "alice@example.com"}, want: "alice"},
		{names: []string{"bad\x00name", "", "alice@example.com"}, want: "alice@example.com"},
		{names: []string{strings.Repeat("n", 300)}, want: "corp:name-3"},
		{names: nil, want: "corp:name-4"},
	} {
		subject := "corp:name-" + string(rune('0'+i))

		p, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn(subject, true, tc.names...))
		if err != nil {
			t.Fatalf("SignInPrincipal(%s): %v", subject, err)
		}

		if p.DisplayName != tc.want {
			t.Errorf("display name of %v = %q, want %q", tc.names, p.DisplayName, tc.want)
		}
	}
}

func TestSignInPrincipalParallelFirstSignIn(t *testing.T) {
	t.Parallel()

	f := newSignInFixture(t)

	const callers = 8

	ids := make([]string, callers)
	errs := make([]error, callers)

	var wg sync.WaitGroup

	for i := range callers {
		wg.Go(func() {
			p, _, err := f.svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:race", true))
			ids[i], errs[i] = p.ID, err
		})
	}

	wg.Wait()

	for i := range callers {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("caller %d = %s, %v; want %s", i, ids[i], errs[i], ids[0])
		}
	}
}

// TestSignInRefusesTempAdmin proves that a sign-in through an identity
// provider cannot name a temporary administrator, while another person
// with no record signs in.
func TestSignInRefusesTempAdmin(t *testing.T) {
	t.Parallel()

	st := openStore(t)

	svc, err := principal.NewService(
		principal.Config{
			TempAdmins: st,
			Store:      st,
			Guard:      principal.AdminOnlyGuard{},
			Namespaces: nil,
			Tokens:     nil,
			GroupReach: nil,
			Now:        nil,
			NewID:      nil,
		},
	)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	f := signInFixture{store: st, svc: svc}
	temp := stored("prn-temp", "corp:temp")
	f.put(t, temp)
	f.put(t, stored("prn-plain", "corp:plain"))

	rec := tempadmin.TempAdmin{
		CreatedAt:      temp.CreatedAt,
		ExpiresAt:      temp.CreatedAt.Add(time.Hour),
		EndedAt:        time.Time{},
		ReviewOpenedAt: time.Time{},
		PrincipalID:    temp.ID,
		Subject:        temp.Subject,
		CreatedBy:      "operator",
		Reason:         "every administrator lost access",
	}

	e := testEntry()
	e.At, e.Namespace, e.Kind, e.Outcome, e.Object = rec.CreatedAt, audit.ServerScope, audit.KindChange,
		audit.OutcomeSuccess, tempadmin.Ref(temp.ID)

	if err := st.CreateTempAdmin(t.Context(), rec, e); err != nil {
		t.Fatalf("record temporary administrator: %v", err)
	}

	if _, _, err := svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:temp", true)); !errors.Is(
		err, principal.ErrNotSignInTarget,
	) {
		t.Fatalf("sign-in of the temporary administrator: err = %v, want ErrNotSignInTarget", err)
	}

	if _, _, err := svc.SignInPrincipal(t.Context(), testEntry(), signIn("corp:plain", false)); err != nil {
		t.Fatalf("sign-in of a person with no record: %v", err)
	}
}
