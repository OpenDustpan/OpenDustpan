package dustpan

import (
	"fmt"
	"net/http"
	"time"

	selfserviceapi "opendustpan.org/opendustpan/internal/api/selfservice"
	"opendustpan.org/opendustpan/internal/expr"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/internal/selfservicesvc"
)

// selfServiceSettings holds the store of the blueprints of the self-service API.
type selfServiceSettings struct {
	// store keeps the blueprints, or is nil. Without it the server serves no
	// blueprint route.
	store selfservice.BlueprintStore
}

// WithSelfServiceBlueprints sets the store of the blueprints of the
// self-service API. With it, the server serves the blueprint routes under
// /api/dustpan/v1/self-service/blueprints. Without it, the server serves none
// of them. Grants apply at namespace level today: a blueprint action is
// checked on the namespace that holds the blueprint.
func WithSelfServiceBlueprints(st selfservice.BlueprintStore) Option {
	return func(o *settings) { o.selfService.store = st }
}

// funcClock reads the time of the server from its clock function.
type funcClock struct {
	now func() time.Time
}

// Now implements selfservicesvc.Clock.
func (c funcClock) Now() time.Time {
	return c.now()
}

// newSelfServiceHandler builds the blueprint API over the store of cfg. The
// Expr engine takes its run slots from one limiter, which this server makes
// for itself: it compiles each expression of a publish with it.
func newSelfServiceHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	evaluator, err := expr.New(expr.NewLimiter())
	if err != nil {
		return nil, fmt.Errorf("self-service expression engine: %w", err)
	}

	blueprints, err := selfservicesvc.New(selfservicesvc.Config{
		Store:      cfg.selfService.store,
		Authorizer: selfserviceapi.NewNamespaceAuthorizer(cfg.authorizer),
		Evaluator:  evaluator,
		Clock:      funcClock{now: cfg.now},
		Audit:      neutralAudit,
		Fences:     namespaceFences{namespaces: svc.namespaces},
	})
	if err != nil {
		return nil, fmt.Errorf("self-service blueprint service: %w", err)
	}

	handler, err := selfserviceapi.NewHandler(selfserviceapi.Config{
		Blueprints:     blueprints,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		Namespaces:     svc.namespaces,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
		Now:            cfg.now,
	})
	if err != nil {
		return nil, fmt.Errorf("self-service api: %w", err)
	}

	return handler, nil
}
