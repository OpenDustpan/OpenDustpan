package dustpan

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// WithTrustRules sets the store of the workload trust rules. With it, the
// server serves the trust rules under
// /api/dustpan/v1/namespaces/{namespace}/trust-rules. Without it, the server
// serves no route of the trust rules. The namespace store must keep the
// principals.
func WithTrustRules(st trustrule.Store) Option {
	return func(o *settings) { o.trustRules = st }
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

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: cfg.trustRules, Namespaces: svc.namespaces, Principals: principals, Authorizer: cfg.authorizer,
		InstallationURLs: []string{cfg.publicURL}, CheckURL: nil, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("trust rule service: %w", err)
	}

	handler, err := trustrules.NewHandler(trustrules.Config{
		Namespaces:     svc.namespaces,
		Rules:          rules,
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
