package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// seedReachGrant writes the grant of role on obj to the principal
// "prn-<subject>" with the ID id, so two grants to one principal do not
// collide.
func (f accessFixture) seedReachGrant(t *testing.T, id, subject, role string, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: id, Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-" + subject},
	}
	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grt.ID)
	if err := f.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed grant: %v", err)
	}
}

// seededReachFixture returns the fixture with the service principal
// "prn-svc-mixed", which holds an operator action and a namespace action,
// and the person "gm", who holds a custom role with no data action, so it
// cannot read the namespace of the service principal.
func seededReachFixture(t *testing.T) accessFixture {
	t.Helper()

	f := newAccessFixture(t)
	f.seedService(t, "svc-mixed")
	f.seedReachGrant(t, "grt-svc-ops", "svc-mixed", access.RoleInstallationOperator, identity.ServerObject())
	f.seedReachGrant(t, "grt-svc-viewer", "svc-mixed", access.RoleViewer, identity.NamespaceObject("acme"))
	f.seed(t, "gm", "", identity.Object{})

	var role struct {
		ID string `json:"id"`
	}
	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/roles",
		`{"name":"reach-checker","actions":["server:manage-access","server:manage-groups",` +
			`"principal:manage-tokens"]}`,
		http.StatusCreated,
	}, &role)

	f.as(t, "admin", apiCall{
		http.MethodPost, "/api/server/grants",
		`{"subject":"principal:prn-gm","role":"` + role.ID + `"}`, http.StatusCreated,
	}, nil)

	return f
}

// auditEntryOf returns the entry of the store with the subject, action and
// outcome, and fails unless there is exactly one.
func auditEntryOf(
	t *testing.T, f accessFixture, subject string, action identity.Action, outcome audit.Outcome,
) audit.Entry {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	var found []audit.Entry

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Subject == subject && e.Action == string(action) && e.Outcome == outcome {
			found = append(found, e)
		}
	}

	if len(found) != 1 {
		t.Fatalf("entries of %s for %s = %+v, want one", subject, action, found)
	}

	return found[0]
}

// checkReachAuditReason checks that reason names the missing actions of the
// service principal, including the namespace that the caller cannot read.
func checkReachAuditReason(t *testing.T, reason string) {
	t.Helper()

	for _, want := range []string{
		"outside the reach of the caller: ",
		"server:backup on the installation",
		"namespace:read on namespace:acme",
	} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q does not hold %q", reason, want)
		}
	}

	if strings.Contains(reason, "objects that you cannot read") {
		t.Errorf("reason %q hides the object from the audit log", reason)
	}
}

// TestTokenReachRefusalAudit proves that a refused create and a refused
// rotation of a service token write the missing actions with their objects
// in the audit entry, while the answer of the request names no object that
// the caller cannot read.
func TestTokenReachRefusalAudit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		rotate bool
	}{
		{name: "a create", rotate: false},
		{name: "a rotation", rotate: true},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := seededReachFixture(t)
			f.seed(t, "tm", access.RoleTokenManager, identity.ServerObject())

			path := "/api/server/principals/prn-svc-mixed/tokens"
			body := `{"name":"ci"}`

			if tc.rotate {
				var made tokenDoc
				f.as(t, "admin", apiCall{http.MethodPost, path, body, http.StatusCreated}, &made)
				path += "/" + made.ID + "/rotate"
				body = `{}`
			}

			rec := f.postWithSessionOf(t, "tm", path, body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), http.StatusForbidden)
			}

			got := decodeErrorBody(t, rec)
			if got.ErrorCode != errcode.TokenReachExceeded {
				t.Fatalf("code = %q, want %q", got.ErrorCode, errcode.TokenReachExceeded)
			}

			if strings.Contains(got.Message, "acme") ||
				!strings.Contains(got.Message, "objects that you cannot read") {
				t.Fatalf("message = %q, want no readable object and the hidden object text", got.Message)
			}

			e := auditEntryOf(t, f, "tm", identity.ActionPrincipalManageTokens, audit.OutcomeFailure)
			checkReachAuditReason(t, e.Reason)
		})
	}
}

// TestGrantReachRefusalAudit proves that a refused grant of a delegation
// role on a service principal writes the missing actions with their objects
// in the audit entry, while the answer names no object that the caller
// cannot read.
func TestGrantReachRefusalAudit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "a grant of a delegation role on a service principal"},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := seededReachFixture(t)

			rec := f.postWithSessionOf(t, "gm", "/api/server/principals/prn-svc-mixed/grants",
				`{"subject":"principal:prn-bob","role":"token-manager"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), http.StatusForbidden)
			}

			got := decodeErrorBody(t, rec)
			if strings.Contains(got.Message, "acme") {
				t.Fatalf("message = %q, want no object that the caller cannot read", got.Message)
			}

			e := auditEntryOf(t, f, "gm", identity.ActionServerManageAccess, audit.OutcomeDenied)
			checkReachAuditReason(t, e.Reason)
		})
	}
}

// TestMemberAddReachRefusalAudit proves that a refused membership add to a
// group whose grants reach past the caller writes the missing actions with
// their objects in the audit entry, while the answer names no object that
// the caller cannot read.
func TestMemberAddReachRefusalAudit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{name: "an add to a group that acts as a service principal outside the reach"},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := seededReachFixture(t)

			var group struct {
				ID string `json:"id"`
			}
			f.as(t, "admin", apiCall{
				http.MethodPost, "/api/server/groups", `{"name":"ops"}`, http.StatusCreated,
			}, &group)
			f.as(t, "admin", apiCall{
				http.MethodPost, "/api/server/principals/prn-svc-mixed/grants",
				`{"subject":"group:` + group.ID + `#member","role":"token-manager"}`, http.StatusCreated,
			}, nil)

			rec := f.postWithSessionOf(t, "gm", "/api/server/groups/"+group.ID+"/members",
				`{"principalID":"prn-bob"}`)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), http.StatusForbidden)
			}

			got := decodeErrorBody(t, rec)
			if strings.Contains(got.Message, "acme") {
				t.Fatalf("message = %q, want no object that the caller cannot read", got.Message)
			}

			e := auditEntryOf(t, f, "gm", identity.ActionServerManageGroups, audit.OutcomeFailure)
			checkReachAuditReason(t, e.Reason)
		})
	}
}
