package trustrules_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// denialEntries returns the entries of outcome denied that the audit store of
// the handler keeps for a refused change of a rule or a bundle.
func denialEntries(t *testing.T, e *env) []audit.Entry {
	t.Helper()

	listing, err := e.audit.List(t.Context(), audit.ServerScope,
		audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		entry := listing.Entries[i]
		if entry.Outcome == audit.OutcomeDenied && strings.HasPrefix(entry.Action, "trust-") {
			out = append(out, entry)
		}
	}

	return out
}

// checkDenialEntry fails the test unless the handler wrote exactly one
// denied entry of a refused change: the action of the change, the caller, the
// namespace and a reason that holds each text of want. The recorder cuts the
// reason to its budget, so a long reason is checked by its parts. As if
// every other check had failed, the hostile request goes to this layer
// alone.
func checkDenialEntry(t *testing.T, e *env, action, subject string, want ...string) {
	t.Helper()

	entries := denialEntries(t, e)
	if len(entries) != 1 {
		t.Fatalf("denied entries = %d, want one: %+v", len(entries), entries)
	}

	entry := entries[0]

	if entry.Action != action {
		t.Errorf("entry action = %q, want %q", entry.Action, action)
	}

	if entry.Subject != subject {
		t.Errorf("entry subject = %q, want %q", entry.Subject, subject)
	}

	if entry.Namespace != "acme" {
		t.Errorf("entry namespace = %q, want acme", entry.Namespace)
	}

	for _, text := range want {
		if !strings.Contains(entry.Reason, text) {
			t.Errorf("entry reason = %q, want it to hold %q", entry.Reason, text)
		}
	}
}

// TestUpdateRefusalWritesAuditEntry proves that an update that points a rule
// at a principal that the caller may not act as leaves its trace: the answer
// is 403 and one denied entry names the caller, the change and the missing
// action. Without the entry, the same attack through PATCH leaves no trace,
// while the same request as a create is recorded.
func TestUpdateRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	existing := e.mustCreate("guarded")

	// The route action stays held; only principal:manage-tokens on the
	// principal is missing, so the refusal comes from the change itself.
	e.serve(holdsOnly{actions: []identity.Action{
		identity.ActionNamespaceRead, identity.ActionNamespaceReadAccess,
		identity.ActionNamespaceManageTrustRules,
	}}, nil)

	status, body := e.send(http.MethodPatch, rulesURL+"/"+existing.ID, adminToken,
		`{"principal":`+quote(e.worker)+`}`, map[string]string{"If-Match": `"1"`, sessionHeader: "1"})
	if status != http.StatusForbidden {
		t.Fatalf("update = %d %s, want 403", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionUpdate, "ada",
		"outside the reach of the caller: principal:manage-tokens on principal:"+e.worker)
}

// TestUpdateKeepingThePrincipalRefusalNamesTheMissingAction proves that an
// update that keeps the stored principal, for a caller without
// principal:manage-tokens on it, is refused with 403 and that the denied entry
// names the action that the caller lacks. The request names no principal, so
// only the fallback of the failure reason can name the cause.
func TestUpdateKeepingThePrincipalRefusalNamesTheMissingAction(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	existing := e.mustCreate("guarded")

	e.serve(holdsOnly{actions: []identity.Action{
		identity.ActionNamespaceRead, identity.ActionNamespaceReadAccess,
		identity.ActionNamespaceManageTrustRules,
	}}, nil)

	status, body := e.send(http.MethodPatch, rulesURL+"/"+existing.ID, adminToken,
		`{"name":"renamed"}`, map[string]string{"If-Match": `"1"`, sessionHeader: "1"})
	if status != http.StatusForbidden {
		t.Fatalf("update = %d %s, want 403", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionUpdate, "ada", trustrulesvc.ErrManageTokensDenied.Error())
}

// TestUpdateReachRefusalWritesAuditEntry proves that an update whose caller
// does not reach the reach of the principal writes the denied entry with the
// missing actions of the reach check.
func TestUpdateReachRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	existing := e.mustCreate("guarded")

	tm := e.principalWithSubject("tm", "", principal.KindPerson)
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: tm}, access.RoleTokenManager,
		identity.ServerObject())
	e.grant(access.Subject{Kind: access.SubjectPrincipal, ID: e.worker}, access.RoleWriter,
		identity.NamespaceObject("acme"))

	status, body := e.send(http.MethodPatch, rulesURL+"/"+existing.ID, personToken,
		`{"principal":`+quote(e.worker)+`}`, map[string]string{"If-Match": `"1"`, sessionHeader: "1"})
	if status != http.StatusForbidden {
		t.Fatalf("update = %d %s, want 403", status, body)
	}

	// The reason of a reach refusal lists the actions that the caller does
	// not hold, and the recorder cuts it to its budget, so the check names
	// the start of the list and one action of it.
	checkDenialEntry(t, e, trustrulesvc.ActionUpdate, "tm",
		"outside the reach of the caller:", "unit:read on namespace:acme")
}

