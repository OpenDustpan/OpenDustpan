package dustpan

import (
	"fmt"
	"net/http"

	profileapi "opendustpan.org/opendustpan/internal/api/profile"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/session"
)

// profileSettings holds the store of profiles.
type profileSettings struct {
	// store keeps the profiles, or is nil. Without it the server serves no
	// profile route.
	store profile.Store
}

// defaultProfileSettings returns the profile settings before any option
// runs: no store.
func defaultProfileSettings() profileSettings {
	return profileSettings{store: nil}
}

// WithProfileStore sets the store of profiles. It is optional: without it the
// server serves no profile route.
func WithProfileStore(st profile.Store) Option {
	return func(o *settings) { o.profiles.store = st }
}

// newProfileAPIs builds the profiles API, the issuer API and the sinks API.
func newProfileAPIs(
	svc services, cfg *settings, onFailure func(), policies *session.Policies,
) (profileAPIs, error) {
	profiles, err := newProfileHandler(svc, cfg, onFailure, policies)
	if err != nil {
		return profileAPIs{}, err
	}

	issuer, err := newIssuerHandler(svc, cfg, onFailure, policies)
	if err != nil {
		return profileAPIs{}, err
	}

	sinks, err := newSinksHandler(svc, cfg, onFailure)
	if err != nil {
		return profileAPIs{}, err
	}

	return profileAPIs{profiles: profiles, issuer: issuer, sinkAPI: sinks}, nil
}

// newProfileHandler builds the profile API, or returns nil when no profile
// store is set.
func newProfileHandler(
	svc services, cfg *settings, onFailure func(), policies *session.Policies,
) (http.Handler, error) {
	if cfg.profiles.store == nil {
		return nil, nil //nolint:nilnil // No profile API: no profile store is set.
	}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	profiles, err := profile.NewService(profile.Config{
		Store:      cfg.profiles.store,
		Namespaces: cfg.namespaceStore,
		Sealer:     svc.secrets,
		Audit:      entries,
		Authorizer: cfg.authorizer,
		Now:        cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("profile service: %w", err)
	}

	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return nil, err
	}

	// A nil *session.Policies must not become a non-nil interface.
	var sessionPolicy profileapi.SessionPolicy
	if policies != nil {
		sessionPolicy = policies
	}

	handler, err := profileapi.NewHandler(profileapi.Config{
		Idempotency:   idem,
		SessionPolicy: sessionPolicy,
		Profiles:      profiles,
		Authenticator: cfg.authenticator,
		Authorizer:    cfg.authorizer,
		Sessions:      cfg.sessionCookies,
		Logger:        cfg.logger,
		Now:           cfg.now,

		Namespaces:     svc.namespaces,
		Audit:          entries,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("profile api: %w", err)
	}

	return handler, nil
}

// profileAPIs holds the profiles API, the issuer API and the sinks API.
type profileAPIs struct {
	profiles http.Handler
	issuer   http.Handler
	sinkAPI  http.Handler
}
