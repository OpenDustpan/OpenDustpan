package dustpan

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
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
// the claimer asks at each claim (see job.NewClaimer). When the store keeps
// no gates, the state.write gates are off, and the guard finds no gate on any
// unit, as the tool adapters check no write. The guard is never nil.
type JobClaimerFunc func(gates job.GateGuard) job.Claimer

// WithJobClaimer sets the function that builds the claimer that a poll of a
// runner of a pool for jobs calls. Without it, a poll of such a runner finds
// no work.
func WithJobClaimer(build JobClaimerFunc) Option {
	return func(o *settings) { o.jobClaimer = build }
}

// claimGuard is the guard of the gates that the claimer of the runner
// protocol asks at each claim. It follows the rule of the state.write gates
// of the tool adapters: with the gates on, it is the guard that the start of
// a job asks, so the claim and the start meet the same gates. With the gates
// off, offReason names why, and no gate covers a unit.
type claimGuard struct {
	gates     jobGates
	offReason string
}

// Enforced implements job.GateGuard.
func (g claimGuard) Enforced(ctx context.Context, unit identity.Object) ([]job.EnforcedGate, error) {
	if g.offReason != "" {
		return nil, nil
	}

	return g.gates.Enforced(ctx, unit)
}

// claimGates returns the guard of the claimer under the rule of gates. A
// store that keeps gates with a part missing is an error, so the server does
// not start, as for the tool adapters.
func claimGates(cfg *settings, gates stateWriteGates) (claimGuard, error) {
	none := jobGates{snapshots: nil, members: inventoryFacts{reader: nil, ids: nil, deleted: nil}}

	if eff := gates.effective(); eff.Mode == stategate.ModeOff {
		return claimGuard{gates: none, offReason: eff.Reason}, nil
	}

	store, ok := cfg.namespaceStore.(gate.Store)

	// Defense in depth: newStateWriteGates refuses a store that keeps gates
	// with no inventory reader. This check stays so that the claimer never
	// runs with gates on and no store of gates. It does not rely on that
	// check.
	if !ok || cfg.inventoryReader == nil || gates.svc == nil {
		return claimGuard{gates: none, offReason: ""}, fmt.Errorf("%w: %w", ErrMissingOption, errGateParts)
	}

	return claimGuard{gates: jobGates{snapshots: store, members: newInventoryFacts(cfg)}, offReason: ""}, nil
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

	// The claimer gets the guard of the rule of the state.write gates, the
	// rule of the tool adapters.
	var claimer job.Claimer

	if cfg.jobClaimer != nil {
		guard, claimErr := claimGates(cfg, svc.stateWrite)
		if claimErr != nil {
			return nil, claimErr
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
