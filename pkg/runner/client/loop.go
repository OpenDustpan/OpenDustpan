// Package client is the runner side of the runner protocol: the loop that
// polls for work, renews the claim after a third of the lease of the pool,
// sends a heartbeat every runner.HeartbeatEvery and stops the work when the
// server says that the claim is lost. It signs each call with the key of the
// runner.
//
// The loop holds no expiry that the server did not send, and no lease that
// the pool did not set.
package client

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Errors of the loop. An answer of the server wraps the sentinel of its
// code, and an *Error.
var (
	// ErrClaimLost reports the code claim-lost, or a claim that the server
	// does not know any more: the work stops.
	ErrClaimLost = errors.New("the claim of the runner is lost")
	// ErrRetired reports the code retired: the server retired the runner.
	ErrRetired = errors.New("the runner is retired")
	// ErrBadProof reports the code bad-proof: the server does not accept the
	// key of the runner.
	ErrBadProof = errors.New("the server does not accept the proof of the runner")
	// ErrNotFound reports the code not-found.
	ErrNotFound = errors.New("the server does not know the object")
	// ErrRateLimited reports the code rate-limited. The loop waits for the
	// Retry-After of the answer.
	ErrRateLimited = errors.New("the server limits the rate of the runner")
	// ErrUnavailable reports a call with no answer, or the code unavailable.
	ErrUnavailable = errors.New("the server is not available")
	// ErrRefused reports an answer of 400 or above that no other sentinel
	// names.
	ErrRefused = errors.New("the server refused the call")
	// ErrCanceled reports a cancel of the work, from a renewal or a
	// heartbeat.
	ErrCanceled = errors.New("the work is canceled")
	// ErrInvalid reports a configuration or an answer that the client refuses.
	ErrInvalid = errors.New("the runner client input is not valid")
)

// Error is the error body of an answer, with its status.
type Error struct {
	Status     int
	Code       string
	Message    string
	RetryAfter time.Duration
}

// Error returns the status, the code and the message of the answer.
func (e *Error) Error() string {
	return fmt.Sprintf("the server answered %d %s: %s", e.Status, e.Code, e.Message)
}

// Is matches the sentinel of the code of e.
func (e *Error) Is(target error) bool {
	switch e.Code {
	case protocol.ErrorClaimLost:
		return target == ErrClaimLost
	case protocol.ErrorRetired:
		return target == ErrRetired
	case protocol.ErrorBadProof:
		return target == ErrBadProof
	case protocol.ErrorNotFound:
		return target == ErrNotFound
	case protocol.ErrorRateLimited:
		return target == ErrRateLimited || target == ErrUnavailable
	case protocol.ErrorUnavailable:
		return target == ErrUnavailable
	default:
		return target == ErrRefused
	}
}

// Limits of the loop.
const (
	// maxWait caps each wait of the loop that the server asks for, the
	// shortest claim lease.
	maxWait = runner.MinLease
	// retryWait is the wait after a call that got no answer.
	retryWait = 5 * time.Second
	// minRenewEvery is the shortest time between two renewals.
	minRenewEvery = time.Second
	// maxOperation is the longest operation ID that the loop puts in a path.
	maxOperation = 128
)

// The method and the statuses of the protocol that the loop reads. The
// domain imports no HTTP code; the transport is an adapter.
const (
	methodPost            = "POST"
	statusNoContent       = 204
	statusBadRequest      = 400
	statusTooManyRequests = 429
	statusServerError     = 500
)

// operationPattern is the allowlist of an operation ID in a path.
var operationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Handler runs one claimed phase or sweep. Its context ends when the claim is
// lost, when a cancel arrives or when the loop ends. It returns when the work
// ends.
type Handler func(ctx context.Context, work protocol.Work) error

// Config is the configuration of a loop.
type Config struct {
	// Server is the base URL of the server: http or https, with a host.
	Server string
	// Transport sends the requests to Server. It is required. It follows no
	// redirect: the proof of a request is for one path of one server.
	Transport session.Transport
	// RunnerID is the ID of the runner, the subject of its proofs.
	RunnerID string
	// Key is the key of the runner.
	Key ed25519.PrivateKey
	// Release is the release of the runner, as major.minor.patch.
	Release string
	// Lease is the lease of the pool of the runner, from runner.MinLease to
	// runner.MaxLease. The renewal of a claim uses the lease that the work
	// carries, and this value when the work carries none.
	Lease time.Duration
	// Wait is the longest wait of one poll, at most protocol.MaxPollWait.
	// Zero takes protocol.MaxPollWait.
	Wait time.Duration
	// Ticker makes a ticker with the period d. It returns the channel and
	// the function that stops it. Nil takes time.Ticker.
	Ticker func(d time.Duration) (<-chan time.Time, func())
	// Now returns the time of a proof. Nil takes time.Now.
	Now func() time.Time
	// Logger receives one record for each event of the loop. Nil discards.
	Logger *slog.Logger
}

