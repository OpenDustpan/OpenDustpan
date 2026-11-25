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
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// errReachSettingRead is the failure of a read of the reach setting.
var errReachSettingRead = errors.New("reach setting read failed")

// reachSettingFixed is a settings port that answers the reach setting with
// the value that the test sets. A test that sets fail answers a failed read,
// so a wrong key fails closed. A test that sets unavailable answers a
// store-unavailable error.
type reachSettingFixed struct {
	value       string
	fail        bool
	unavailable bool
}

func (r reachSettingFixed) Effective(_ context.Context, _ setting.Scope, key string) (setting.Item, error) {
	if key != access.SettingInstallationGrantsReachNamespaces {
		return setting.Item{}, fmt.Errorf("%w: setting %s", errReachSettingRead, key)
	}

	if r.unavailable {
		return setting.Item{}, namespace.ErrUnavailable
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
// namespace route; the body names the action and the kind of the object, not
// the namespace, and the
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
				authzSettings: reachSettingFixed{value: tc.value, fail: false, unavailable: false},
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

// seedReachBot stores the service principal "prn-ns-bot" of the namespace acme.
func seedReachBot(t *testing.T, st *sqlite.Store) {
	t.Helper()

	at := time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC)
	bot := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ns-bot", Subject: "ns-bot",
		DisplayName: "ns-bot", Kind: principal.KindService, Source: principal.SourceLocal,
		Namespace: "acme", GroupCount: 0, BreakGlass: false,
	}
	fence, e := storetest.GrantWrite(t, st, identity.NamespaceObject("acme"), at, principal.Ref(bot.ID))

	if err := st.CreateNamespacePrincipal(t.Context(), bot, fence, e); err != nil {
		t.Fatalf("create namespace principal: %v", err)
	}
}

// TestInstallationReachPrincipalRoutes is the hostile table of the principal
// routes. With the setting false, a caller whose only grant is on the
// installation gets 403 on each route of a principal of a namespace; the body
// names the action and the kind of the object, each refused read and change
// writes one denied entry and no failure entry, and the principal does not
// change. With the setting true, the same calls pass the guard.
func TestInstallationReachPrincipalRoutes(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"false", "true"} {
		for _, rt := range reachRoutes() {
			t.Run(rt.name+" with "+value, func(t *testing.T) {
				t.Parallel()

				f := newAccessFixturePorts(t, accessPorts{
					collections:   nil,
					settings:      nil,
					authzSettings: reachSettingFixed{value: value, fail: false, unavailable: false},
				})
				seedReachBot(t, f.store)

				status, body := callAs(t, f, "ann", rt.call)

				if value == "true" {
					if status != rt.wantStatus {
						t.Fatalf("%s = %d %s, want %d", rt.name, status, body, rt.wantStatus)
					}

					return
				}

				checkReachRouteDenied(t, f, rt, status, body)
			})
		}
	}
}

// reachRoute is one route of the principal of a namespace. wantStatus is the
// answer with the setting true, and action is the action that a refusal
// names.
type reachRoute struct {
	name       string
	call       apiCall
	action     string
	wantStatus int
}

// reachRoutes lists the routes of the principal "prn-ns-bot" of acme.
func reachRoutes() []reachRoute {
	const (
		bot    = "/api/server/principals/prn-ns-bot"
		read   = "server:read-principals"
		manage = "server:manage-principals"
	)

	return []reachRoute{
		{"get", apiCall{http.MethodGet, bot, "", 0}, read, http.StatusOK},
		{"patch", apiCall{http.MethodPatch, bot, `{"displayName":"renamed"}`, 0}, manage, http.StatusOK},
		{"delete", apiCall{http.MethodDelete, bot, "", 0}, manage, http.StatusNoContent},
		{"enable", apiCall{http.MethodPost, bot + "/enable", "", 0}, manage, http.StatusConflict},
		{"disable", apiCall{http.MethodPost, bot + "/disable", "", 0}, manage, http.StatusOK},
		{"groups", apiCall{http.MethodGet, bot + "/groups", "", 0}, read, http.StatusOK},
	}
}

// checkReachRouteDenied checks one refused call: the status 403, the text, the
// entries of ann in @server, and a principal that did not change.
func checkReachRouteDenied(t *testing.T, f accessFixture, rt reachRoute, status int, body string) {
	t.Helper()

	name := rt.name

	if status != http.StatusForbidden {
		t.Fatalf("%s = %d %s, want 403", name, status, body)
	}

	for _, part := range []string{rt.action, "on this namespace"} {
		if !strings.Contains(body, part) {
			t.Fatalf("%s: refusal %q does not name %q", name, body, part)
		}
	}

	if strings.Contains(body, "acme") {
		t.Fatalf("%s: refusal %q names the namespace", name, body)
	}

	checkReachEntries(t, f, rt)
	checkReachBotUnchanged(t, f, name)
}

