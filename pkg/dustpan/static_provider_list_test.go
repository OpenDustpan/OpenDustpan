package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// staticProviderDoc is the part of a sign-in provider that the tests of the
// static providers read.
type staticProviderDoc struct {
	LDAP     *staticLDAPDoc `json:"ldap"`
	ID       string         `json:"id"`
	Source   string         `json:"source"`
	Issuer   string         `json:"issuer"`
	ClientID string         `json:"clientID"`
	Scopes   []string       `json:"scopes"`
}

// staticLDAPDoc is the part of the LDAP settings that the tests read.
type staticLDAPDoc struct {
	CAFile         string   `json:"caFile"`
	DisabledMask   string   `json:"disabledMask"`
	DisabledValues []string `json:"disabledValues"`
}

// TestStaticProvidersShowTheirEffectiveValues proves that the list of the
// sign-in providers shows the issuer, the client ID and the scopes of a static
// OpenID Connect provider, and the disabled-account settings of a static LDAP
// provider, as the flags set them.
func TestStaticProvidersShowTheirEffectiveValues(t *testing.T) {
	t.Parallel()

	oidc := oidcProvider("corp")
	oidc.Issuer = "https://idp.example"
	oidc.ClientID = "dustpan"
	oidc.Scopes = []string{"profile", "email", "groups"}

	ldap := ldapProvider()
	ldap.DisabledAttribute = "pwdAccountLockedTime"
	ldap.DisabledValues = []string{"000001010000Z"}
	ldap.DisabledMask = 2
	ldap.CAFile = "/etc/dustpan/ldap-ca.pem"

	st := openAuditStore(t)
	opts := append(auditSinkServerOptions(t, st),
		dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithOIDCProvider(oidc),
		dustpan.WithLDAPProvider(ldap))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/sign-in-providers", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var list struct {
		Providers []staticProviderDoc `json:"providers"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s, %v", rec.Code, rec.Body.String(), err)
	}

	byID := map[string]staticProviderDoc{}
	for _, p := range list.Providers {
		byID[p.ID] = p
	}

	checkStaticOIDC(t, byID["corp"])
	checkStaticLDAP(t, byID["corp-ldap"])
}

// checkStaticOIDC fails when corp does not show the effective flags.
func checkStaticOIDC(t *testing.T, corp staticProviderDoc) {
	t.Helper()

	if corp.Source != "static" || corp.Issuer != "https://idp.example" || corp.ClientID != "dustpan" ||
		!slices.Equal(corp.Scopes, []string{"profile", "email", "groups"}) {
		t.Fatalf("corp = %+v, want the issuer, the client ID and the scopes of the flags", corp)
	}
}

// checkStaticLDAP fails when the LDAP provider does not show the effective
// flags.
func checkStaticLDAP(t *testing.T, doc staticProviderDoc) {
	t.Helper()

	if doc.LDAP == nil || doc.LDAP.CAFile != "/etc/dustpan/ldap-ca.pem" || doc.LDAP.DisabledMask != "2" ||
		!slices.Equal(doc.LDAP.DisabledValues, []string{"000001010000Z"}) {
		t.Fatalf("corp-ldap = %+v, want the CA file, the disabled mask and the disabled values of the flags", doc)
	}
}

// TestStaticProviderListHidesIssuerSecrets proves that a static provider with
// user information, a query or a fragment in its issuer never reaches the list
// of the sign-in providers: the server refuses the issuer at start, and the
// error holds none of the secrets of the issuer.
func TestStaticProviderListHidesIssuerSecrets(t *testing.T) {
	t.Parallel()

	oidc := oidcProvider("corp")
	oidc.Issuer = fakecred.URL("https", "user", "pw-secret", "idp.example", "") + "/r?token=tok-secret#frag-secret"

	st := openAuditStore(t)
	opts := append(auditSinkServerOptions(t, st),
		dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithOIDCProvider(oidc))

	_, err := dustpan.NewServer(opts...)
	if err == nil {
		t.Fatal("NewServer accepted an issuer with user information, a query and a fragment")
	}

	if strings.Contains(err.Error(), "secret") {
		t.Fatalf("the error holds a secret: %v", err)
	}
}
