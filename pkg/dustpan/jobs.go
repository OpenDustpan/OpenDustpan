package dustpan

import (
	"fmt"
	"net/http"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// WithJobStore sets the store of jobs. With it, the server serves the jobs
// API under /api/dustpan/v1/namespaces/{namespace}/jobs. Without it, the
// server serves no job route. With the runner stores (WithRunners), the
// inventory reader and a store of gates, the server also starts plan jobs
// through POST on that path.
func WithJobStore(st job.Store) Option {
	return func(o *settings) { o.jobStore = st }
}

// newJobsHandler builds the jobs API, or returns nil when no job store is
// set. starter is the job service, or nil when the server lacks a port that it needs.
func newJobsHandler(svc services, cfg *settings, onFailure func(), starter job.Starter) (http.Handler, error) {
	if cfg.jobStore == nil {
		return nil, nil //nolint:nilnil // No jobs API: no job store is set.
	}

	// A namespace store that keeps no audit entries is an error: a refusal
	// always writes its entry.
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
	}

	// A nil runner store must stay a nil interface: a typed nil would pass the
	// nil check of the handler and panic at the first read.
	var pools runner.PoolReader
	if cfg.runners != nil {
		pools = cfg.runners
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     svc.namespaces,
		Jobs:           cfg.jobStore,
		Starter:        starter,
		Workers:        cfg.watch,
		Pools:          pools,
		Authenticator:  cfg.authenticator,
		Authorizer:     cfg.authorizer,
		Audit:          neutralAudit,
		OnAuditFailure: onFailure,
		DenialLimiter:  cfg.denialLimiter,
		Sessions:       cfg.sessionCookies,
		Logger:         cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("jobs api: %w", err)
	}

	return handler, nil
}
