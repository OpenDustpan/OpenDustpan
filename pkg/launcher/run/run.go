// Package run is the mode run of the launcher: it opens the job session of
// a claim, runs each step of the assignment as an argument list in its own
// process group, sends the masked log of each step, renews the claim, and
// finishes the phase. It returns the exit code of the launcher. It never
// imports a transport: the caller gives the session client.
package run

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Config is what Run needs.
type Config struct {
	// Client sends the calls of the session.
	Client *session.Client
	// Handoff is the handoff token that the handoff gave.
	Handoff string
	// WorkDir holds the checkout, the home and the temporary directory of
	// the steps.
	WorkDir string
	// Path is the PATH of the image. A step program with a bare name is
	// found in its absolute directories only.
	Path string
	// Logger gets the messages of the launcher itself. They never hold a
	// secret or the output of a step. Nil writes none.
	Logger *slog.Logger
	// Stop closes when the platform stops the launcher, for example with
	// SIGTERM. The launcher stops the step as on a cancel and finishes the
	// phase failed.
	Stop <-chan struct{}
	// Grace returns the grace period of a step after SIGINT. Nil takes
	// runner.GraceChanges and runner.GraceOther.
	Grace func(changesResources bool) time.Duration
	// RenewEvery is the time between two renewals. Zero takes a third of
	// the lease.
	RenewEvery time.Duration
	// ChunkSize is the largest log chunk. Zero takes DefaultChunkSize.
	ChunkSize int
	// FlushEvery is the longest time that the output of a step waits
	// before it is sent. Zero takes DefaultFlushEvery.
	FlushEvery time.Duration
	// Retry is the wait before the first retry of a call that got no
	// answer. Zero takes DefaultRetry.
	Retry time.Duration
	// Sleep waits before a retry of a call and reports false when the
	// wait ended before the duration. Nil waits on the clock.
	Sleep func(ctx context.Context, d time.Duration) bool
}

// Defaults of Config.
const (
	DefaultChunkSize  = 256 << 10
	DefaultFlushEvery = 2 * time.Second
	DefaultRetry      = time.Second
	// retries is the number of retries of a call that got no answer.
	retries = 4
	// minRenewEvery is the shortest time between two renewals.
	minRenewEvery = time.Second
	// renewParts divides the lease into the time between two renewals.
	renewParts = 3
	// versionAheadWait is the wait before a new exchange after
	// version-ahead.
	versionAheadWait = 10 * time.Second
)

// notePrefix starts each line that the launcher writes to the log of a
// step.
const notePrefix = "dustpan-launcher: "

// Run runs one phase and returns the exit code of the launcher.
func Run(ctx context.Context, cfg *Config) launcher.ExitCode {
	r := &phaseRun{
		cfg:      cfg,
		log:      logger(cfg),
		sess:     nil,
		profile:  "",
		mu:       sync.Mutex{},
		lost:     false,
		canceled: false,
		events:   make(chan struct{}, 1),
		checkout: "",
		home:     "",
		tmp:      "",
	}

	if err := r.prepareDirs(); err != nil {
		r.log.WarnContext(ctx, "prepare the work directory", slog.Any("error", err))

		return launcher.ExitSandbox
	}

	sess, code := r.open(ctx)
	if sess == nil {
		return code
	}

	r.sess = sess
	r.profile = sess.Assignment.Profile

	return r.runPhase(ctx)
}

// logger returns the logger of cfg, or a logger that writes nothing.
func logger(cfg *Config) *slog.Logger {
	if cfg.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}

	return cfg.Logger
}

// phaseRun is the state of one run.
type phaseRun struct {
	cfg *Config
	// log gets the messages of the launcher.
	log  *slog.Logger
	sess *session.Session
	// profile is the profile of the assignment: the credentials that the
	// tool steps need, or empty when they need none.
	profile string
	// mu guards lost and canceled, which the renewal sets.
	mu       sync.Mutex
	lost     bool
	canceled bool
	// events gets a value when lost or canceled changes.
	events   chan struct{}
	checkout string
	home     string
	tmp      string
}

