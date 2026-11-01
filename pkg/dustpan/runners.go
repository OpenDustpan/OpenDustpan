package dustpan

import (
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/gate"
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

// JobClaimerFunc builds the claimer that a poll of a runner of a pool for jobs
// calls. The server gives it the guard of the gates of its gate store, which
// the claimer asks at each claim (see job.NewClaimer). The guard is nil when
// the server keeps no gates or no inventory: the claimer of job.NewClaimer
// then refuses every claim.
type JobClaimerFunc func(gates job.GateGuard) job.Claimer

// WithJobClaimer sets the function that builds the claimer that a poll of a
// runner of a pool for jobs calls. Without it, a poll of such a runner finds
// no work.
func WithJobClaimer(build JobClaimerFunc) Option {
	return func(o *settings) { o.jobClaimer = build }
}

// claimGates returns the guard of the gates that the claimer of the runner
// protocol asks at each claim, and false when the server keeps no gates or no
// inventory. It is the guard that the start of a job asks, so the claim and
// the start meet the same gates.
func claimGates(cfg *settings) (jobGates, bool) {
	gates, ok := cfg.namespaceStore.(gate.Store)
	if !ok || cfg.inventoryReader == nil {
		return jobGates{snapshots: nil, members: inventoryFacts{reader: nil, ids: nil, deleted: nil}}, false
	}

	return jobGates{snapshots: gates, members: newInventoryFacts(cfg)}, true
}

// newRunnerHandler builds the runner protocol and the runner management API,
// or returns nil when the server has no runner stores or no public URL. The
// join challenge names the public URL without a trailing slash, so a runner
// cannot join without it.
func newRunnerHandler(cfg *settings, svc services, onFailure func()) (http.Handler, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, err
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

	// A server with no gates gives the claimer no guard, and the claimer then
	// refuses every claim.
	var claimer job.Claimer

	if cfg.jobClaimer != nil {
		var guard job.GateGuard
		if g, ok := claimGates(cfg); ok {
			guard = g
		}

		claimer = cfg.jobClaimer(guard)
	}

	handler, err := runnerapi.NewHandler(runnerapi.Config{
		Namespaces: svc.namespaces, Pools: cfg.runners, Members: cfg.runners, Handoffs: cfg.runners,
		Claimer: claimer, Sweeps: nil, Operations: cfg.durableStore, Authenticator: cfg.authenticator,
		Authorizer: cfg.authorizer, Sessions: cfg.sessionCookies, Logger: cfg.logger, Release: release,
		ServerURL: publicURL, Now: cfg.now, PollInterval: 0,
		Audit: neutralAudit, OnAuditFailure: onFailure, DenialLimiter: cfg.denialLimiter,
	})
	if err != nil {
		return nil, fmt.Errorf("runner api: %w", err)
	}

	return handler, nil
}
