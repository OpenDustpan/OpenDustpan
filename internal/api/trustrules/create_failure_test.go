package trustrules_test

import (
	"errors"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// createFailureEntries returns the failure entries of a create in the audit
// store of the handler.
func createFailureEntries(t *testing.T, e *env) []audit.Entry {
	t.Helper()

	listing, err := e.audit.List(t.Context(), audit.ServerScope,
		audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		entry := listing.Entries[i]
		if entry.Action == trustrulesvc.ActionCreate && entry.Outcome == audit.OutcomeFailure {
			out = append(out, entry)
		}
	}

	return out
}

// createFailureCase is one create of a rule and what it must write.
type createFailureCase struct {
	name       string
	authz      identity.Authorizer
	setup      func(e *env)
	body       func(e *env) string
	token      string
	wantStatus int
	wantEntry  bool
	wantReason func(t *testing.T, e *env) string
}

// TestRefusedCreateWritesOneFailureEntry checks that a create that a missing
// action refused writes one failure entry with the action, the principal and
// the reason of the refusal, and that any other error writes none.
func TestRefusedCreateWritesOneFailureEntry(t *testing.T) {
	t.Parallel()

	tests := []createFailureCase{
		{
			name:  "a reach refusal",
			authz: allActions(),
			setup: func(e *env) {
				tm := e.principalWithSubject("tm", "", principal.KindPerson)
				e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: tm},
					access.RoleTokenManager, identity.ServerObject())
				e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: e.worker},
					access.RoleWriter, identity.NamespaceObject("acme"))
			},
			body:       func(e *env) string { return ruleBody("deploy", e.worker) },
			token:      personToken,
			wantStatus: http.StatusForbidden,
			wantEntry:  true,
			wantReason: reachFailureReason,
		},
		{
			name: "a missing hold on the principal",
			authz: holdsOnly{actions: []identity.Action{
				identity.ActionNamespaceRead, identity.ActionNamespaceReadAccess,
				identity.ActionNamespaceManageTrustRules,
			}},
			setup:      nil,
			body:       func(e *env) string { return ruleBody("deploy", e.worker) },
			token:      personToken,
			wantStatus: http.StatusForbidden,
			wantEntry:  true,
			wantReason: func(_ *testing.T, e *env) string {
				return "outside the reach of the caller: principal:manage-tokens on principal:" + e.worker
			},
		},
		{
			name:       "a body that is not valid",
			authz:      allActions(),
			setup:      nil,
			body:       func(*env) string { return `{"name":"deploy"}` },
			token:      adminToken,
			wantStatus: http.StatusBadRequest,
			wantEntry:  false,
			wantReason: nil,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			runCreateFailureCase(t, tc)
		})
	}
}

// runCreateFailureCase sends the create of one row and checks its answer and
// its failure entry.
func runCreateFailureCase(t *testing.T, tc *createFailureCase) {
	t.Helper()

	e := newEnv(t, tc.authz)
	if tc.setup != nil {
		tc.setup(e)
	}

	status, body := e.do(http.MethodPost, rulesURL, tc.token, tc.body(e))
	if status != tc.wantStatus {
		t.Fatalf("create = %d %s, want %d", status, body, tc.wantStatus)
	}

	if tc.wantEntry {
		checkCreateFailure(t, e, tc.wantReason)

		return
	}

	if entries := createFailureEntries(t, e); len(entries) != 0 {
		t.Fatalf("failure entries = %+v, want none", entries)
	}
}

// reachFailureReason returns the reason that the reach check of the worker by
// tm refuses with.
func reachFailureReason(t *testing.T, e *env) string {
	t.Helper()

	caller, err := tokenAuthenticator{}.AuthenticateToken(t.Context(), personToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	refused := e.tokens.CheckReach(t.Context(), caller, e.worker)
	reach, ok := errors.AsType[access.ReachRefusal](refused)
	if !ok {
		t.Fatalf("reach check = %v, want a reach refusal", refused)
	}

	return access.MissingActionsReason(reach.MissingActions())
}

// checkCreateFailure checks the one failure entry of a refused create of the
// rule for the worker of e.
func checkCreateFailure(t *testing.T, e *env, wantReason func(t *testing.T, e *env) string) {
	t.Helper()

	entries := createFailureEntries(t, e)
	if len(entries) != 1 {
		t.Fatalf("failure entries = %+v, want one", entries)
	}

	entry := entries[0]

	if entry.Kind != audit.KindChange || entry.Outcome != audit.OutcomeFailure {
		t.Fatalf("entry kind %q outcome %q, want change and failure", entry.Kind, entry.Outcome)
	}

	if entry.Action != trustrulesvc.ActionCreate {
		t.Fatalf("entry action = %q, want %q", entry.Action, trustrulesvc.ActionCreate)
	}

	if entry.Object != principal.Ref(e.worker) {
		t.Fatalf("entry object = %q, want %q", entry.Object, principal.Ref(e.worker))
	}

	if entry.Namespace != "acme" {
		t.Fatalf("entry namespace = %q, want acme", entry.Namespace)
	}

	want := wantReason(t, e)
	if entry.Reason != want {
		t.Fatalf("entry reason = %q, want %q", entry.Reason, want)
	}
}