// prepareDirs creates the checkout, the home and the temporary directory of
// the steps in the work directory, with mode launcher.ModeDir.
func (r *phaseRun) prepareDirs() error {
	if !filepath.IsAbs(r.cfg.WorkDir) {
		return fmt.Errorf("%w: the work directory is not an absolute path", launcher.ErrUsage)
	}

	r.checkout = filepath.Join(r.cfg.WorkDir, "checkout")
	r.home = filepath.Join(r.cfg.WorkDir, "home")
	r.tmp = filepath.Join(r.cfg.WorkDir, "tmp")

	for _, dir := range []string{r.checkout, r.home, r.tmp} {
		if err := os.MkdirAll(dir, launcher.ModeDir); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Base(dir), err)
		}

		if err := os.Chmod(dir, launcher.ModeDir); err != nil {
			return fmt.Errorf("set the mode of %s: %w", filepath.Base(dir), err)
		}
	}

	return nil
}

// open exchanges the handoff token for a job session. On version-ahead it
// tries again until runner.VersionAheadLimit. It returns a nil session and
// the exit code when it fails.
func (r *phaseRun) open(ctx context.Context) (*session.Session, launcher.ExitCode) {
	if !launcher.ValidHandoffToken(r.cfg.Handoff) {
		r.log.WarnContext(ctx, "the handoff token is not valid")

		return nil, launcher.ExitHandoff
	}

	var since time.Time

	for {
		sess, err := session.Open(ctx, r.cfg.Client, r.cfg.Handoff)

		switch {
		case err == nil:
			return sess, launcher.ExitOK
		case errors.Is(err, session.ErrVersionAhead):
			if since.IsZero() {
				since = time.Now()
			}

			if runner.CheckVersionAhead(since, time.Now()) != nil {
				r.log.WarnContext(ctx, "the server stayed older than this release", slog.Any("error", err))

				return nil, launcher.ExitVersion
			}

			if !sleep(ctx, r.cfg.Stop, versionAheadWait) {
				return nil, launcher.ExitHandoff
			}
		case errors.Is(err, session.ErrVersionUnsupported):
			r.log.WarnContext(ctx, "the server refused the release of the launcher", slog.Any("error", err))

			return nil, launcher.ExitVersion
		default:
			r.log.WarnContext(ctx, "open the job session", slog.Any("error", err))

			return nil, launcher.ExitHandoff
		}
	}
}

// sleep waits for d. It returns false when ctx ends or stop closes first.
func sleep(ctx context.Context, stop <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	case <-stop:
		return false
	}
}

// runPhase checks the assignment, runs the steps under the renewal and
// finishes the phase.
func (r *phaseRun) runPhase(ctx context.Context) launcher.ExitCode {
	a := &r.sess.Assignment

	if a.Sweep != "" {
		r.log.WarnContext(ctx, "this launcher release does not run a sweep")

		return launcher.ExitInternal
	}

	if reason, code := unsupported(r.sess.AssignmentVersion, a); reason != "" {
		r.log.WarnContext(ctx, "the launcher cannot run the assignment", slog.String("reason", reason))

		return r.finish(ctx, protocol.Finish{Succeeded: false, Message: reason}, code)
	}

	renewCtx, stopRenew := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		r.renewLoop(renewCtx)
	}()

	outcome := r.runSteps(ctx)

	// The renewal loop runs until the report ends, so that the retries of the
	// report, which can wait up to a lease each, keep the claim.
	code := launcher.ExitClaimLost
	if !r.isLost() {
		code = r.finish(ctx, outcome, launcher.ExitOK)
	}

	stopRenew()
	<-done

	if r.isLost() {
		r.log.WarnContext(ctx, "the claim is lost: the launcher reports nothing more")

		return launcher.ExitClaimLost
	}

	return code
}

// unsupported returns the reason why this release cannot run the
// assignment a of version v, and the exit code after the report, or "".
func unsupported(v int, a *protocol.Assignment) (string, launcher.ExitCode) {
	switch {
	case v > runner.AssignmentVersion:
		return fmt.Sprintf("the assignment has the version %d, and this launcher release reads up to %d",
			v, runner.AssignmentVersion), launcher.ExitOK
	case a.Source != nil:
		return "this launcher release cannot download a source snapshot", launcher.ExitSource
	case a.Plan != nil:
		return "this launcher release cannot read the plan of an apply phase", launcher.ExitOK
	case len(a.Steps) == 0:
		return "the assignment has no step", launcher.ExitOK
	default:
		return "", launcher.ExitOK
	}
}

