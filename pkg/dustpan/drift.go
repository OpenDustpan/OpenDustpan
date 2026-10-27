package dustpan

import (
	"fmt"
	"net/http"

	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/driftsvc"
	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/job"
)

// WithDriftStore sets the store of the latest drift result of each unit. With
// it, and with the inventory reader, the server serves the latest drift result
// of a unit under /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift.
// With the job service too (see WithJobStore), the server also starts a drift
// check through POST on /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/drift-checks.
// The context API reads the same store for its drift section. Without it, the
// server serves no drift route, and the drift section of the context API
// answers unavailable.
func WithDriftStore(st drift.Store) Option {
	return func(o *settings) { o.driftStore = st }
}

// newDriftHandler builds the drift API, or returns nil when no drift store or no
// inventory reader is set. starter is the job service, or nil when the server
// lacks a port that it needs: the server then serves the read route only.
func newDriftHandler(svc services, cfg *settings, onFailure func(), starter job.Starter) (http.Handler, error) {
	if cfg.driftStore == nil || cfg.inventoryReader == nil {
		return nil, nil //nolint:nilnil // No drift API: no drift store or no inventory is set.
	}

	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	var checker drift.Checker

	if starter != nil {
		service, serviceErr := driftsvc.NewService(starter, cfg.now)
		if serviceErr != nil {
			return nil, fmt.Errorf("drift service: %w", serviceErr)
		}

		checker = service
	}

	handler, err := driftapi.NewHandler(driftapi.Config{
		Namespaces:     svc.namespaces,
		Units:          cfg.inventoryReader,
		Results:        cfg.driftStore,
		Checker:        checker,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("drift api: %w", err)
	}

	return handler, nil
}
