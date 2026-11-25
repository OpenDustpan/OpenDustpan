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
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
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
	// ErrAnswerTooLarge reports an answer body above maxAnswer. It wraps
	// ErrInvalid.
	ErrAnswerTooLarge = fmt.Errorf("%w: the answer body is too large", ErrInvalid)
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
	// A status of 500 or more is a fault of the server: the loop polls again
	// after a backoff, whatever the code.
	if e.Status >= statusServerError && target == ErrUnavailable {
		return true
	}

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
	// retryWait is the wait after a rate limit with no Retry-After.
	retryWait = 5 * time.Second
	// minBackoff and maxBackoff bound the wait after a poll that got no
	// answer or a status of 500 or more. The wait doubles after each failure
	// and has a random part, so that many runners do not poll again at the
	// same time.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
	// maxDoublings is the count of failures after which the upper bound of
	// the backoff is maxBackoff: minBackoff doubled 5 times is above it. The
	// bound keeps the shift small.
	maxDoublings = 5
	// minRenewEvery is the shortest time between two renewals.
	minRenewEvery = time.Second
	// maxOperation is the longest operation ID that the loop puts in a path.
	maxOperation = 128
	// maxAnswer is the largest answer body that the loop decodes. The answers
	// of poll, renew and heartbeat are small JSON documents, at most
	// protocol.MaxBody. A larger body comes from a hostile or broken server
	// or proxy: the loop refuses it before it decodes it, so that one answer
	// cannot use up the memory of the runner. The transport must also bound
	// its read (at most maxAnswer+1 bytes); this check does not rely on it.
	maxAnswer = protocol.MaxBody
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
	// Lease is the lease of the pool of the runner, from the join answer,
	// from runner.MinLease to runner.MaxLease. The renewal of a claim uses
	// only the lease that the claim carries. A claim with another lease
	// writes a log record.
	Lease time.Duration
	// Wait is the longest wait of one poll, at most protocol.MaxPollWait.
	// Zero takes protocol.MaxPollWait.
	Wait time.Duration
	// Ticker makes a ticker with the period d. It returns the channel and
	// the function that stops it. Nil takes time.Ticker.
	Ticker func(d time.Duration) (<-chan time.Time, func())
	// Now returns the time of a proof. Nil takes time.Now.
	Now func() time.Time
	// Random returns a number from 0 to n-1, for the random part of a
	// backoff. Nil takes crypto/rand.
	Random func(n int64) int64
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

	if cfg.Random == nil {
		cfg.Random = cryptoRandom
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

// cryptoRandom returns a number from 0 to n-1 from crypto/rand. It returns 0
// for n below 1, or when crypto/rand fails: the backoff then has no random
// part, and its upper bound stays.
func cryptoRandom(n int64) int64 {
	if n < 1 {
		return 0
	}

	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		return 0
	}

	return v.Int64()
}

