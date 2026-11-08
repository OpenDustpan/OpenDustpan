package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"unicode"

	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
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
	// principals keeps the principals, or is nil without them. The start
	// reads it for the warning about the people of opendustpan dev.
	principals principal.Store
	kind       installation.Kind
}

// newKindState returns the installation kind of cfg and the stores that the
// start reads for it.
func newKindState(cfg *settings) kindState {
	return kindState{store: cfg.kindStore, principals: principalStoreOf(cfg), kind: cfg.installationKind}
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

// maxDevWarned is the count of dev people that the start-up warning names.
const maxDevWarned = 20

// principalStoreOf returns the principal store of cfg, or nil when the store
// keeps no principals.
//
//nolint:ireturn // The two SQL adapters share no concrete store type, so the caller keeps the port.
func principalStoreOf(cfg *settings) principal.Store {
	store, ok := cfg.namespaceStore.(principal.Store)
	if !ok {
		return nil
	}

	return store
}

// warnDevPrincipals logs one warning that names the dev people (principals
// with a subject that starts with principal.DevSubjectPrefix, in any case of
// its ASCII letters) of a server that is not of the kind dev. The
// authenticator refuses them. The warning does not stop the start, and a
// failed read only logs the failure.
func (s *Server) warnDevPrincipals(ctx context.Context) {
	if s.kind.kind == installation.KindDev || s.kind.principals == nil {
		return
	}

	svc, err := principal.NewService(principal.Config{
		TempAdmins: nil,
		Store:      s.kind.principals,
		Guard:      principal.AdminOnlyGuard{},
		Namespaces: nil,
		Tokens:     nil,
		GroupReach: nil,
		Now:        nil,
		NewID:      nil,
	})
	if err != nil {
		s.logger.WarnContext(ctx, "dev people check skipped", slog.String("reason", err.Error()))

		return
	}

	var (
		total    int64
		subjects []string
	)

	for _, prefix := range devPrefixCases() {
		req := principal.PageRequest{Prefix: prefix, Cursor: "", Limit: maxDevWarned}

		page, listErr := svc.ListPrincipals(ctx, req, "")
		if listErr != nil {
			s.logger.WarnContext(ctx, "dev people check failed", slog.String("reason", listErr.Error()))

			return
		}

		total += page.Total

		for i := range page.Items {
			// A subject comes from the database, which a restore may fill from a
			// backup. Clean it before it reaches the log.
			clean, _ := backup.CleanSubject(page.Items[i].Subject)
			subjects = append(subjects, clean)
		}
	}

	if len(subjects) == 0 {
		return
	}

	slices.Sort(subjects)
	subjects = subjects[:min(len(subjects), maxDevWarned)]

	s.logger.WarnContext(ctx,
		"this installation holds people that opendustpan dev made; the server refuses their sessions and tokens",
		slog.Int64("count", total), slog.Any("subjects", subjects))
}

// devPrefixCases returns each ASCII case form of principal.DevSubjectPrefix,
// in byte order: "DEV:" first and "dev:" last. The authenticator refuses a
// subject with the prefix in any case (see principal.IsDevSubject), but the
// prefix of ListPrincipals compares bytes exactly, so the warning reads each
// form.
func devPrefixCases() []string {
	forms := []string{""}

	for _, c := range principal.DevSubjectPrefix {
		lower, upper := string(unicode.ToLower(c)), string(unicode.ToUpper(c))
		next := make([]string, 0, len(forms)+len(forms))

		for _, form := range forms {
			next = append(next, form+upper)

			if lower != upper {
				next = append(next, form+lower)
			}
		}

		forms = next
	}

	slices.Sort(forms)

	return forms
}
