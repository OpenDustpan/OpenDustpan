package dustpan

import (
	"crypto/x509"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/trustkeys"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// trustRuleSettings holds the settings of the trust rules: the store, and
// the egress settings of the reads of the keys of an issuer.
type trustRuleSettings struct {
	store trustrule.Store
	cas   *x509.CertPool
	// loopback lets a read of keys reach the loopback interface, for the
	// tests of the server with a fake issuer on the local host.
	loopback bool
}

// WithTrustRules sets the store of the workload trust rules. With it, the
// server serves the trust rules under
// /api/dustpan/v1/namespaces/{namespace}/trust-rules. Without it, the server
// serves no route of the trust rules. The namespace store must keep the
// principals.
func WithTrustRules(st trustrule.Store) Option {
	return func(o *settings) { o.trustRules.store = st }
}

// WithTrustRuleKeysLoopbackForTests lets the reads of the keys of an issuer
// reach the loopback interface, so that a test can run a fake issuer on the
// local host. A server in production does not set it: no flag or
// environment variable reaches it.
func WithTrustRuleKeysLoopbackForTests() Option {
	return func(o *settings) { o.trustRules.loopback = true }
}

// WithTrustRuleKeysRootCAs sets the root certificates that the reads of the
// keys of an issuer trust. Nil, the default, uses the roots of the system.
func WithTrustRuleKeysRootCAs(pool *x509.CertPool) Option {
	return func(o *settings) { o.trustRules.cas = pool }
}

// newTrustRuleHandler builds the trust rule API. The caller builds it only
// when the server has a trust rule store.
func newTrustRuleHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	principals, ok := cfg.namespaceStore.(trustrulesvc.Principals)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no principals for the trust rules: %w", ErrMissingOption)
	}

	rules := egress.Rules{Allow: nil, LoopbackForTests: cfg.trustRules.loopback}

	ruleSvc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: cfg.trustRules.store, Namespaces: svc.namespaces, Principals: principals, Authorizer: cfg.authorizer,
		InstallationURLs: []string{cfg.publicURL},
		CheckURL:         func(raw string) error { return rules.CheckURL(raw, egress.HTTPSOnly) },
		Now:              cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("trust rule service: %w", err)
	}

	// The server serves the exchange only when it keeps access tokens and
	// the trust rule store keeps the exchanges.
	var exchanger *trustrulesvc.Exchanger

	if exchanges, ok := cfg.trustRules.store.(trustrule.ExchangeStore); ok && svc.tokens != nil {
		exchanger, err = newExchanger(svc, cfg, principals, exchanges, rules)
		if err != nil {
			return nil, err
		}
	}

	handler, err := trustrules.NewHandler(trustrules.Config{
		Namespaces:     svc.namespaces,
		Rules:          ruleSvc,
		Exchanger:      exchanger,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
		Now:            cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("trust rule api: %w", err)
	}

	return handler, nil
}

// newExchanger builds the token exchange over the exchange store exchanges.
// The keys of a discovery rule come through the egress rules rules.
func newExchanger(
	svc services, cfg *settings, principals trustrulesvc.Principals, exchanges trustrule.ExchangeStore,
	rules egress.Rules,
) (*trustrulesvc.Exchanger, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	x, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: cfg.trustRules.store, Exchanges: exchanges, Namespaces: svc.namespaces, Principals: principals,
		Tokens: svc.tokens, Audit: neutralAudit, Refusals: nil,
		Keys:             trustkeys.New(trustkeys.Config{Rules: rules, RootCAs: cfg.trustRules.cas, Now: cfg.now}),
		InstallationURLs: []string{cfg.publicURL}, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}

	return x, nil
}