// Loop is the poll loop of one runner. It runs one phase or sweep at a time.
// Make it with New and call Run once.
type Loop struct {
	cfg    Config
	client *session.Client
	log    *slog.Logger

	mu       sync.Mutex
	current  string
	stopWork context.CancelCauseFunc
	draining bool
}

// New checks cfg and returns the loop. It returns an error that wraps
// ErrInvalid for a key that is not an ed25519 key, a lease outside
// runner.MinLease to runner.MaxLease, a server URL that is not http or https
// with a host, or a missing runner ID, release or transport.
func New(cfg Config) (*Loop, error) {
	if err := checkIdentity(cfg); err != nil {
		return nil, err
	}

	if err := checkLimits(cfg); err != nil {
		return nil, err
	}

	if cfg.Wait == 0 {
		cfg.Wait = protocol.MaxPollWait
	}

	if cfg.Ticker == nil {
		cfg.Ticker = realTicker
	}

	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Loop{
		cfg: cfg, log: log,
		client: &session.Client{Server: cfg.Server, Transport: cfg.Transport, Release: cfg.Release, Now: cfg.Now},
		mu:     sync.Mutex{}, current: "", stopWork: nil, draining: false,
	}, nil
}

// checkIdentity checks the key, the runner ID, the release and the transport
// of cfg.
func checkIdentity(cfg Config) error {
	if len(cfg.Key) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: the key of the runner is not an ed25519 key", ErrInvalid)
	}

	if cfg.RunnerID == "" || cfg.Release == "" {
		return fmt.Errorf("%w: the runner ID and the release are required", ErrInvalid)
	}

	if cfg.Transport == nil {
		return fmt.Errorf("%w: the transport is required", ErrInvalid)
	}

	return nil
}

// checkLimits checks the lease, the server URL and the poll wait of cfg.
func checkLimits(cfg Config) error {
	if cfg.Lease < runner.MinLease || cfg.Lease > runner.MaxLease {
		return fmt.Errorf("%w: the lease must be from %v to %v", ErrInvalid, runner.MinLease, runner.MaxLease)
	}

	u, err := url.Parse(cfg.Server)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("%w: the server URL is not an http or https URL with a host", ErrInvalid)
	}

	if cfg.Wait < 0 || cfg.Wait > protocol.MaxPollWait {
		return fmt.Errorf("%w: the poll wait must be from 0 to %v", ErrInvalid, protocol.MaxPollWait)
	}

	return nil
}

// Run makes a loop from cfg and runs it.
func Run(ctx context.Context, cfg Config, handle Handler) error {
	l, err := New(cfg)
	if err != nil {
		return err
	}

	return l.Run(ctx, handle)
}

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)

	return t.C, t.Stop
}

// Run polls for work and calls handle for each claim until ctx ends, the
// server retires the runner, the server refuses the key, or the runner
// drains. It returns nil when ctx ends or the runner drains, and the error
// otherwise. A claim that is lost stops its work and the loop goes on.
func (l *Loop) Run(ctx context.Context, handle Handler) error {
	ctx, stop := context.WithCancelCause(ctx)
	defer stop(nil)

	var wg sync.WaitGroup

	wg.Go(func() { l.heartbeats(ctx, stop) })

	err := l.pollLoop(ctx, stop, handle)

	stop(err)
	wg.Wait()

	if errors.Is(err, errStopped) {
		return nil
	}

	return err
}

// pollLoop is the main loop. It returns the error that ends the loop.
func (l *Loop) pollLoop(ctx context.Context, stop context.CancelCauseFunc, handle Handler) error {
	for {
		if err := l.ended(ctx); err != nil {
			return err
		}

		w, found, err := l.poll(ctx)

		switch {
		case err == nil && !found:
			continue
		case err == nil:
			l.runWork(ctx, stop, w, handle)
		case errors.Is(err, ErrRateLimited):
			l.log.WarnContext(ctx, "the server limits the polls of the runner", slog.Any("error", err))

			l.wait(ctx, retryAfter(err))
		case errors.Is(err, ErrUnavailable):
			l.log.WarnContext(ctx, "the poll got no answer", slog.Any("error", err))

			l.wait(ctx, retryWait)
		default:
			if ctx.Err() != nil {
				continue
			}

			return fmt.Errorf("poll: %w", err)
		}

		l.mu.Lock()
		draining := l.draining
		l.mu.Unlock()

		if draining {
			l.log.InfoContext(ctx, "the runner drains: the loop ends")

			return nil
		}
	}
}

