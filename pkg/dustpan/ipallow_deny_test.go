package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/setting"
)

// TestDenyConsoleSignInOutsideList proves that an address outside the
// installation list is refused at the provider password sign-in route before
// the password is checked.
func TestDenyConsoleSignInOutsideList(t *testing.T) {
	t.Parallel()

	s := newLDAPServer(t)
	enableInstallationList(t, s.settings, "198.51.100.0/24")

	rec := s.signIn(t, "tls-ldap", ldapAlicePassword)

	checkAllowlistRefusal(t, rec, "installation")
}

// TestDenyTokenOutsideList proves that an access token request from an
// address outside the installation list is refused on the units route.
func TestDenyTokenOutsideList(t *testing.T) {
	t.Parallel()

	s := newLDAPServer(t)

	create := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/api/server/namespaces", strings.NewReader(`{"name":"acme"}`),
	)
	create.Header.Set("Authorization", "Bearer "+adminToken)
	create.Header.Set("Content-Type", "application/json")

	if rec := s.serve(create); rec.Code != http.StatusCreated {
		t.Fatalf("create the namespace = %d %s, want 201", rec.Code, rec.Body.String())
	}

	enableInstallationList(t, s.settings, "198.51.100.0/24")

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units", http.NoBody,
	)
	req.Header.Set("Authorization", "token "+adminToken)

	checkAllowlistRefusal(t, s.serve(req), "installation")
}

// enableInstallationList turns on the installation list in the settings
// store that the server reads. It writes both keys in one change, so the
// feature never runs with an empty list.
func enableInstallationList(t *testing.T, store setting.Store, list string) {
	t.Helper()

	reg, err := setting.NewRegistry(ipallow.SettingDeclarations())
	if err != nil {
		t.Fatalf("setting registry: %v", err)
	}

	svc := setting.NewService(store, reg, nil, nil, time.Now)

	stored, err := svc.Get(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("read the settings: %v", err)
	}

	_, err = svc.Patch(t.Context(), setting.InstallationScope(), setting.Patch{
		{Key: ipallow.SettingEnabled, Value: "true", Delete: false},
		{Key: ipallow.SettingInstallation, Value: list, Delete: false},
	}, setting.PatchRequest{
		Entry: audit.Entry{
			At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "", Action: "",
			Object: string(audit.ServerScope), Version: "", ValuePath: "", ValueCount: nil, Changes: nil,
			SourceIP: "192.0.2.1", UserAgent: "test", Channel: audit.ChannelAPI, Kind: "", Outcome: "",
			RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "",
			PrincipalKind: "", JobID: "", Origin: nil,
		},
		IfMatch: stored.Revision,
		Fresh:   true,
	})
	if err != nil {
		t.Fatalf("enable the installation list: %v", err)
	}
}

// checkAllowlistRefusal fails unless rec is a 403 with the allowlist code and
// the scope that refused.
func checkAllowlistRefusal(t *testing.T, rec *httptest.ResponseRecorder, scope string) {
	t.Helper()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("request = %d %s, want 403", rec.Code, rec.Body.String())
	}

	var answer struct {
		Message   string `json:"message"`
		Code      int    `json:"code"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode the refusal: %v: %s", err, rec.Body.String())
	}

	if answer.Code != http.StatusForbidden || answer.ErrorCode != "access.ip-not-allowed" ||
		!strings.HasSuffix(answer.Message, "of the "+scope) {
		t.Fatalf("refusal = %s, want 403 access.ip-not-allowed that names the scope %s", rec.Body.String(), scope)
	}
}
