package dustpan

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/pkg/installation"
)

// WithInstallationKind sets the installation kind of the server: dev or
// production. Without it, the server runs as production, so an SDK server
// gets the rules of a production installation. Run claims the kind in the
// store of WithInstallationKindStore. A server of the kind dev serves
// the sign-in route of the one-time code only when the wiring of the binary
// passes one.
func WithInstallationKind(kind installation.Kind) Option {
	return func(o *settings) { o.installationKind = kind }
}

// WithInstallationKindStore sets the store that keeps the installation kind.
// With it, Run claims the kind as the first start-up step, before any other
// step that writes, and stops with an error that wraps
// installation.ErrKindMismatch when another kind claimed the database.
// Without it, Run claims nothing and NewServer reads no database.
func WithInstallationKindStore(store installation.KindStore) Option {
	return func(o *settings) { o.kindStore = store }
}

// This variable registers the maker of the dev sign-in option with the
// internal seam when the package initializes. pkg/dustpan has no exported
// option for the sign-in: only code of this repository can import
// internal/devsignin, so an SDK server never serves it.
//
//nolint:unparam // The initializer needs a value; the function registers and answers nothing else.
var _ = func() struct{} {
	devsignin.Register(func(c devsignin.Config) any {
		return Option(func(o *settings) { o.devSignIn = &c })
	})

	return struct{}{}
}()

// kindState is the installation kind of a server and the store that keeps its
// claim. The store is nil for a server that claims nothing.
type kindState struct {
	store installation.KindStore
	kind  installation.Kind
}

// checkKindSettings refuses an installation kind that is not known, and a
// dev sign-in on a server of another kind.
func checkKindSettings(cfg *settings) error {
	if cfg.installationKind == "" {
		cfg.installationKind = installation.KindProduction
	}

	if !cfg.installationKind.Valid() {
		return fmt.Errorf("%w: WithInstallationKind: %w", ErrInvalidOption, installation.ErrInvalidKind)
	}

	if cfg.devSignIn != nil && cfg.installationKind != installation.KindDev {
		return fmt.Errorf("%w: the dev sign-in needs the installation kind dev", ErrInvalidOption)
	}

	return nil
}

// claimInstallationKind is the first start-up step after the migrations. It
// claims the kind of the server in the store, and stops the start when the
// stored kind differs: a database belongs to the kind that claimed it first.
func (s *Server) claimInstallationKind(ctx context.Context) error {
	if s.kind.store == nil {
		return nil
	}

	stored, err := s.kind.store.ClaimKind(ctx, s.kind.kind)
	if err != nil {
		return fmt.Errorf("claim installation kind: %w", err)
	}

	if stored != s.kind.kind {
		return &installation.MismatchError{Stored: stored, Want: s.kind.kind}
	}

	return nil
}