// finish ends the phase with f. It returns code when the server has the
// outcome, launcher.ExitClaimLost when the claim is lost, and
// launcher.ExitUnreported when the report failed.
func (r *phaseRun) finish(ctx context.Context, f protocol.Finish, code launcher.ExitCode) launcher.ExitCode {
	err := r.retry(ctx, func() error { return r.sess.Finish(ctx, f) })

	switch {
	case err == nil:
		return code
	case errors.Is(err, session.ErrClaimLost):
		r.log.WarnContext(ctx, "finish the phase: the claim is lost")

		return launcher.ExitClaimLost
	default:
		r.log.WarnContext(ctx, "finish the phase", slog.Any("error", err))

		return launcher.ExitUnreported
	}
}

// retry calls call, and calls it again after a wait when it got no answer,
// up to retries times. A Retry-After of the answer that is longer than the
// wait raises the wait, up to runner.MinLease: the shortest claim lease, so
// one wait never outlasts a lease. The backoff of the next wait is unchanged.
func (r *phaseRun) retry(ctx context.Context, call func() error) error {
	wait := r.cfg.Retry
	if wait <= 0 {
		wait = DefaultRetry
	}

	err := call()

	for i := 0; i < retries && errors.Is(err, session.ErrUnavailable); i++ {
		d := wait

		var serr *session.Error
		// Defense in depth: the transport caps the header too; this cap
		// stays so that another transport cannot stall a lease.
		if errors.As(err, &serr) && serr.RetryAfter > d {
			d = min(serr.RetryAfter, runner.MinLease)
		}

		if !r.sleep(ctx, d) {
			return err
		}

		wait *= 2
		err = call()
	}

	return err
}

// sleep waits for d before a retry. The Sleep of the config replaces the
// wait of the clock.
func (r *phaseRun) sleep(ctx context.Context, d time.Duration) bool {
	if r.cfg.Sleep != nil {
		return r.cfg.Sleep(ctx, d)
	}

	return sleep(ctx, nil, d)
}

// renewLoop renews the claim until ctx ends. It records a cancel and a lost
// claim, and signals each change on events.
func (r *phaseRun) renewLoop(ctx context.Context) {
	every := r.cfg.RenewEvery
	if every <= 0 {
		every = max(r.sess.Lease/renewParts, minRenewEvery)
	}

	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}

		resp, err := r.sess.Renew(ctx)

		switch {
		case errors.Is(err, session.ErrClaimLost):
			r.set(func() { r.lost = true })

			return
		case err != nil:
			if ctx.Err() == nil {
				r.log.WarnContext(ctx, "renew the claim", slog.Any("error", err))
			}
		case resp.Canceled:
			r.set(func() { r.canceled = true })
		default:
			// The claim is live and no cancel stops the work.
		}
	}
}

// set changes the state under the lock and signals events.
func (r *phaseRun) set(change func()) {
	r.mu.Lock()
	change()
	r.mu.Unlock()

	select {
	case r.events <- struct{}{}:
	default:
	}
}

// markLost records that the claim is lost.
func (r *phaseRun) markLost() {
	r.set(func() { r.lost = true })
}

// isLost reports whether the claim is lost.
func (r *phaseRun) isLost() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.lost
}

// isCanceled reports whether a cancel stops the work.
func (r *phaseRun) isCanceled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.canceled
}

// runSteps runs each step in order and returns the outcome of the phase.
func (r *phaseRun) runSteps(ctx context.Context) protocol.Finish {
	for i := range r.sess.Assignment.Steps {
		step := &r.sess.Assignment.Steps[i]

		res := r.runStep(ctx, step)

		switch {
		case r.isLost():
			return protocol.Finish{Succeeded: false, Message: "the claim is lost"}
		case res.reason != "":
			return protocol.Finish{Succeeded: false, Message: fmt.Sprintf("step %s: %s", step.Name, res.reason)}
		case res.exitCode != 0 && !step.AllowFailure:
			return protocol.Finish{
				Succeeded: false, Message: fmt.Sprintf("step %s ended with the exit code %d", step.Name, res.exitCode),
			}
		default:
			// The step succeeded, or its failure is allowed: the next step runs.
		}
	}

	return protocol.Finish{Succeeded: true, Message: ""}
}
