package dustpan

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TestTheTrustRuleServiceAsksTheIssuer proves the wiring of the server: the
// service of the trust rules refuses an issuer URL that a namespace set as its
// issuerURL, and an issuer at the issuer base URL, besides the public URL.
func TestTheTrustRuleServiceAsksTheIssuer(t *testing.T) {
	t.Parallel()

	st := openCollectionStore(t)

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Now(), Name: "acme", PrimaryInstallation: "local", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := st.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	set := profile.DefaultIssuerSettings(ns.Name)
	set.IssuerURL = "https://files.example.com/acme"

	entry := audit.Entry{
		At:            time.Now(),
		Namespace:     ns.Name,
		Subject:       "ada",
		Action:        "namespace:manage-issuer",
		Object:        "namespace:acme",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		PrincipalKind: audit.PrincipalKindPerson,
		IDPSubject:    "",
		TokenID:       "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		JobID:         "",
		Reason:        "",
	}

	if _, err := st.Issuer().PutIssuerSettings(t.Context(), set, 0, ns.Fence(), []audit.Entry{entry}); err != nil {
		t.Fatalf("put settings: %v", err)
	}

	nsSvc, err := namespace.NewService(namespace.Config{Store: st, Now: nil, Installation: "local"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	issuer, err := issuersvc.NewService(issuersvc.Config{
		Store: st.Issuer(), Namespaces: st, Sealer: noSealer{}, Authorizer: identity.AdminOnly{},
		Notifier: nil, Fetcher: nil, IssuerBaseURL: "https://issuer.example.com", PublicURL: "https://api.example.com",
		Now: nil, Log: nil,
	})
	if err != nil {
		t.Fatalf("issuer service: %v", err)
	}

	cfg := defaultSettings()
	cfg.namespaceStore = st
	cfg.trustRules.store = st.TrustRules()
	cfg.authorizer = identity.AdminOnly{}
	cfg.publicURL = "https://api.example.com"

	var withServices services
	withServices.namespaces = nsSvc
	withServices.issuer = issuer

	with, err := newTrustRuleService(withServices, cfg)
	if err != nil {
		t.Fatalf("service with an issuer: %v", err)
	}

	var withoutServices services
	withoutServices.namespaces = nsSvc

	without, err := newTrustRuleService(withoutServices, cfg)
	if err != nil {
		t.Fatalf("service without an issuer: %v", err)
	}

	for _, iss := range []string{
		"https://files.example.com/acme", "HTTPS://Files.Example.com:443/acme/", "https://issuer.example.com/oidc/ns-1",
		"https://api.example.com/oidc/ns-1",
	} {
		if err = with.CheckOwnIssuer(t.Context(), iss); !errors.Is(err, trustrule.ErrIssuerRefused) {
			t.Errorf("with an issuer: CheckOwnIssuer(%q) = %v, want ErrIssuerRefused", iss, err)
		}
	}

	if err = with.CheckOwnIssuer(t.Context(), "https://token.actions.example.com"); err != nil {
		t.Errorf("with an issuer: an outside issuer = %v, want nil", err)
	}

	// Without the issuer, the public URL still holds, and the stored issuerURL
	// is not known to the service: the wiring is what carries it.
	err = without.CheckOwnIssuer(t.Context(), "https://api.example.com/oidc/ns-1")
	if !errors.Is(err, trustrule.ErrIssuerRefused) {
		t.Errorf("without an issuer: the public URL = %v, want ErrIssuerRefused", err)
	}

	if err = without.CheckOwnIssuer(t.Context(), "https://files.example.com/acme"); err != nil {
		t.Errorf("without an issuer: the stored issuerURL = %v, want nil (no issuer, no issuerURL to protect)", err)
	}
}

var errNoSealer = errors.New("no sealer")

// noSealer is a sealer for a test that never makes or opens a key.
type noSealer struct{}

func (noSealer) Encrypt(context.Context, secret.Scope, [][]byte) ([][]byte, error) {
	return nil, errNoSealer
}

func (noSealer) Decrypt(context.Context, secret.Scope, [][]byte) ([][]byte, error) {
	return nil, errNoSealer
}
