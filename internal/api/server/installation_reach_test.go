package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	refusalapi "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errReachSettingRead is the failure of a read of the reach setting.
var errReachSettingRead = errors.New("reach setting read failed")

// reachSettingFixed is a settings port that answers the reach setting with
// the value that the test sets. A test that sets fail answers a failed read,
// so a wrong key fails closed.
type reachSettingFixed struct {
	value string
	fail  bool
}

func (r reachSettingFixed) Effective(_ context.Context, _ setting.Scope, key string) (setting.Item, error) {
	if key != access.SettingInstallationGrantsReachNamespaces {
		return setting.Item{}, fmt.Errorf("%w: setting %s", errReachSettingRead, key)
	}

	if r.fail {
		return setting.Item{}, errReachSettingRead
	}

	value := r.value

	return setting.Effective(reachSettingDeclaration(), &value, nil), nil
}

// reachSettingDeclaration returns the declaration of the reach setting.
func reachSettingDeclaration() setting.Declaration {
	for _, d := range access.SettingDeclarations() {
		if d.Key == access.SettingInstallationGrantsReachNamespaces {
			return d
		}
	}

	panic("the declaration of the reach setting is missing")
}

// callAs sends one request of subject and returns its status and body,
// without an assertion of the status.
func callAs(t *testing.T, f accessFixture, subject string, c apiCall) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), c.method, c.path, strings.NewReader(c.body))
	req.Header.Set("Authorization", "Bearer "+subject)

	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)

	return rec.Code, rec.Body.String()
}

// grantSecond writes one more grant of role on obj to the principal
// "prn-<subject>". The grant helper of the fixture derives the grant ID from
// the subject, so a subject with two grants needs this helper for the second.
func grantSecond(t *testing.T, f accessFixture, subject, role string, obj identity.Object) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	grt := access.Grant{
		CreatedAt: at, Condition: nil, Object: obj, ID: "grt-seed-2-" + subject, Role: role, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: "prn-" + subject},
	}
	fence, e := storetest.GrantWrite(t, f.store, obj, at, "grant:"+grt.ID)
	if err := f.store.CreateGrant(t.Context(), grt, fence, e, nil); err != nil {
		t.Fatalf("seed second grant: %v", err)
	}
}

// TestInstallationReach is the hostile case of the reach setting at the HTTP
// handler layer. With the setting false, a caller whose only grant is on the
// installation, with a role that holds a namespace action, gets 403 on a
// namespace route; the body names the action and the namespace, and the
// export of the installation still shows the grant. With the setting true,
// the same route answers 200.
func TestInstallationReach(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		value        string
		wantNsStatus int
	}{
		{name: "the setting is true", value: "true", wantNsStatus: http.StatusOK},
		{name: "the setting is false", value: "false", wantNsStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixturePorts(t, accessPorts{
				collections:   nil,
				settings:      nil,
				authzSettings: reachSettingFixed{value: tc.value, fail: false},
			})
			f.seed(t, "nia", access.RoleAuditor, identity.ServerObject())
			grantSecond(t, f, "nia", access.RoleInstallationOperator, identity.ServerObject())

			status, body := callAs(t, f, "nia",
				apiCall{http.MethodGet, "/api/server/namespaces/acme/access-review", "", 0})
			if status != tc.wantNsStatus {
				t.Fatalf("namespace access review = %d %s, want %d", status, body, tc.wantNsStatus)
			}

			if tc.wantNsStatus != http.StatusForbidden {
				return
			}

			checkDeniedNamespaceAction(t, f, body)
		})
	}
}

// TestInstallationReachFailedReadWritesOneAuditEntry checks the fail-closed
// rule of the setting at the route: when the read of the setting fails, the
// namespace route refuses the caller and the namespace holds one denied entry
// with the reason of a failed decision.
func TestInstallationReachFailedReadWritesOneAuditEntry(t *testing.T) {
	t.Parallel()

	f := newAccessFixturePorts(t, accessPorts{
		collections:   nil,
		settings:      nil,
		authzSettings: reachSettingFixed{value: "true", fail: true},
	})
	f.seed(t, "nia", access.RoleAuditor, identity.ServerObject())

	status, body := callAs(t, f, "nia",
		apiCall{http.MethodGet, "/api/server/namespaces/acme/access-review", "", 0})
	if status == http.StatusOK {
		t.Fatalf("namespace access review = %d %s, want a refusal", status, body)
	}

	listing, err := f.store.List(t.Context(), "acme", audit.Page{Size: audit.DefaultPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list acme entries: %v", err)
	}

	count := 0

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Outcome == audit.OutcomeDenied && e.Subject == "nia" &&
			strings.HasPrefix(e.Reason, refusalapi.ReasonFailed) {
			count++
		}
	}

	if count != 1 {
		t.Fatalf("denied entries of nia with reason %q = %d, want 1 (entries %+v)",
			refusalapi.ReasonFailed, count, listing.Entries)
	}
}

// checkDeniedNamespaceAction checks the refusal of the namespace route and
// the export that shows the grant. The answer names the action and the
// namespace: the coded denial names the kind of the object, never its name,
// so that an answer to a caller without the action shows no object. The
// export of the installation still shows the grant of nia on @server, so a
// person can ask why the action was denied. ann holds server:read-access
// through its grant of account-administrator on @server.
func checkDeniedNamespaceAction(t *testing.T, f accessFixture, body string) {
	t.Helper()

	for _, part := range []string{"namespace:read-access", "on this namespace"} {
		if !strings.Contains(body, part) {
			t.Fatalf("refusal %q does not name %q", body, part)
		}
	}

	var all reviewDoc
	f.as(t, "ann", apiCall{http.MethodGet, "/api/server/access-review", "", http.StatusOK}, &all)

	rows := make([]string, 0, len(all.Grants))
	for i := range all.Grants {
		g := &all.Grants[i]
		rows = append(rows, g.Subject+" "+g.Role+" "+g.Object)
	}

	if !slices.Contains(rows, "principal:prn-nia auditor @server") {
		t.Fatalf("export of the installation = %q, want the grant of nia on @server", rows)
	}
}

// TestInstallationReachSettingPatchNeedsFreshStep checks that the change of
// the reach setting from false to true needs a fresh authentication: an
// access token answers 403 mfa.fresh-step-required, and a console session
// with a young sign-in changes the value.
func TestInstallationReachSettingPatchNeedsFreshStep(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry(access.SettingDeclarations())
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	e := newKeyedSettingsEnvWith(t, reg)

	weaker := `{"access.installationGrantsReachNamespaces":false}`
	stricter := `{"access.installationGrantsReachNamespaces":true}`

	wantSettingsStatus(
		t, e.patch(t, tokenPatch(settingsURL, `"0"`, "key-reach-false", weaker)), http.StatusOK,
	)

	refused := e.patch(t, tokenPatch(settingsURL, `"1"`, "key-reach-true", stricter))
	wantSettingsStatus(t, refused, http.StatusForbidden)

	if !strings.Contains(refused.body, settingsFreshCode) {
		t.Fatalf("body = %s, want the code mfa.fresh-step-required", refused.body)
	}

	fresh := e.patch(t, sessionPatch(`"1"`, stricter, 0))
	wantSettingsStatus(t, fresh, http.StatusOK)

	if fresh.header.Get("ETag") != `"2"` {
		t.Fatalf("ETag = %q, want \"2\"", fresh.header.Get("ETag"))
	}

	got := e.read(t)
	if !isTrue(decodeSettings(t, got)[access.SettingInstallationGrantsReachNamespaces]["value"]) {
		t.Fatalf("body = %s, want the value true", got.body)
	}
}