// ended returns the error that ended ctx, or nil when ctx is live. A cancel
// of the caller is not an error.
func (l *Loop) ended(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}

	cause := context.Cause(ctx)
	if errors.Is(cause, context.Canceled) {
		return errStopped
	}

	return fmt.Errorf("the loop ended: %w", cause)
}

// errStopped is the end of the loop by the caller. Run returns nil for it.
var errStopped = errors.New("the loop is stopped")

// wait waits for d, or until ctx ends. d is capped.
func (l *Loop) wait(ctx context.Context, d time.Duration) {
	d = min(max(d, time.Second), maxWait)

	c, stopTicker := l.cfg.Ticker(d)
	defer stopTicker()

	select {
	case <-ctx.Done():
	case <-c:
	}
}

// retryAfter returns the Retry-After of err, or retryWait.
func retryAfter(err error) time.Duration {
	var e *Error
	if errors.As(err, &e) && e.RetryAfter > 0 {
		return e.RetryAfter
	}

	return retryWait
}

// post sends a signed POST with a JSON body (nil sends none).
func (l *Loop) post(ctx context.Context, path string, body any) (*session.Response, error) {
	var raw []byte

	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return nil, fmt.Errorf("encode the body of %s: %w", path, err)
		}
	}

	resp, err := l.client.Signed(ctx, l.cfg.Key, l.cfg.RunnerID, &session.Request{
		Method: methodPost, Path: path, Body: raw, Header: map[string]string{"Content-Type": "application/json"},
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	return resp, nil
}

// answerError returns nil when resp has a status below 400 and an *Error
// otherwise.
func answerError(resp *session.Response) error {
	if resp.Status < statusBadRequest {
		return nil
	}

	var body protocol.Error

	e := &Error{
		Status: resp.Status, Code: "", Message: "the answer has the status " + strconv.Itoa(resp.Status),
		RetryAfter: resp.RetryAfter,
	}

	switch {
	case json.Unmarshal(resp.Body, &body) == nil && body.Code != "":
		e.Code, e.Message = body.Code, body.Message
	case resp.Status == statusTooManyRequests:
		// A proxy in front of the server answers 429 with no protocol body.
		e.Code = protocol.ErrorRateLimited
	case resp.Status >= statusServerError:
		e.Code = protocol.ErrorUnavailable
	default:
	}

	return e
}

// poll asks for one phase or sweep. It returns false and no error for an
// answer of 204.
func (l *Loop) poll(ctx context.Context) (protocol.Work, bool, error) {
	var none protocol.Work

	resp, err := l.post(ctx, protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(l.cfg.Wait), AssignmentVersion: runner.AssignmentVersion,
	})
	if err != nil {
		return none, false, err
	}

	if aerr := answerError(resp); aerr != nil {
		return none, false, aerr
	}

	if resp.Status == statusNoContent {
		return none, false, nil
	}

	var w protocol.Work
	if json.Unmarshal(resp.Body, &w) != nil {
		return none, false, fmt.Errorf("%w: the answer to the poll is not work", ErrInvalid)
	}

	// Defense in depth: the server makes the operation ID; this check stays
	// so that a hostile answer never changes the path of a renewal.
	if len(w.Operation) > maxOperation || !operationPattern.MatchString(w.Operation) {
		return none, false, fmt.Errorf("%w: the operation ID of the work is not valid", ErrInvalid)
	}

	return w, true, nil
}

// runWork runs handle for w and renews the claim while it runs.
func (l *Loop) runWork(ctx context.Context, stop context.CancelCauseFunc, w protocol.Work, handle Handler) {
	wctx, stopWork := context.WithCancelCause(ctx)
	defer stopWork(nil)

	l.mu.Lock()
	l.current, l.stopWork = w.Operation, stopWork
	l.mu.Unlock()

	defer func() {
		l.mu.Lock()
		l.current, l.stopWork = "", nil
		l.mu.Unlock()
	}()

	l.log.InfoContext(ctx, "the runner holds a claim", slog.String("operation", w.Operation))

	var wg sync.WaitGroup

	wg.Go(func() { l.renewals(wctx, stop, stopWork, w) })

	err := handle(wctx, w)

	cause := context.Cause(wctx)

	stopWork(nil)
	wg.Wait()

	switch {
	case errors.Is(cause, ErrClaimLost), errors.Is(cause, ErrCanceled):
		l.log.WarnContext(ctx, "the work stopped", slog.String("operation", w.Operation), slog.Any("cause", cause))
	case err != nil:
		l.log.WarnContext(ctx, "the work failed", slog.String("operation", w.Operation), slog.Any("error", err))
	default:
	}
}

