package dustpan

import (
	"context"
	"fmt"
	"net/http"
	"time"

	issuerapi "opendustpan.org/opendustpan/internal/api/issuer"
	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/issuerfetch"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/session"
)

// The name, the kind and the interval of the schedule of the issuer. The name
// and the kind match, as for the other schedules of the server.
const (
	issuerRotateName    = "issuer.rotate"
	issuerRotateKind    = durable.Kind(issuerRotateName)
	issuerRotateVersion = 1
	issuerRotateEvery   = 24 * time.Hour
)

// WithIssuerStore sets the store of the OIDC issuer: the signing keys and the
// settings of each namespace. It is optional: without it the server serves no
// issuer route and signs no token.
func WithIssuerStore(st profile.IssuerStore) Option {
	return func(o *settings) { o.issuerStore = st }
}

// WithIssuerBaseURL sets the host of the issuer URL of each namespace, for
// example "https://issuer.example.com". When it is empty, the issuer uses the
// public URL. A change of this host, or of the public URL when this is empty,
// breaks the trust that clouds hold for each namespace with no issuerURL.
func WithIssuerBaseURL(raw string) Option {
	return func(o *settings) { o.issuerBaseURL = raw }
}

// WithIssuerOutbox sets the outbox that receives the notices issuer.key-next,
// issuer.key-revoked and issuer.key-copy-missing. It is optional: without it
// the issuer sends no notice.
func WithIssuerOutbox(ob notify.Outbox) Option {
	return func(o *settings) { o.issuerOutbox = ob }
}

// newIssuerService builds the issuer, or returns nil when no issuer store is
// set.
func newIssuerService(svc services, cfg *settings) (*issuersvc.Service, error) {
	if cfg.issuerStore == nil {
		return nil, nil //nolint:nilnil // No issuer: no issuer store is set.
	}

	var notifier issuersvc.Notifier
	if cfg.issuerOutbox != nil {
		notifier = cfg.issuerOutbox
	}

	issuer, err := issuersvc.NewService(issuersvc.Config{
		Store:         cfg.issuerStore,
		Namespaces:    cfg.namespaceStore,
		Sealer:        svc.secrets,
		Authorizer:    cfg.authorizer,
		Notifier:      notifier,
		Fetcher:       issuerfetch.New(egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly)),
		IssuerBaseURL: cfg.issuerBaseURL,
		PublicURL:     cfg.publicURL,
		Now:           cfg.now,
		Log:           cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("issuer service: %w", err)
	}

	return issuer, nil
}

// newIssuerHandler builds the issuer routes, or returns nil when no issuer
// store is set.
func newIssuerHandler(
	svc services, cfg *settings, onFailure func(), policies *session.Policies,
) (http.Handler, error) {
	if svc.issuer == nil {
		return nil, nil //nolint:nilnil // No issuer routes: no issuer store is set.
	}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	// A nil *session.Policies must not become a non-nil interface.
	var sessionPolicy issuerapi.SessionPolicy
	if policies != nil {
		sessionPolicy = policies
	}

	handler, err := issuerapi.NewHandler(issuerapi.Config{
		Issuer:         svc.issuer,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          entries,
		Namespaces:     svc.namespaces,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		SessionPolicy:  sessionPolicy,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("issuer api: %w", err)
	}

	return handler, nil
}

// issuerRotateSchedule returns the schedule of the daily rotation, or none
// when the server has no issuer.
func (s *Server) issuerRotateSchedule() []durable.ScheduleSpec {
	if s.issuer == nil {
		return nil
	}

	return []durable.ScheduleSpec{{
		Name: issuerRotateName, Kind: issuerRotateKind, Version: issuerRotateVersion,
		Interval: issuerRotateEvery, Input: nil,
	}}
}

// issuerRotateKindSpec returns the runner kind of the daily rotation. Its step
// returns the error of a run, so the runner counts a failed run as a failed
// attempt and tries again.
func (s *Server) issuerRotateKindSpec() []durable.KindSpec {
	if s.issuer == nil {
		return nil
	}

	return []durable.KindSpec{
		{
			Kind:       issuerRotateKind,
			Version:    issuerRotateVersion,
			MinVersion: issuerRotateVersion,
			Steps: []durable.Step{
				{Name: jobStepName, Run: func(ctx context.Context, _ durable.StepContext) ([]byte, error) {
					if err := s.issuer.RotateAll(ctx); err != nil {
						return nil, fmt.Errorf("issuer rotation: %w", err)
					}

					return nil, nil
				}},
			},
		},
	}
}