// TestDeleteRefusalWritesAuditEntry proves that a delete with a stored
// access token writes the denied entry of the refused delete.
func TestDeleteRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	existing := e.mustCreate("guarded")

	status, body := e.send(http.MethodDelete, rulesURL+"/"+existing.ID, storedToken, "", nil)
	if status != http.StatusForbidden || errorCode(t, body) != "trust-rule.sign-in-required" {
		t.Fatalf("delete = %d %s, want 403 trust-rule.sign-in-required", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionDelete, "ada", trustrulesvc.ErrSignInRequired.Error())
}

// TestSaveBundleRefusalWritesAuditEntry proves that a bundle save with no
// console session writes the denied entry of the refused save.
func TestSaveBundleRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	status, body := e.send(http.MethodPost, bundlesURL, adminToken, `{"pem":"x"}`, nil)
	if status != http.StatusForbidden || errorCode(t, body) != "mfa.fresh-step-required" {
		t.Fatalf("save = %d %s, want 403 mfa.fresh-step-required", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionBundleSave, "ada", trustrulesvc.ErrFreshRequired.Error())
}

// TestRotateBundleRefusalWritesAuditEntry proves that a rotation from a
// console session whose sign-in is too old writes the denied entry of the
// refused rotation.
func TestRotateBundleRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()

	status, body := e.send(http.MethodPatch, bundlesURL+"/"+id, adminToken, `{"pem":"x"}`,
		map[string]string{"If-Match": `"1"`, sessionHeader: "old"})
	if status != http.StatusForbidden || errorCode(t, body) != "mfa.fresh-step-required" {
		t.Fatalf("rotate = %d %s, want 403 mfa.fresh-step-required", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionBundleRotate, "ada", trustrulesvc.ErrStaleToken.Error())
}

// TestDeleteBundleVersionRefusalWritesAuditEntry proves that the delete of a
// bundle version with a stored access token writes the denied entry of the
// refused delete.
func TestDeleteBundleVersionRefusalWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())
	id := e.mustSaveBundle()

	status, body := e.send(http.MethodDelete, bundlesURL+"/"+id+"/versions/1", storedToken, "", nil)
	if status != http.StatusForbidden || errorCode(t, body) != "trust-rule.sign-in-required" {
		t.Fatalf("delete version = %d %s, want 403 trust-rule.sign-in-required", status, body)
	}

	checkDenialEntry(t, e, trustrulesvc.ActionBundleDelete, "ada", trustrulesvc.ErrSignInRequired.Error())
}

// TestCrossNamespaceCreateWritesAuditEntry proves the spec scenario "Refuse
// other namespace": a create that names a principal of another namespace is
// refused with 403 and writes one failure entry that names the caller and
// the cross-namespace write.
func TestCrossNamespaceCreateWritesAuditEntry(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allActions())

	status, body := e.admin(http.MethodPost, rulesURL, ruleBody("x", e.stranger))
	if status != http.StatusForbidden || errorCode(t, body) != "trust-rule.principal-refused" {
		t.Fatalf("create = %d %s, want 403 trust-rule.principal-refused", status, body)
	}

	entries := createFailureEntries(t, e)
	if len(entries) != 1 {
		t.Fatalf("failure entries = %d, want one: %+v", len(entries), entries)
	}

	entry := entries[0]

	if entry.Subject != "ada" || entry.Namespace != "acme" || entry.Object != principal.Ref(e.stranger) {
		t.Errorf("entry = %+v, want the refusal of ada in acme for the principal of the other namespace", entry)
	}

	if !strings.Contains(entry.Reason, trustrule.ErrCrossNamespace.Error()) {
		t.Errorf("entry reason = %q, want it to hold %q", entry.Reason, trustrule.ErrCrossNamespace.Error())
	}
}
