package dustpan

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"net/netip"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forge/github"
	"opendustpan.org/opendustpan/pkg/secret"
)

// commitProofScope is the purpose label of the proof that a write-back commit
// carries. It is the id of an installation scope, so the proofs have a data
// key of their own, and no other value of the installation opens with it.
const commitProofScope = "forge-commit-proof"

// commitProofText is the value that a proof seals. Its content does not
// matter: the proof is valid when the AEAD opens it for the binding.
const commitProofText = "dustpan commit proof"

// commitProver is the github.Prover of the server. It seals the proof with the
// data key of an installation scope, bound to the repository, the trailer and
// the base commit, so that no key leaves pkg/secret.
type commitProver struct {
	secrets *secret.InstallationSecrets
	// keyed is whether the server has a key-encryption key. The secrets cannot
	// seal or open without it.
	keyed bool
}

// Ready reports whether the server has a key-encryption key.
func (p commitProver) Ready() bool { return p.keyed }

// Seal returns the proof for binding. It fails with secret.ErrKeyNotConfigured
// when the server has no key-encryption key.
func (p commitProver) Seal(ctx context.Context, binding []byte) ([]byte, error) {
	if !p.keyed {
		return nil, fmt.Errorf("seal the proof of a commit: %w", secret.ErrKeyNotConfigured)
	}

	proof, err := p.secrets.SealBound(ctx, commitProofScope, binding, []byte(commitProofText))
	if err != nil {
		return nil, fmt.Errorf("seal the proof of a commit: %w", err)
	}

	return proof, nil
}

// Open reports whether proof is a proof that Seal made for binding. A proof
// that does not open is false and a nil error. The compare of the sealed text
// is in constant time, and the AEAD check of the binding is the proof.
//
// With no key-encryption key it returns secret.ErrKeyNotConfigured and not
// false: the secret package reports that case as a value that does not open,
// and a commit of the connection would then look like a forged commit.
func (p commitProver) Open(ctx context.Context, binding, proof []byte) (bool, error) {
	if !p.keyed {
		return false, fmt.Errorf("open the proof of a commit: %w", secret.ErrKeyNotConfigured)
	}

	text, err := p.secrets.OpenBound(ctx, commitProofScope, binding, proof)

	switch {
	case errors.Is(err, secret.ErrDecrypt):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("open the proof of a commit: %w", err)
	}

	return subtle.ConstantTimeCompare(text, []byte(commitProofText)) == 1, nil
}

// gitHostSettings holds the settings of the Git host connections: the store,
// the address ranges that a connection may reach in addition to public
// addresses, and the root certificates that it trusts.
type gitHostSettings struct {
	store forge.Store
	allow []netip.Prefix
	cas   *x509.CertPool
	// loopback lets a connection reach the loopback interface, for the tests
	// of the server with a fake Git host on the local host.
	loopback bool
	// checkLimits bounds the check calls. Nil means the default limits.
	checkLimits *forge.CheckLimits
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
// GitHub Enterprise Server on a private network. A range opens private-network
// addresses only (RFC 1918 and unique local). The server never opens loopback,
// link-local, cloud metadata, unspecified or multicast addresses, also inside
// a range, and NewServer refuses a range that is too wide (see
// egress.ParseAllowedRanges).
func WithGitHostAllowedRanges(ranges ...netip.Prefix) Option {
	return func(o *settings) { o.gitHost.allow = append([]netip.Prefix(nil), ranges...) }
}

// WithGitHostLoopbackForTests lets the connections to a Git host reach the
// loopback interface, so that a test can run a fake Git host on the local host.
// A server in production does not set it: no flag or environment variable
// reaches it.
func WithGitHostLoopbackForTests() Option {
	return func(o *settings) { o.gitHost.loopback = true }
}

// WithGitHostCheckLimits sets the limits of the check of a Git host connection:
// the shortest time between two checks of one connection, the number of checks
// of one installation in a window, and the budget of the app, the number of
// signed calls of the app in the window (PerApp). The default is one check in
// 10 seconds for each connection, 30 checks in a minute for each installation
// and 80 signed calls of the app in a minute. The server has no flag for it.
func WithGitHostCheckLimits(limits forge.CheckLimits) Option {
	return func(o *settings) { o.gitHost.checkLimits = &limits }
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

	// Defense in depth: the flag parser refuses a range that is too wide, and
	// the rules never open loopback or metadata addresses. This check stays so
	// that an embedder cannot pass a wide range.
	for _, p := range cfg.gitHost.allow {
		if rangeErr := egress.CheckAllowedRange(p); rangeErr != nil {
			return nil, fmt.Errorf("git host allowed ranges: %w", rangeErr)
		}
	}

	rules := egress.Rules{Allow: cfg.gitHost.allow, LoopbackForTests: cfg.gitHost.loopback}

	limits := forge.DefaultCheckLimits()
	if cfg.gitHost.checkLimits != nil {
		limits = *cfg.gitHost.checkLimits
	}

	// The budget of the signed calls of the app is the one of the limits,
	// for every connection of the app.
	budget, err := forge.NewAppBudget(limits.PerApp, limits.Window)
	if err != nil {
		return nil, fmt.Errorf("git host limits: %w", err)
	}

	opener, err := github.NewOpener(github.Config{
		Rules:     rules,
		RootCAs:   cfg.gitHost.cas,
		Now:       cfg.now,
		Prover:    commitProver{secrets: secrets, keyed: cfg.secretWrapper != nil},
		AppBudget: budget,
	})
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
		CheckLimits:    cfg.gitHost.checkLimits,
		Logger:         cfg.logger,
		Now:            cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("make the Git host service: %w", err)
	}

	return svc, nil
}
