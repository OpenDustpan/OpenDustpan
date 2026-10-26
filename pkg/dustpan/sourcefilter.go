package dustpan

import (
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/sourcefilter"
	"opendustpan.org/opendustpan/internal/source"
)

// WithSourceFilters sets the store of the source filter of each namespace. With
// it, the server serves the source filter under
// /api/dustpan/v1/namespaces/{namespace}/source-filter. Without it, the server
// serves no route of the source filter.
func WithSourceFilters(f source.Filters) Option {
	return func(o *settings) { o.sourceFilters = f }
}

// newSourceFilterHandler builds the source filter API. The caller builds it
// only when the server has a store of the source filter.
func newSourceFilterHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	handler, err := sourcefilter.NewHandler(sourcefilter.Config{
		Namespaces:     svc.namespaces,
		Filters:        cfg.sourceFilters,
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
		return nil, fmt.Errorf("source filter api: %w", err)
	}

	return handler, nil
}
