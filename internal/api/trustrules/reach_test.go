package trustrules_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// hiddenObject is what the refusal of the reach compare writes in place of
// the objects that the caller cannot read.
const hiddenObject = "objects that you cannot read"

// grant stores a current grant of role on obj to subject.
func (e *env) grant(subject access.Subject, role string, obj identity.Object) {
	e.t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	g := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: ident.NewID(access.GrantIDPrefix), Role: role,
		CreatedBy: "admin", Subject: subject,
	}

	fence, entry := storetest.GrantWrite(e.t, e.store, obj, at, access.GrantRef(g))
	if err := e.store.CreateGrant(e.t.Context(), g, fence, entry, nil); err != nil {
		e.t.Fatalf("grant %s on %q to %s: %v", role, obj.String(), subject.String(), err)
	}
}

// errorMessage returns the message of an error answer.
func errorMessage(t *testing.T, body []byte) string {
	t.Helper()

	var got struct {
		Message string `json:"message"`
	}

	decode(t, body, &got)

	return got.Message
}

// TestCreateNeedsTheReachOfThePrincipal proves that a create of a trust
// rule compares the reach of the principal with the reach of the caller,
// as the create of a token does: a caller with the token role on the
// installation only cannot make a rule for a principal that holds a data
// role it does not hold. The refusal keeps the text of the token route,
// and it names no object that the caller cannot read.
func TestCreateNeedsTheReachOfThePrincipal(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	tm := e.principalWithSubject("tm", "", principal.KindPerson)
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: tm}, access.RoleTokenManager, identity.ServerObject())
	e.grant(
		access.Subject{Kind: access.SubjectPrincipal, ID: e.worker},
		access.RoleWriter,
		identity.NamespaceObject("acme"),
	)

	status, body := e.do(http.MethodPost, rulesURL, personToken, ruleBody("deploy", e.worker))
	if status != http.StatusForbidden || errorCode(t, body) != errcode.TokenReachExceeded {
		t.Fatalf("create for a principal outside the reach = %d %s, want 403 %s",
			status, body, errcode.TokenReachExceeded)
	}

	message := errorMessage(t, body)
	if !strings.Contains(message, hiddenObject) {
		t.Fatalf("message = %q, want %q", message, hiddenObject)
	}

	if strings.Contains(message, "acme") {
		t.Fatalf("message = %q names a namespace that the caller cannot read", message)
	}

	// The answer holds the text of the reach refusal, as the token route
	// answers it.
	caller, err := tokenAuthenticator{}.AuthenticateToken(t.Context(), personToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	refusal := e.tokens.CheckReach(t.Context(), caller, e.worker)
	if refusal == nil {
		t.Fatal("the token reach check passes for a principal outside the reach")
	}

	if message != refusal.Error() {
		t.Fatalf("message = %q, want the refusal of the token check %q", message, refusal.Error())
	}

	if got := e.mustList(); len(got) != 0 {
		t.Fatalf("rules = %+v, want none", got)
	}
}

// TestCreatePassesWhenTheReachCoversThePrincipal proves that a caller whose
// reach covers the reach of the principal makes the rule.
func TestCreatePassesWhenTheReachCoversThePrincipal(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	tm := e.principalWithSubject("tm", "", principal.KindPerson)
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: tm}, access.RoleTokenManager, identity.ServerObject())
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: tm}, access.RoleViewer, identity.NamespaceObject("acme"))

	plain := e.principalWithSubject("ci-plain", "acme", principal.KindService)
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: plain}, access.RoleViewer,
		identity.NamespaceObject("acme"))

	status, body := e.do(http.MethodPost, rulesURL, personToken, ruleBody("deploy", plain))
	if status != http.StatusCreated {
		t.Fatalf("create for a principal inside the reach = %d %s, want 201", status, body)
	}

	if got := e.mustList(); len(got) != 1 || got[0].Principal != plain {
		t.Fatalf("rules = %+v, want one rule for %q", got, plain)
	}
}

// TestDirectHolderMakesTheRule proves that a caller that holds the token
// role on the principal itself makes the rule, as it makes the token: the
// hold on the principal decides alone.
func TestDirectHolderMakesTheRule(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	direct := e.principalWithSubject("direct", "", principal.KindPerson)
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: direct}, access.RoleTokenManager,
		identity.PrincipalObject(e.worker))
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: e.worker}, access.RoleWriter,
		identity.NamespaceObject("acme"))

	status, body := e.do(http.MethodPost, rulesURL, directToken, ruleBody("deploy", e.worker))
	if status != http.StatusCreated {
		t.Fatalf("create by a holder on the principal = %d %s, want 201", status, body)
	}

	if got := e.mustList(); len(got) != 1 || got[0].Principal != e.worker {
		t.Fatalf("rules = %+v, want one rule for %q", got, e.worker)
	}
}