// renewals renews the claim of w every third of its lease, until wctx ends.
func (l *Loop) renewals(
	wctx context.Context, stop, stopWork context.CancelCauseFunc, w protocol.Work,
) {
	lease := w.Lease.Std()
	if lease < runner.MinLease || lease > runner.MaxLease {
		lease = l.cfg.Lease
	}

	every := max(runner.RenewEvery(lease), minRenewEvery)
	tick, stopTicker := l.cfg.Ticker(every)

	defer stopTicker()

	expires := w.ExpiresAt

	for {
		select {
		case <-wctx.Done():
			return
		case <-tick:
		}

		var done bool
		if expires, done = l.renew(wctx, stop, stopWork, w.Operation, expires); done {
			return
		}
	}
}

// renew sends one renewal of the claim of op. It returns the last expiry
// that the server sent, and true when the renewals end.
func (l *Loop) renew(
	ctx context.Context, stop, stopWork context.CancelCauseFunc, op string, expires time.Time,
) (time.Time, bool) {
	path := strings.NewReplacer("{operation}", op).Replace(protocol.PathClaimRenew)

	resp, err := l.post(ctx, path, nil)
	if err == nil {
		err = answerError(resp)
	}

	switch {
	case err == nil:
		var rr protocol.RenewResponse
		if json.Unmarshal(resp.Body, &rr) == nil {
			if rr.Canceled {
				stopWork(ErrCanceled)
			}

			// With an open session, the launcher renews its own claim now.
			return rr.ExpiresAt, rr.Canceled || rr.SessionOpen
		}

		err = fmt.Errorf("%w: the answer to the renewal is not valid", ErrInvalid)
	case errors.Is(err, ErrRetired), errors.Is(err, ErrBadProof):
		stop(err)
		stopWork(err)

		return expires, true
	case errors.Is(err, ErrClaimLost), errors.Is(err, ErrNotFound):
		stopWork(fmt.Errorf("%w: %w", ErrClaimLost, err))

		return expires, true
	default:
	}

	l.log.WarnContext(ctx, "the renewal failed", slog.String("operation", op), slog.Any("error", err))

	// The expiry is the one that the server sent. A claim that the server
	// could not renew has ended at that time.
	if !expires.IsZero() && l.now().After(expires) {
		stopWork(fmt.Errorf("%w: the claim expired", ErrClaimLost))

		return expires, true
	}

	return expires, false
}

func (l *Loop) now() time.Time {
	if l.cfg.Now != nil {
		return l.cfg.Now()
	}

	return time.Now()
}

// heartbeats sends a heartbeat every runner.HeartbeatEvery until ctx ends.
func (l *Loop) heartbeats(ctx context.Context, stop context.CancelCauseFunc) {
	tick, stopTicker := l.cfg.Ticker(runner.HeartbeatEvery)
	defer stopTicker()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
		}

		l.mu.Lock()
		claims, free := []string{}, 1

		if l.current != "" {
			claims, free = []string{l.current}, 0
		}

		l.mu.Unlock()

		resp, err := l.post(ctx, protocol.PathHeartbeat, protocol.Heartbeat{Claims: claims, Free: free})
		if err == nil {
			err = answerError(resp)
		}

		if errors.Is(err, ErrRetired) || errors.Is(err, ErrBadProof) {
			stop(err)

			return
		}

		if err != nil {
			l.log.WarnContext(ctx, "the heartbeat failed", slog.Any("error", err))

			continue
		}

		var hr protocol.HeartbeatResponse
		if json.Unmarshal(resp.Body, &hr) != nil {
			l.log.WarnContext(ctx, "the answer to the heartbeat is not valid")

			continue
		}

		l.applyHeartbeat(hr)
	}
}

// applyHeartbeat stops the work that the heartbeat answer names and records
// a drain.
func (l *Loop) applyHeartbeat(hr protocol.HeartbeatResponse) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.draining = l.draining || hr.Draining

	if l.stopWork == nil {
		return
	}

	for _, op := range hr.Lost {
		if op == l.current {
			l.stopWork(ErrClaimLost)
		}
	}

	for _, op := range hr.Cancel {
		if op == l.current {
			l.stopWork(ErrCanceled)
		}
	}
}
