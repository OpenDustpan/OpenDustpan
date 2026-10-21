package dustpan

import (
	"crypto/x509"
	"fmt"
	"net/netip"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
	"opendustpan.org/opendustpan/pkg/secret"
)

// gitHostSettings holds the settings of the Git host connections: the store,
// the address ranges that a connection may reach in addition to public
// addresses, and the root certificates that it trusts.
type gitHostSettings struct {
	store forge.Store
	allow []netip.Prefix
	cas   *x509.CertPool
}

// WithGitHostStore sets the store of the Git host connections. With it, and
// with a namespace store that keeps installation secret keys and audit
// entries, the server serves the Git host routes. Without it, the server
// serves none.
func WithGitHostStore(st forge.Store) Option {
	return func(o *settings) { o.gitHost.store = st }
}

// WithGitHostAllowedRanges sets the address ranges that a connection to a Git
// host may reach in addition to public addresses, for example the range of a
// GitHub Enterprise Server on a private network.
func WithGitHostAllowedRanges(ranges ...netip.Prefix) Option {
	return func(o *settings) { o.gitHost.allow = append([]netip.Prefix(nil), ranges...) }
}

// WithGitHostRootCAs sets the root certificates that the connections to a Git
// host trust, for a GitHub Enterprise Server with a certificate of a private
// authority. Nil, the default, uses the roots of the system.
func WithGitHostRootCAs(pool *x509.CertPool) Option {
	return func(o *settings) { o.gitHost.cas = pool }
}

// newGitHostParts returns the Git host service and the middleware of the
// Idempotency-Key header that its routes use. Both are nil when no Git host
// store is set.
func newGitHostParts(cfg *settings, deps installationDeps) (*forge.Service, *idempotent.Middleware, error) {
	svc, err := newGitHostService(cfg, deps)
	if err != nil || svc == nil {
		return nil, nil, err
	}

	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return nil, nil, err
	}

	return svc, idem, nil
}

// newGitHostService returns the Git host service, or nil when no Git host store
// is set.
func newGitHostService(cfg *settings, deps installationDeps) (*forge.Service, error) {
	if cfg.gitHost.store == nil {
		return nil, nil //nolint:nilnil // No Git host routes: no store is set.
	}

	keys, ok := cfg.namespaceStore.(secret.InstallationKeyStore)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no installation keys: %w", ErrMissingOption)
	}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	secrets, err := secret.NewInstallationSecrets(secret.InstallationConfig{
		Keys: keys, Wrapper: cfg.secretWrapper, Now: cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("installation secrets: %w", err)
	}

	rules := egress.Rules{Allow: cfg.gitHost.allow}

	opener, err := github.NewOpener(github.Config{Rules: rules, RootCAs: cfg.gitHost.cas, Now: cfg.now})
	if err != nil {
		return nil, fmt.Errorf("make the GitHub adapter: %w", err)
	}

	svc, err := forge.NewService(forge.Config{
		Store: cfg.gitHost.store, Namespaces: cfg.namespaceStore, Secrets: secrets, Audit: entries,
		Openers: []forge.Opener{opener},
		CheckURL: func(raw string) error {
			return rules.CheckURL(raw, egress.HTTPSOnly)
		},
		OnAuditFailure: func() { deps.observed.IncAuditAppendFailure() },
		OnRefused:      func(reason string) { deps.observed.ObserveGitHostRefused(reason) },
		Now:            cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("make the Git host service: %w", err)
	}

	return svc, nil
}
