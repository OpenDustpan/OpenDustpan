package accesstoken_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// hostFixture is a token service with a temporary administrator reader,
// over the fixture store. The person of the fixture has a working
// temporary administrator record.
type hostFixture struct {
	fixture

	issuer *accesstoken.HostIssuer
}

func newHostFixture(t *testing.T) hostFixture {
	t.Helper()

	f := newFixture(t, nil)

	svc, err := accesstoken.NewService(accesstoken.Config{
		TempAdmins: f.store,
		Store:      f.store, Principals: f.store, Freshness: nil, Authorizer: nil, Logger: nil,
		Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	f.svc = svc

	temps, err := tempadmin.NewService(tempadmin.Config{Store: f.store, Now: f.clock.Now})
	if err != nil {
		t.Fatalf("temporary administrator service: %v", err)
	}

	in := tempadmin.Input{Subject: f.person.Subject, PrincipalID: f.person.ID, CreatedBy: "operator"}

	if _, err = temps.Create(t.Context(), hostAuditEntry(), in); err != nil {
		t.Fatalf("record temporary administrator: %v", err)
	}

	issuer, err := accesstoken.NewHostIssuer(svc)
	if err != nil {
		t.Fatalf("host issuer: %v", err)
	}

	return hostFixture{fixture: f, issuer: issuer}
}

func hostAuditEntry() audit.Entry {
	e := caller()
	e.Subject = tempadmin.HostSubject
	e.Action = string(identity.ActionServerManageTokens)

	return e
}

// hostCaller is the host caller of the host command.
func hostCaller() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
		Subject: tempadmin.HostSubject, DisplayName: tempadmin.HostSubject, Admin: true,
		TokenID: "", TokenExpiresAt: time.Time{},
	}
}

// TestHostIssuerFirstToken issues the first token and proves its name,
// creator, expiry, use and audit entry.
func TestHostIssuerFirstToken(t *testing.T) {
	t.Parallel()

	f := newHostFixture(t)
	ctx := t.Context()
	expires := f.clock.Now().Add(time.Hour)

	tok, secret, err := f.issuer.IssueFirstToken(ctx, hostAuditEntry(), f.person.ID, expires)
	if err != nil {
		t.Fatalf("issue first token: %v", err)
	}

	if tok.Name != tempadmin.HostTokenName || tok.CreatedBy != tempadmin.HostSubject ||
		tok.PrincipalID != f.person.ID || !tok.ExpiresAt.Equal(expires) {
		t.Fatalf("token = %+v, want the host token of the person that ends at %s", tok, expires)
	}

	if _, authErr := f.svc.AuthenticateToken(ctx, secret.Reveal()); authErr != nil {
		t.Fatalf("authenticate before expiry: %v", authErr)
	}

	requireHostTokenAudit(ctx, t, &f, tok)

	f.clock.Advance(time.Hour + time.Millisecond)

	if _, authErr := f.svc.AuthenticateToken(ctx, secret.Reveal()); !errors.Is(authErr, identity.ErrUnauthenticated) {
		t.Fatalf("authenticate after expiry: err = %v, want ErrUnauthenticated", authErr)
	}
}

// requireHostTokenAudit proves that exactly one audit entry records the
// creation of the host token tok.
func requireHostTokenAudit(ctx context.Context, t *testing.T, f *hostFixture, tok accesstoken.Token) {
	t.Helper()

	listing, err := f.store.List(ctx, audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	var found int

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Action == string(identity.ActionServerManageTokens) && e.Subject == tempadmin.HostSubject &&
			e.Object == accesstoken.TokenRef(f.person.ID, tok.ID) && e.Outcome == audit.OutcomeSuccess {
			found++
		}
	}

	if found != 1 {
		t.Fatalf("audit entries for the host token = %d, want 1 in %+v", found, listing.Entries)
	}
}