// Run polls for work and calls handle for each claim until ctx ends, the
// server retires the runner, the server refuses the key or an answer, or the
// runner drains. It returns nil when the caller cancels ctx or the runner
// drains. When the deadline of ctx passes, it returns an error that wraps
// context.DeadlineExceeded. It returns the error that ends the loop
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
	failures := 0

	for {
		if err := l.ended(ctx); err != nil {
			return err
		}

		w, found, err := l.poll(ctx)

		switch {
		case err != nil:
			if end := l.pollFailed(ctx, err, &failures); end != nil {
				return end
			}
		case !found:
			failures = 0

			l.log.InfoContext(ctx, "the poll found no work: the loop polls again")

			continue
		default:
			failures = 0

			l.runWork(ctx, stop, w, handle)
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

// pollFailed waits after a poll that failed with err and returns nil, or
// returns the error that ends the loop. failures counts the polls in a row
// that got no answer or a status of 500 or more.
func (l *Loop) pollFailed(ctx context.Context, err error, failures *int) error {
	switch {
	case errors.Is(err, ErrRateLimited):
		*failures = 0

		l.log.WarnContext(ctx, "the server limits the polls of the runner", slog.Any("error", err))

		l.wait(ctx, retryAfter(err))
	case errors.Is(err, ErrUnavailable):
		d := l.backoff(*failures)
		*failures++

		l.log.WarnContext(ctx, "the poll failed: the loop waits and polls again",
			slog.Any("error", err), slog.Duration("wait", d))

		l.wait(ctx, d)
	case ctx.Err() != nil:
		// The poll failed because ctx ended: the end of ctx ends the loop.
		return l.ended(ctx)
	default:
		return fmt.Errorf("poll: %w", err)
	}

	return nil
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

// backoff returns the wait after failures earlier failed polls in a row: a
// random time from minBackoff to an upper bound that doubles with each
// failure, from minBackoff to maxBackoff.
func (l *Loop) backoff(failures int) time.Duration {
	upper := maxBackoff
	if failures < maxDoublings {
		upper = min(minBackoff<<failures, maxBackoff)
	}

	spread := int64(upper - minBackoff)

	r := l.cfg.Random(spread + 1)
	// Defense in depth: Random is a caller hook; this check stays so that a
	// bad value never makes the wait leave the bounds.
	if r < 0 || r > spread {
		r = spread
	}

	return minBackoff + time.Duration(r)
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

	if len(resp.Body) > maxAnswer {
		l.refused(ctx, path, "the answer body is above the limit", slog.Int("bytes", len(resp.Body)))

		return nil, fmt.Errorf("%w: %s: %d bytes, at most %d", ErrAnswerTooLarge, path, len(resp.Body), maxAnswer)
	}

	return resp, nil
}

// refused writes the log record of an answer that the loop refuses, with its
// reason.
func (l *Loop) refused(ctx context.Context, path, reason string, attrs ...slog.Attr) {
	all := append([]slog.Attr{slog.String("path", path), slog.String("reason", reason)}, attrs...)

	l.log.LogAttrs(ctx, slog.LevelWarn, "the runner refused an answer of the server", all...)
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
		l.refused(ctx, protocol.PathPoll, "the answer is not work")

		return none, false, fmt.Errorf("%w: the answer to the poll is not work", ErrInvalid)
	}

	// Defense in depth: the server makes the operation ID; this check stays
	// so that a hostile answer never changes the path of a renewal.
	if len(w.Operation) > maxOperation || !operationPattern.MatchString(w.Operation) {
		l.refused(ctx, protocol.PathPoll, "the operation ID of the work is not valid",
			slog.Int("length", len(w.Operation)))

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
	case cause != nil && !errors.Is(cause, context.Canceled):
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
	// The renewals use the lease of the claim only, as the server sent it.
	// A lease outside the bounds of a pool is a hostile or broken answer: the
	// work stops, with no lease of another source.
	lease := w.Lease.Std()
	if lease < runner.MinLease || lease > runner.MaxLease {
		l.refused(wctx, protocol.PathPoll, "the lease of the claim is out of range",
			slog.String("operation", w.Operation), slog.Duration("lease", lease))
		stopWork(fmt.Errorf("%w: the lease %v of the claim is not from %v to %v",
			ErrInvalid, lease, runner.MinLease, runner.MaxLease))

		return
	}

	if lease != l.cfg.Lease {
		l.log.InfoContext(wctx, "the lease of the claim is not the lease of the pool from the join",
			slog.String("operation", w.Operation), slog.Duration("lease", lease),
			slog.Duration("pool_lease", l.cfg.Lease))
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

		l.refused(ctx, path, "the answer is not a renewal")

		err = fmt.Errorf("%w: the answer to the renewal is not valid", ErrInvalid)

		stop(err)
		stopWork(err)

		return expires, true
	case errors.Is(err, ErrRetired), errors.Is(err, ErrBadProof), errors.Is(err, ErrInvalid):
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

		if errors.Is(err, ErrRetired) || errors.Is(err, ErrBadProof) || errors.Is(err, ErrInvalid) {
			stop(err)

			return
		}

		if err != nil {
			l.log.WarnContext(ctx, "the heartbeat failed", slog.Any("error", err))

			continue
		}

		var hr protocol.HeartbeatResponse
		if json.Unmarshal(resp.Body, &hr) != nil {
			l.refused(ctx, protocol.PathHeartbeat, "the answer is not a heartbeat answer")
			stop(fmt.Errorf("%w: the answer to the heartbeat is not valid", ErrInvalid))

			return
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
