package dustpan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/api/runnerpools"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/stack"
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
// /runner/v1 and the management API under /api/dustpan/v1/namespaces. Without them, the
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

// WithJobClaimerWrapForTests sets a function that wraps the claimer that the
// job claimer function built, so a test can keep or watch it. The wrap does not
// depend on the order of the options, so it works with a built-in store, whose
// options replace WithJobClaimer. It does nothing when the server builds no
// claimer.
func WithJobClaimerWrapForTests(wrap func(job.Claimer) job.Claimer) Option {
	return func(o *settings) { o.jobClaimerWrap = wrap }
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

// WithJobRecorder sets the recorder that the steps and the log chunks of a
// launcher call. It replaces the recorder that the server builds itself
// (see newDurableRecorder). The session exchange also needs WithJobStore.
func WithJobRecorder(r job.CheckedRecorder) Option {
	return func(o *settings) { o.jobRecorder = r }
}

// newRunnerHandler builds the runner protocol and the runner management API,
// or returns nil when the server has no runner stores or no public URL. The
// join challenge names the public URL without a trailing slash, so a runner
// cannot join without it.
func newRunnerHandler(
	cfg *settings, svc services, onFailure func(), proxies *proxysessionsvc.Service,
) (http.Handler, error) {
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
		if cfg.jobClaimerWrap != nil && claimer != nil {
			claimer = cfg.jobClaimerWrap(claimer)
		}
	}

	deps, err := sessionDepsOf(cfg, svc)
	if err != nil {
		return nil, err
	}

	handler, err := runnerapi.NewHandler(runnerapi.Config{
		Namespaces: svc.namespaces, Pools: cfg.runners, Members: cfg.runners, Handoffs: cfg.runners,
		Claimer: claimer, Sweeps: nil, Operations: cfg.durableStore,
		Recorder: deps.recorder, Jobs: cfg.jobStore, Authenticator: cfg.authenticator,
		Authorizer: cfg.authorizer, Sessions: cfg.sessionCookies, Logger: cfg.logger, Release: release,
		ServerURL: publicURL, Now: cfg.now, PollInterval: 0,
		Audit: neutralAudit, OnAuditFailure: onFailure, DenialLimiter: cfg.denialLimiter,
		Plans: deps.plans.plans, Artifacts: deps.plans.artifacts, OnHandoffRefusalSuppressed: nil,
		JobTokens: deps.issuer.tokens, Starters: deps.issuer.starters, Proxies: proxies,
	})
	if err != nil {
		return nil, fmt.Errorf("runner api: %w", err)
	}

	return handler, nil
}

// sessionDeps are what the paths of a job session need: the recorder of the
// steps and the logs, the plan parts, and the issuer of the job tokens. A part
// that the server lacks is nil.
type sessionDeps struct {
	recorder job.CheckedRecorder
	plans    planDeps
	issuer   jobTokenIssuer
}

// sessionDepsOf returns the parts of the job session paths. The recorder is
// the one of WithJobRecorder, else the one that the server builds itself.
func sessionDepsOf(cfg *settings, svc services) (sessionDeps, error) {
	var deps sessionDeps

	deps.recorder = cfg.jobRecorder

	if deps.recorder == nil {
		built, ok, err := newDurableRecorder(&jobRecorderInputs{
			jobs: cfg.jobStore, blobs: cfg.blobStore, updates: cfg.updateStore, namespaceStore: cfg.namespaceStore,
			wrapper: cfg.secretWrapper, now: cfg.now, namespaces: svc.namespaces, logger: cfg.logger,
			retention: cfg.jobRetention,
		})
		if err != nil {
			return deps, err
		}

		if ok {
			deps.recorder = built
		}
	}

	var err error

	if deps.plans, err = planDepsOf(deps.recorder, svc, cfg); err != nil {
		return deps, err
	}

	if deps.issuer, err = jobTokenIssuerOf(cfg); err != nil {
		return deps, err
	}

	return deps, nil
}