// TestHostIssuerRefusals proves that the exception stays narrow: each other
// token the host asks for gets ErrNotOwnToken.
func TestHostIssuerRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, f hostFixture) error
	}{
		{"second token", func(t *testing.T, f hostFixture) error {
			t.Helper()

			_, _, err := f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.person.ID, f.clock.Now().Add(time.Hour),
			)
			if err != nil {
				t.Fatalf("first token: %v", err)
			}

			_, _, err = f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.person.ID, f.clock.Now().Add(time.Hour),
			)

			return err
		}},
		{"token of a service principal", func(t *testing.T, f hostFixture) error {
			t.Helper()

			_, _, err := f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.service.ID, f.clock.Now().Add(time.Hour),
			)

			return err
		}},
		{"person with no temporary administrator record", func(t *testing.T, f hostFixture) error {
			t.Helper()

			other, err := f.principals.CreatePrincipal(t.Context(), caller(),
				principal.Input{Subject: "bob@example.com", DisplayName: "", Kind: principal.KindPerson})
			if err != nil {
				t.Fatalf("create person: %v", err)
			}

			_, _, err = f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), other.ID, f.clock.Now().Add(time.Hour),
			)

			return err
		}},
		{"person made long ago", func(t *testing.T, f hostFixture) error {
			t.Helper()

			f.clock.Advance(accesstoken.HostFirstTokenWindow + time.Minute)

			_, _, err := f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.person.ID, f.clock.Now().Add(time.Hour),
			)

			return err
		}},
		{"expiry beyond the temporary administrator lifetime", func(t *testing.T, f hostFixture) error {
			t.Helper()

			_, _, err := f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.person.ID, f.clock.Now().Add(tempadmin.Lifetime+time.Hour),
			)

			return err
		}},
		{"expiry in the past", func(t *testing.T, f hostFixture) error {
			t.Helper()

			_, _, err := f.issuer.IssueFirstToken(
				t.Context(), hostAuditEntry(), f.person.ID, f.clock.Now().Add(-time.Hour),
			)

			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.run(t, newHostFixture(t))
			if !errors.Is(err, accesstoken.ErrNotOwnToken) || !errors.Is(err, identity.ErrDenied) {
				t.Fatalf("err = %v, want ErrNotOwnToken wrapped with ErrDenied", err)
			}
		})
	}
}

// TestNormalCreateKeepsOwnTokenRule proves that the normal create path has
// no exception: the host caller, an administrator and the person itself get
// ErrNotOwnToken for a token of the temporary administrator, in the
// personal scope that the HTTP routes use.
func TestNormalCreateKeepsOwnTokenRule(t *testing.T) {
	t.Parallel()

	f := newHostFixture(t)

	for name, who := range map[string]identity.Principal{
		"host caller":   hostCaller(),
		"administrator": admin(),
		"other person":  as(f.service),
	} {
		_, _, err := f.svc.Create(t.Context(), who, hostAuditEntry(), accesstoken.ScopePersonal, accesstoken.Input{
			PrincipalID: f.person.ID, Name: tempadmin.HostTokenName, ExpiresAt: f.clock.Now().Add(time.Hour),
		})
		if !errors.Is(err, accesstoken.ErrNotOwnToken) {
			t.Errorf("%s: err = %v, want ErrNotOwnToken", name, err)
		}
	}
}

// TestHostIssuerNeedsReader proves that the issuer fails closed without a
// temporary administrator reader.
func TestHostIssuerNeedsReader(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	if _, err := accesstoken.NewHostIssuer(f.svc); !errors.Is(err, accesstoken.ErrNoTempAdminReader) {
		t.Fatalf("NewHostIssuer without a reader: err = %v, want ErrNoTempAdminReader", err)
	}
}

// TestHostIssuerNotInRoutes proves that no HTTP adapter or other package
// reaches the host issuer: only the host command in cmd/opendustpan uses it.
func TestHostIssuerNotInRoutes(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"../../internal", "../../pkg"} {
		fsys := os.DirFS(root)

		err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
				strings.HasPrefix(path, "accesstoken/") {
				return err
			}

			src, readErr := fs.ReadFile(fsys, path)
			if readErr != nil {
				return fmt.Errorf("read %s: %w", path, readErr)
			}

			if strings.Contains(string(src), "NewHostIssuer") {
				t.Errorf("%s/%s uses the host issuer: only cmd/opendustpan may", root, path)
			}

			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
}
