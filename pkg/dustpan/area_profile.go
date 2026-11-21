package dustpan

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	profileapi "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/session"
)

// profileSettings holds the stores of profiles.
type profileSettings struct {
	// store keeps the profiles, or is nil. Without it the server serves no
	// profile route.
	store profile.Store
	// sessions keeps the open sessions. It is set with the store: without
	// it the server serves no profile route.
	sessions profile.Sessions
}

// defaultProfileSettings returns the profile settings before any option
// runs: no store.
func defaultProfileSettings() profileSettings {
	return profileSettings{store: nil, sessions: nil}
}

// WithProfileStore sets the store of profiles. It is optional: without it the
// server serves no profile route.
func WithProfileStore(st profile.Store) Option {
	return func(o *settings) { o.profiles.store = st }
}

// WithProfileSessions sets the store of open sessions. It is optional with
// the store: without it the server serves no profile route.
func WithProfileSessions(st profile.Sessions) Option {
	return func(o *settings) { o.profiles.sessions = st }
}

// profileInputs holds the values that newProfileHandler reads. The
// assembly fills each field; the profile area reads no other value.
type profileInputs struct {
	// store keeps the profiles, or is nil: then the server serves no
	// profile route.
	store profile.Store
	// openSessions keeps the open sessions, or is nil: then the server
	// serves no profile route.
	openSessions profile.Sessions
	// entries is the namespace store. It must keep audit entries too.
	entries    namespace.Store
	namespaces *namespace.Service
	sealer     profile.Sealer

	authenticator identity.Authenticator
	authorizer    identity.Authorizer
	// sessions checks the console session cookie, or is nil with no
	// console sign-in. policies is the session policy service, or nil.
	sessions    *sessionauth.Checker
	policies    *session.Policies
	idempotency *idempotent.Middleware

	denialLimiter *denylimit.Limiter
	onFailure     func()
	logger        *slog.Logger
	now           func() time.Time
}

// newProfileHandler builds the profile API, or returns nil when no profile
// store is set.
func newProfileHandler(in profileInputs) (http.Handler, error) {
	if in.store == nil {
		return nil, nil //nolint:nilnil // No profile API: no profile store is set.
	}

	if in.openSessions == nil {
		return nil, fmt.Errorf("%w: profile sessions", ErrMissingOption)
	}

	entries, ok := in.entries.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	// The Expr engine takes its run slots from one limiter, which the
	// server makes for the profile API: it computes the expressions of
	// each open with it.
	evaluator, err := expr.New(expr.NewLimiter())
	if err != nil {
		return nil, fmt.Errorf("profile expression engine: %w", err)
	}

	profiles, err := profile.NewService(profile.Config{
		Store:      in.store,
		Namespaces: in.entries,
		Sealer:     in.sealer,
		Audit:      entries,
		Authorizer: in.authorizer,
		Sessions:   in.openSessions,
		Evaluator:  evaluator,
		Now:        in.now,
	})
	if err != nil {
		return nil, fmt.Errorf("profile service: %w", err)
	}

	// A nil *session.Policies must not become a non-nil interface.
	var sessionPolicy profileapi.SessionPolicy
	if in.policies != nil {
		sessionPolicy = in.policies
	}

	handler, err := profileapi.NewHandler(profileapi.Config{
		Idempotency:   in.idempotency,
		SessionPolicy: sessionPolicy,
		Profiles:      profiles,
		Authenticator: in.authenticator,
		Authorizer:    in.authorizer,
		Sessions:      in.sessions,
		Logger:        in.logger,
		Now:           in.now,

		Namespaces:     in.namespaces,
		Audit:          entries,
		OnAuditFailure: in.onFailure,
		DenialLimiter:  in.denialLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("profile api: %w", err)
	}

	return handler, nil
}
