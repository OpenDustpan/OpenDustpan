package accesstoken_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
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
		Store:      f.store, Principals: f.store, Freshness: nil, Authorizer: nil,
		Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	})
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	f.svc = svc

	temps, err := tempadmin.NewService(tempadmin.Config{Store: f.store, Reviews: nil, Now: f.clock.Now, Logger: nil})
	if err != nil {
		t.Fatalf("temporary administrator service: %v", err)
	}

	in := tempadmin.Input{
		Subject: f.person.Subject, PrincipalID: f.person.ID, CreatedBy: "operator", Reason: hostReason,
	}

	if _, err = temps.Create(t.Context(), hostAuditEntry(), in); err != nil {
		t.Fatalf("record temporary administrator: %v", err)
	}

	issuer, err := accesstoken.NewHostIssuer(svc, f.store)
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
		Kind:       "",
		Credential: "",
		JobID:      "",
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

	if _, err := accesstoken.NewHostIssuer(f.svc, f.store); !errors.Is(err, accesstoken.ErrNoTempAdminReader) {
		t.Fatalf("NewHostIssuer without a reader: err = %v, want ErrNoTempAdminReader", err)
	}
}

// TestHostIssuerOnlyInHostCommand proves that no package of the module
// but the host command in cmd/opendustpan reaches the host issuer, and that
// no package but this one and its conformance suite calls
// CreateHostRecovery. It lists every package
// with go list and reads each non-test file of each one.
func TestHostIssuerOnlyInHostCommand(t *testing.T) {
	t.Parallel()

	const (
		self  = "opendustpan.org/opendustpan/pkg/accesstoken"
		suite = self + "/accesstokentest"
		host  = "opendustpan.org/opendustpan/cmd/opendustpan"
		lines = "{{.ImportPath}}|{{.Dir}}|{{join .GoFiles \",\"}}"
	)

	out, err := exec.CommandContext(t.Context(), "go", "list", "-f", lines, "opendustpan.org/opendustpan/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 || parts[0] == self || parts[2] == "" {
			continue
		}

		for name := range strings.SplitSeq(parts[2], ",") {
			uses := hostUsesOf(t, filepath.Join(parts[1], name), self)

			if uses.issuer && parts[0] != host {
				t.Errorf("%s/%s uses the host issuer: only %s may", parts[0], name, host)
			}

			if uses.recovery && parts[0] != suite {
				t.Errorf("%s/%s calls CreateHostRecovery: only the host issuer of %s may", parts[0], name, self)
			}
		}
	}
}

// hostUses holds what one Go file names of the host path: the host issuer
// of the package accesstoken, and the store method CreateHostRecovery.
type hostUses struct {
	issuer   bool
	recovery bool
}

// hostUsesOf reports whether the Go file path names HostIssuer or
// NewHostIssuer of the package self, and whether it names a method or a
// field CreateHostRecovery of any value.
func hostUsesOf(t *testing.T, path, self string) hostUses {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	alias := ""

	for _, spec := range file.Imports {
		if strings.Trim(spec.Path.Value, "\"") != self {
			continue
		}

		alias = "accesstoken"
		if spec.Name != nil {
			alias = spec.Name.Name
		}
	}

	var uses hostUses

	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		if sel.Sel.Name == "CreateHostRecovery" {
			uses.recovery = true
		}

		if x, isIdent := sel.X.(*ast.Ident); isIdent && alias != "" && x.Name == alias &&
			(sel.Sel.Name == "HostIssuer" || sel.Sel.Name == "NewHostIssuer") {
			uses.issuer = true
		}

		return true
	})

	return uses
}

// TestHostIssuerRecover makes a temporary administrator in one call and
// proves the person, the record with its reason, the shared expiry and the
// token.
func TestHostIssuerRecover(t *testing.T) {
	t.Parallel()

	f := newHostFixture(t)
	ctx := t.Context()

	got, err := f.issuer.Recover(ctx, hostAuditEntry(), accesstoken.HostInput{
		Subject: "carol@example.com", Reason: hostReason, CreatedBy: "operator",
	})
	if err != nil {
		t.Fatalf("recover: %v", err)
	}

	if got.Person.Subject != "carol@example.com" || got.Record.Reason != hostReason ||
		!got.Token.ExpiresAt.Equal(got.Record.ExpiresAt) || got.Token.PrincipalID != got.Person.ID {
		t.Fatalf("recovery = %+v, want carol with the reason and one shared expiry", got)
	}

	if want := f.clock.Now().Add(tempadmin.Lifetime); !got.Record.ExpiresAt.Equal(want) {
		t.Fatalf("record expires at %s, want %s", got.Record.ExpiresAt, want)
	}

	if _, authErr := f.svc.AuthenticateToken(ctx, got.Secret.Reveal()); authErr != nil {
		t.Fatalf("authenticate the recovery token: %v", authErr)
	}

	_, err = f.issuer.Recover(ctx, hostAuditEntry(), accesstoken.HostInput{
		Subject: "carol@example.com", Reason: hostReason, CreatedBy: "operator",
	})
	if !errors.Is(err, principal.ErrAlreadyExists) {
		t.Fatalf("second recovery of carol: err = %v, want principal.ErrAlreadyExists", err)
	}
}

// hostReason is the reason of the recoveries of the tests.
const hostReason = "every administrator lost access"