// checkReachEntries checks the entries of ann in @server after one refusal:
// one denied entry with the action, the principal and the setting as the
// reason, and no failure entry.
func checkReachEntries(t *testing.T, f accessFixture, rt reachRoute) {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	denied, failed := 0, 0

	for i := range listing.Entries {
		e := listing.Entries[i]
		if e.Subject != "ann" {
			continue
		}

		if e.Outcome == audit.OutcomeFailure {
			failed++
		}

		if e.Outcome != audit.OutcomeDenied {
			continue
		}

		denied++

		if e.Action != rt.action || e.Object != principal.Ref("prn-ns-bot") ||
			!strings.Contains(e.Reason, access.SettingInstallationGrantsReachNamespaces) {
			t.Fatalf("%s: denied entry = %+v, want action %s, the principal and the setting", rt.name, e, rt.action)
		}
	}

	if denied != 1 || failed != 0 {
		t.Fatalf("%s: entries of ann in @server: denied %d, failure %d, want 1 and 0", rt.name, denied, failed)
	}
}

// TestInstallationReachNoAccessServiceDenies checks that a handler with no
// access service cannot read the setting and so refuses a principal of a
// namespace (fail closed): 403 and one denied entry with the reason of a
// failed decision.
func TestInstallationReachNoAccessServiceDenies(t *testing.T) {
	t.Parallel()

	svc, st := newTestPrincipalStore(t)

	err := st.CreateNamespace(t.Context(), namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: "acme", PrimaryInstallation: "acme",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	seedReachBot(t, st)

	entries := newTestAudit()
	handler := newAuditedPrincipalHandler(t, svc, entries)

	status, body := call(t, handler, apiCall{http.MethodGet, "/api/server/principals/prn-ns-bot", "", 0})
	if status != http.StatusForbidden {
		t.Fatalf("get with no access service = %d %s, want 403", status, body)
	}

	listing, err := entries.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	denied := 0

	for i := range listing.Entries {
		if e := listing.Entries[i]; e.Outcome == audit.OutcomeDenied && e.Reason == refusalapi.ReasonFailed {
			denied++
		}
	}

	if denied != 1 {
		t.Fatalf("denied entries with reason %q = %d, want 1", refusalapi.ReasonFailed, denied)
	}
}

// checkReachBotUnchanged checks that the principal "prn-ns-bot" is as seeded.
func checkReachBotUnchanged(t *testing.T, f accessFixture, name string) {
	t.Helper()

	got, err := f.store.GetPrincipal(t.Context(), "prn-ns-bot")
	if err != nil {
		t.Fatalf("%s: read principal: %v", name, err)
	}

	if got.DisplayName != "ns-bot" || !got.DisabledAt.IsZero() {
		t.Fatalf("%s: principal changed: %+v", name, got)
	}
}

// TestInstallationReachPrincipalList checks that the list of principals shows
// no principal of a namespace with the setting false, and shows it with true.
func TestInstallationReachPrincipalList(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		value string
		want  bool
	}{{"false", false}, {"true", true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixturePorts(t, accessPorts{
				collections:   nil,
				settings:      nil,
				authzSettings: reachSettingFixed{value: tc.value, fail: false, unavailable: false},
			})
			seedReachBot(t, f.store)

			status, body := callAs(t, f, "ann", apiCall{http.MethodGet, "/api/server/principals", "", 0})
			if status != http.StatusOK {
				t.Fatalf("list = %d %s, want 200", status, body)
			}

			if got := strings.Contains(body, "prn-ns-bot"); got != tc.want {
				t.Fatalf("list %s shows the principal of the namespace = %t, want %t", body, got, tc.want)
			}

			if !strings.Contains(body, "prn-ann") {
				t.Fatalf("list %s does not show the principal of the installation", body)
			}
		})
	}
}

// TestInstallationReachFailedReadWritesOneAuditEntry checks the fail-closed
// rule of the setting at the route: when the read of the setting fails, the
// namespace route refuses the caller and the namespace holds one denied entry
// with the reason of a failed decision.
func TestInstallationReachFailedReadWritesOneAuditEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reader reachSettingFixed
		want   int
	}{
		{
			name:   "a failed read",
			reader: reachSettingFixed{value: "true", fail: true, unavailable: false},
			want:   http.StatusInternalServerError,
		},
		{
			name:   "a store-unavailable read",
			reader: reachSettingFixed{value: "true", fail: false, unavailable: true},
			want:   http.StatusServiceUnavailable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newAccessFixturePorts(t, accessPorts{
				collections:   nil,
				settings:      nil,
				authzSettings: tc.reader,
			})
			f.seed(t, "nia", access.RoleAuditor, identity.ServerObject())

			status, body := callAs(t, f, "nia",
				apiCall{http.MethodGet, "/api/server/namespaces/acme/access-review", "", 0})
			if status != tc.want {
				t.Fatalf("namespace access review = %d %s, want %d", status, body, tc.want)
			}

			if count := countFailedDenials(t, f); count != 1 {
				t.Fatalf("denied entries of nia with reason %q = %d, want 1",
					refusalapi.ReasonFailed, count)
			}
		})
	}
}

// countFailedDenials counts the denied entries of nia in acme with the reason
// of a failed decision.
func countFailedDenials(t *testing.T, f accessFixture) int {
	t.Helper()

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

	return count
}

// checkDeniedNamespaceAction checks the refusal of the namespace route and
// the export that shows the grant. The answer names the action and the kind
// of the object, not the namespace: the coded denial names the kind of the object, never its name,
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

	if strings.Contains(body, "acme") {
		t.Fatalf("refusal %q names the namespace, want the kind of the object only", body)
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
