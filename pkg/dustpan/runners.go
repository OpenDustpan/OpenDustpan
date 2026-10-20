package dustpan

import (
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// RunnerStores are the ports of runner pools, runners, join tokens and job
// sessions. Both SQL adapters serve them from the same database connection
// as the other ports.
type RunnerStores interface {
	runner.PoolStore
	runner.MemberStore
	runner.SessionStore
}

// WithRunners sets the stores of the runner protocol. With them and a public
// URL (WithPublicURL), the server serves the runner protocol under
// /runner/v1 and the management API under /api/runners. Without them, the
// server serves neither.
func WithRunners(s RunnerStores) Option {
	return func(o *settings) { o.runners = s }
}

// WithJobClaimer sets the claimer that a poll of a runner of a pool for jobs
// calls. Without it, a poll of such a runner finds no work.
func WithJobClaimer(c job.Claimer) Option {
	return func(o *settings) { o.jobClaimer = c }
}

// newRunnerHandler builds the runner protocol and the runner management API,
// or returns nil when the server has no runner stores or no public URL. The
// join challenge names the public URL without a trailing slash, so a runner
// cannot join without it.
func newRunnerHandler(cfg *settings, svc services, onFailure func()) (http.Handler, error) {
	var neutralAudit audit.Store
	if st, ok := cfg.namespaceStore.(audit.Store); ok {
		neutralAudit = st
	}

	publicURL := strings.TrimRight(strings.TrimSpace(cfg.publicURL), "/")
	if cfg.runners == nil || publicURL == "" {
		return nil, nil //nolint:nilnil // No runner protocol: the server has no runner stores or no public URL.
	}

	release, err := runner.ParseRelease(cfg.version)
	if err != nil {
		// A development build has no release. It accepts only runners of the
		// release 0.0.x.
		release = runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}
	}

	handler, err := runnerapi.NewHandler(runnerapi.Config{
		Namespaces: svc.namespaces, Pools: cfg.runners, Members: cfg.runners, Handoffs: cfg.runners,
		Claimer: cfg.jobClaimer, Sweeps: nil, Operations: cfg.durableStore, Authenticator: cfg.authenticator,
		Authorizer: cfg.authorizer, Sessions: cfg.sessionCookies, Logger: cfg.logger, Release: release,
		ServerURL: publicURL, Now: cfg.now, PollInterval: 0,
		Audit: neutralAudit, OnAuditFailure: onFailure,
	})
	if err != nil {
		return nil, fmt.Errorf("runner api: %w", err)
	}

	return handler, nil
}