// jobTokenIssuer is what the credentials path of a job session needs: the
// service that issues job tokens and the records of the principals that
// start jobs. Both are nil when the server issues no job tokens.
type jobTokenIssuer struct {
	tokens   *jobtoken.Service
	starters runnerapi.StarterRecords
}

// errNoStarterRecords is the error of a server that issues job tokens and
// keeps no records of principals.
var errNoStarterRecords = errors.New("the namespace store keeps no principals, which the job token issuer needs")

// jobTokenIssuerOf returns the issuer of the credentials path. A server with
// no job token store issues no job tokens, and the path answers not-found. A
// server with a job token store and no records of principals does not start:
// the issuer must name the record of the starter of each job.
func jobTokenIssuerOf(cfg *settings) (jobTokenIssuer, error) {
	if cfg.jobTokenStore == nil {
		return jobTokenIssuer{tokens: nil, starters: nil}, nil
	}

	records, ok := cfg.namespaceStore.(principal.Store)
	if !ok {
		return jobTokenIssuer{tokens: nil, starters: nil}, fmt.Errorf("%w: %w", ErrMissingOption, errNoStarterRecords)
	}

	return jobTokenIssuer{tokens: jobtoken.NewService(cfg.jobTokenStore, cfg.now), starters: records}, nil
}

// newRunnerPoolsHandler builds the runner management API. It returns false
// when the server has no runner stores or no public URL, as the protocol does.
func newRunnerPoolsHandler(
	cfg *settings, svc services, onFailure func(), proxies *proxysessionsvc.Service,
) (http.Handler, bool, error) {
	neutralAudit, err := auditStoreOf(cfg)
	if err != nil {
		return nil, false, err
	}

	publicURL := strings.TrimRight(strings.TrimSpace(cfg.publicURL), "/")
	if cfg.runners == nil || publicURL == "" {
		return nil, false, nil
	}

	pools, err := runnerpools.NewHandler(runnerpools.Config{
		Namespaces: svc.namespaces, Pools: cfg.runners, Members: cfg.runners,
		Authenticator: cfg.authenticator, Authorizer: cfg.authorizer,
		Audit: neutralAudit, OnAuditFailure: onFailure, DenialLimiter: cfg.denialLimiter,
		Sessions: cfg.sessionCookies, Logger: cfg.logger, ServerURL: publicURL, Now: cfg.now, Proxies: proxies,
	})
	if err != nil {
		return nil, false, fmt.Errorf("runner pools api: %w", err)
	}

	return pools, true, nil
}

// planDeps are what the plan paths of a launcher need. A part that the server
// lacks is nil, and the plan paths then answer not-found.
type planDeps struct {
	plans     job.PlanRecorder
	artifacts job.ArtifactReader
}

// planDepsOf returns the recorder as a plan recorder, and the reader of stored
// plans. A recorder from WithJobRecorder may not store plans, and a server may
// lack the stores for the reader.
func planDepsOf(recorder job.CheckedRecorder, svc services, cfg *settings) (planDeps, error) {
	var deps planDeps

	if p, ok := recorder.(job.PlanRecorder); ok {
		deps.plans = p
	}

	reader, canRead, err := newJobArtifactReader(svc, cfg)
	if err != nil {
		return planDeps{plans: nil, artifacts: nil}, err
	}

	if canRead {
		deps.artifacts = reader
	}

	return deps, nil
}

// jobRecorderInputs are the values that newDurableRecorder reads.
type jobRecorderInputs struct {
	jobs           job.Store
	blobs          blob.Store
	updates        stack.UpdateStore
	namespaceStore namespace.Store
	wrapper        secret.Wrapper
	now            func() time.Time
	logger         *slog.Logger
	namespaces     *namespace.Service
	// retention is the period of each new plan and each new job log version.
	retention jobRetention
}

// newDurableRecorder returns the recorder that the steps and the log chunks of
// a launcher call, for a server with no recorder from WithJobRecorder. It
// builds the durable recorder over the record store of the job store, the
// blob store and the item key sealer. It returns false when the server lacks
// one of these, so the step and log paths of a job session answer not-found
// and no launcher writes a byte. A job store that keeps jobs but cannot record
// is not an error: the server then serves no steps and no logs.
func newDurableRecorder(in *jobRecorderInputs) (*job.DurableRecorder, bool, error) {
	records, ok := in.jobs.(job.RecordStore)
	if !ok || in.blobs == nil || in.namespaces == nil || in.updates == nil || in.namespaceStore == nil {
		return nil, false, nil
	}

	refusals, err := blobRefusalsOf(in.namespaceStore, job.ArtifactBlobFamily(), in.now)
	if err != nil {
		return nil, false, err
	}

	blobs, err := job.NewArtifactBlobs(in.blobs, refusals)
	if err != nil {
		return nil, false, fmt.Errorf("job recorder blobs: %w", err)
	}

	sealer, err := newItemSealerOf(&itemSealerInputs{
		updates: in.updates, blobs: in.blobs, namespaces: in.namespaceStore, wrapper: in.wrapper, now: in.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("job recorder: %w", err)
	}

	recorder, err := job.NewRecorder(job.RecorderConfig{
		Store: records, Jobs: in.jobs, Blobs: blobs, Sealer: sealer,
		Namespaces: jobNamespaces{namespaces: in.namespaces}, Retention: in.retention,
		Now: in.now, Log: in.logger,
	})
	if err != nil {
		return nil, false, fmt.Errorf("job recorder: %w", err)
	}

	return recorder, true, nil
}

// runnerHandlers holds the two runner handlers: the runner protocol and the
// management API of pools, join tokens and runners. Each is nil when the
// server has no runner stores or no public URL.
type runnerHandlers struct {
	protocol http.Handler
	pools    http.Handler
}

// newRunnerHandlers builds the runner protocol and the management API. Both
// share one proxy session service: the protocol serves the proxy session,
// and the management API serves the proxy settings of a pool.
func newRunnerHandlers(cfg *settings, svc services, onFailure func()) (runnerHandlers, error) {
	proxies, _, err := newProxySessionService(cfg, svc)
	if err != nil {
		return runnerHandlers{protocol: nil, pools: nil}, err
	}

	protocol, err := newRunnerHandler(cfg, svc, onFailure, proxies)
	if err != nil {
		return runnerHandlers{protocol: nil, pools: nil}, err
	}

	pools, _, err := newRunnerPoolsHandler(cfg, svc, onFailure, proxies)
	if err != nil {
		return runnerHandlers{protocol: nil, pools: nil}, err
	}

	return runnerHandlers{protocol: protocol, pools: pools}, nil
}

// newProxySessionService builds the service of the proxy session and of the
// proxy settings of a pool. The second result is false when the server lacks
// a store that the service reads: the runner stores, the proxy rule sets, the
// secret service, the durable operations, the jobs or the inventory. Without
// the service, the paths of the job proxy answer not-found and the proxy
// settings routes are not mounted. Both SQL adapters keep all of them.
func newProxySessionService(cfg *settings, svc services) (*proxysessionsvc.Service, bool, error) {
	settingsStore, ok := cfg.runners.(runner.PoolProxyStore)
	rules, hasRules := cfg.namespaceStore.(proxyRuleStore)

	if !ok || !hasRules || svc.secrets == nil || svc.namespaces == nil || cfg.durableStore == nil ||
		cfg.jobStore == nil || cfg.inventoryReader == nil {
		return nil, false, nil
	}

	entries, err := auditStoreOf(cfg)
	if err != nil {
		return nil, false, err
	}

	proxies, err := proxysessionsvc.New(proxysessionsvc.Config{
		Settings: settingsStore, Rules: rules.ProxyRules(), Secrets: svc.secrets, Namespaces: svc.namespaces,
		Operations: cfg.durableStore, Claims: cfg.runners, Jobs: cfg.jobStore, Units: cfg.inventoryReader,
		Audit: entries, Logger: cfg.logger, Now: cfg.now,
	})
	if err != nil {
		return nil, false, fmt.Errorf("proxy session service: %w", err)
	}

	return proxies, true, nil
}
