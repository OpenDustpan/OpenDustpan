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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
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
	// maxAnswer is the largest answer body that the loop reads. The answers
	// of poll, renew and heartbeat are small JSON documents, at most
	// protocol.MaxBody. A larger body comes from a hostile or broken server
	// or proxy. The loop reads the body stream itself, at most maxAnswer+1
	// bytes, and refuses a body above maxAnswer before it decodes it, so that
	// one answer cannot use up the memory of the runner. A transport cannot
	// skip this bound: it gives a stream, not the read body.
	maxAnswer = protocol.MaxBody
)

// Transport sends one request of the runner protocol and returns the answer.
// It returns an error only when it got no answer. It follows no redirect: the
// proof of a request is for one path of one server.
type Transport interface {
	Do(ctx context.Context, req *session.Request) (*Answer, error)
}

// Answer is the answer of the server to one request. The loop reads at most
// maxAnswer+1 bytes of Body and closes it.
type Answer struct {
	Status int
	// RetryAfter is the value of the header Retry-After, or 0.
	RetryAfter time.Duration
	// Body is the body stream of the answer. Nil is an empty body.
	Body io.ReadCloser
}

// The method and the statuses of the protocol that the loop reads. The
// domain imports no HTTP code; the transport is an adapter.
const (
	methodPost            = "POST"
	statusNoContent       = 204
	statusOK              = 200
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
	// Transport sends the requests to Server. It is required.
	Transport Transport
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
	cfg Config
	log *slog.Logger

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

	if cfg.Random == nil {
		cfg.Random = CryptoRandom(rand.Reader, log)
	}

	return &Loop{
		cfg: cfg, log: log,
		mu: sync.Mutex{}, current: "", stopWork: nil, draining: false,
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

// CryptoRandom returns the random source of a backoff that reads r: a
// function that returns a number from 0 to n-1. New takes it with
// crypto/rand.Reader when Config.Random is nil. The function returns 0 for n
// below 1. When r fails, it returns the upper bound n-1, so that the backoff
// takes its longest wait and does not poll faster, and writes a log record to
// log that names the error.
func CryptoRandom(r io.Reader, log *slog.Logger) func(n int64) int64 {
	return func(n int64) int64 {
		if n < 1 {
			return 0
		}

		v, err := rand.Int(r, big.NewInt(n))
		if err != nil {
			log.Warn("the random source failed: the backoff takes its upper bound",
				errorAttr("error", err), slog.Int64("upper", n-1))

			return n - 1
		}

		return v.Int64()
	}
}

// Run polls for work and calls handle for each claim until ctx ends, the
// server retires the runner, the server refuses the key or an answer, or the
// runner drains. It returns nil when the caller cancels ctx or the runner
// drains. When the deadline of ctx passes, it returns an error that wraps
// context.DeadlineExceeded. It returns the error that ends the loop
// otherwise. A claim that is lost stops its work and the loop goes on.
//
// Each wait of the loop is at most runner.MinLease (30 s), so that one wait
// never outlasts the shortest claim lease. A Retry-After above that cap
// writes a log record with the asked wait and the cap, and the loop waits for
// the cap.
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

		// Each turn checks the drain first, also after a poll with no work.
		l.mu.Lock()
		draining := l.draining
		l.mu.Unlock()

		if draining {
			l.log.InfoContext(ctx, "the runner drains: the loop ends")

			return nil
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
		default:
			failures = 0

			l.runWork(ctx, stop, w, handle)
		}
	}
}

// pollFailed waits after a poll that failed with err and returns nil, or
// returns the error that ends the loop. failures counts the polls in a row
// that got no answer or a status of 500 or more.
func (l *Loop) pollFailed(ctx context.Context, err error, failures *int) error {
	switch {
	case ctx.Err() != nil:
		// The poll failed because ctx ended: the end of ctx ends the loop,
		// with no warning and no backoff.
		return l.ended(ctx)
	case errors.Is(err, ErrRateLimited):
		*failures = 0

		l.log.WarnContext(ctx, "the server limits the polls of the runner", errorAttr("error", err))

		l.wait(ctx, retryAfter(err))
	case errors.Is(err, ErrUnavailable):
		d := l.backoff(*failures)
		*failures++

		l.log.WarnContext(ctx, "the poll failed: the loop waits and polls again",
			errorAttr("error", err), slog.Duration("wait", d))

		l.wait(ctx, d)
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

// wait waits for d, or until ctx ends. d is from 1 s to maxWait. A d above
// maxWait writes a log record with both values.
func (l *Loop) wait(ctx context.Context, d time.Duration) {
	if d > maxWait {
		l.log.WarnContext(ctx, "the wait that the server asks for is above the cap: the loop waits for the cap",
			slog.Duration("asked", d), slog.Duration("cap", maxWait))
	}

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

// post sends a signed POST with a JSON body (nil sends none). It returns the
// answer as a value, so that no caller can read a nil answer: each call of
// the poll, the renewal and the heartbeat reads its body with no nil check.
func (l *Loop) post(ctx context.Context, path string, body any) (session.Response, error) {
	var (
		raw  []byte
		none session.Response
	)

	if body != nil {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			return none, fmt.Errorf("encode the body of %s: %w", path, err)
		}
	}

	sum := sha256.Sum256(raw)

	proof, err := session.Sign(l.cfg.Key, l.cfg.RunnerID,
		runner.Signed{Method: methodPost, PathQuery: path, BodySHA256: sum[:]}, l.now())
	if err != nil {
		return none, fmt.Errorf("sign %s: %w", path, err)
	}

	ans, err := l.cfg.Transport.Do(ctx, &session.Request{
		Method: methodPost, Path: path, Body: raw, Header: map[string]string{
			"Content-Type": "application/json", protocol.HeaderProof: proof, protocol.HeaderRelease: l.cfg.Release,
		},
	})
	if err != nil {
		return none, l.noAnswer(ctx, path, err)
	}

	// The one guard of a transport that breaks its contract, for each call.
	if ans == nil {
		l.refused(ctx, path, "the transport gave no answer and no error")

		return none, fmt.Errorf("%w: %s: the transport gave no answer and no error", ErrInvalid, path)
	}

	data, err := l.readBody(ctx, path, ans.Body)
	if err != nil {
		return none, err
	}

	return session.Response{Status: ans.Status, Body: data, RetryAfter: ans.RetryAfter}, nil
}

// noAnswer returns the error of a call to path that got no answer. A call
// that the end of ctx cut short is not a fault of the server: it returns the
// cause of the end of ctx, not ErrUnavailable, so that the loop neither warns
// nor backs off.
func (l *Loop) noAnswer(ctx context.Context, path string, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", path, context.Cause(ctx))
	}

	return fmt.Errorf("%w: %s: %w", ErrUnavailable, path, err)
}

// readBody reads at most maxAnswer+1 bytes of the answer body of path, closes
// it, and refuses a body above maxAnswer.
func (l *Loop) readBody(ctx context.Context, path string, rc io.ReadCloser) ([]byte, error) {
	if rc == nil {
		return nil, nil
	}

	body, readErr := io.ReadAll(io.LimitReader(rc, maxAnswer+1))
	if err := errors.Join(readErr, rc.Close()); err != nil {
		return nil, l.noAnswer(ctx, path, err)
	}

	if len(body) > maxAnswer {
		l.refused(ctx, path, "the answer body is above the limit", slog.Int("limit", maxAnswer))

		return nil, fmt.Errorf("%w: %s: more than %d bytes", ErrAnswerTooLarge, path, maxAnswer)
	}

	return body, nil
}

// refused writes the log record of an answer that the loop refuses, with its
// reason.
func (l *Loop) refused(ctx context.Context, path, reason string, attrs ...slog.Attr) {
	all := append([]slog.Attr{slog.String("path", path), slog.String("reason", reason)}, attrs...)

	l.log.LogAttrs(ctx, slog.LevelWarn, "the runner refused an answer of the server", all...)
}

// answerError returns nil when resp is an answer that the loop accepts, and
// an *Error otherwise. A poll accepts a 200 with work and a 204 with no work;
// a renewal and a heartbeat accept a 200. Any other status is an error, and
// the loop fails closed.
func answerError(resp session.Response) error {
	var body protocol.Error

	e := &Error{
		Status: resp.Status, Code: "", Message: "the answer has the status " + strconv.Itoa(resp.Status),
		RetryAfter: resp.RetryAfter,
	}

	if json.Unmarshal(resp.Body, &body) == nil && body.Code != "" {
		e.Code, e.Message = logText(body.Code), logText(body.Message)
	}

	switch {
	case resp.Status == statusNoContent:
		// A poll with a 204 has no work.
		return nil
	case resp.Status == statusOK:
		// A 200 is a success: with a code, a refusal; without, the answer.
		if e.Code == "" {
			return nil
		}

		return e
	case resp.Status == statusTooManyRequests:
		// A proxy in front of the server answers 429 with no protocol body.
		e.Code = protocol.ErrorRateLimited

		return e
	case resp.Status >= statusServerError:
		e.Code = protocol.ErrorUnavailable

		return e
	case resp.Status < statusBadRequest:
		// A status below 400 that is not a success of the loop: fail closed.
		return e
	default:
		return e
	}
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

	// The loop holds no expiry that the server did not send: work with no
	// expiry is refused.
	if w.ExpiresAt.IsZero() {
		l.refused(ctx, protocol.PathPoll, "the work has no expiry", slog.String("operation", w.Operation))

		return none, false, fmt.Errorf("%w: the work has no expiry", ErrInvalid)
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
		l.log.WarnContext(ctx, "the work stopped", slog.String("operation", w.Operation), errorAttr("cause", cause))
	case err != nil:
		l.log.WarnContext(ctx, "the work failed", slog.String("operation", w.Operation), errorAttr("error", err))
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
		return l.renewed(ctx, stops{loop: stop, work: stopWork}, op, resp.Body, expires)
	case ctx.Err() != nil:
		return expires, true
	case errors.Is(err, ErrRetired), errors.Is(err, ErrBadProof), errors.Is(err, ErrInvalid):
		stop(err)
		stopWork(err)

		return expires, true
	case errors.Is(err, ErrClaimLost), errors.Is(err, ErrNotFound):
		stopWork(fmt.Errorf("%w: %w", ErrClaimLost, err))

		return expires, true
	case !errors.Is(err, ErrUnavailable):
		// Fail closed: a code that the loop does not know stops the work.
		l.refused(ctx, path, "the code of the answer is not known",
			slog.String("operation", op), errorAttr("error", err))
		stopWork(err)

		return expires, true
	default:
	}

	l.log.WarnContext(ctx, "the renewal failed", slog.String("operation", op), errorAttr("error", err))

	// The expiry is the one that the server sent. A claim that the server
	// could not renew has ended at that time.
	if !expires.IsZero() && l.now().After(expires) {
		stopWork(fmt.Errorf("%w: the claim expired", ErrClaimLost))

		return expires, true
	}

	return expires, false
}

// stops holds the functions that end the loop and the work of one claim.
type stops struct {
	loop context.CancelCauseFunc
	work context.CancelCauseFunc
}

// renewed reads the body of a renewal answer of 200 for the claim of op. It
// returns the expiry that the server sent, and true when the renewals end.
func (l *Loop) renewed(ctx context.Context, s stops, op string, body []byte, expires time.Time) (time.Time, bool) {
	path := strings.NewReplacer("{operation}", op).Replace(protocol.PathClaimRenew)

	var rr protocol.RenewResponse
	if json.Unmarshal(body, &rr) != nil {
		l.refused(ctx, path, "the answer is not a renewal", slog.String("operation", op))

		err := fmt.Errorf("%w: the answer to the renewal is not valid", ErrInvalid)

		s.loop(err)
		s.work(err)

		return expires, true
	}

	// The loop holds no expiry that the server did not send.
	if rr.ExpiresAt.IsZero() {
		l.refused(ctx, path, "the renewal has no expiry", slog.String("operation", op))
		s.work(fmt.Errorf("%w: the renewal of the claim has no expiry", ErrInvalid))

		return expires, true
	}

	if rr.Canceled {
		s.work(ErrCanceled)

		return rr.ExpiresAt, true
	}

	if rr.SessionOpen {
		// With an open session, the launcher renews its own claim now.
		l.log.InfoContext(ctx, "the runner stops the renewals of the claim",
			slog.String("operation", op), slog.String("reason", "the launcher opened the job session"))

		return rr.ExpiresAt, true
	}

	return rr.ExpiresAt, false
}

// maxLogText is the longest text from outside the loop that one log
// attribute holds: the message of a server answer or the text of an error. A
// longer text is cut at this length.
const maxLogText = 256

// unknownCode is the logged value of a code that is not a code of the runner
// protocol.
const unknownCode = "unknown"

// isProtocolCode reports whether code is a code of the runner protocol: the
// allowlist of the codes that a log record names.
func isProtocolCode(code string) bool {
	switch code {
	case protocol.ErrorInvalid, protocol.ErrorBadProof, protocol.ErrorKeyExpired, protocol.ErrorRetired,
		protocol.ErrorGrantLost, protocol.ErrorTokenUsed, protocol.ErrorTokenExpired, protocol.ErrorNotFound,
		protocol.ErrorClaimLost, protocol.ErrorCanceled, protocol.ErrorConflict, protocol.ErrorSourceNotReady,
		protocol.ErrorPlanStale, protocol.ErrorVersionUnsupported, protocol.ErrorVersionAhead,
		protocol.ErrorRateLimited, protocol.ErrorUnavailable:
		return true
	default:
		return false
	}
}

// errorAttr returns the log attribute key of err. It is the one way that the
// loop puts an error in a log record, because the code, the message and the
// text of an error can come from the server or the network. For an answer of
// the server, the group holds the status, the code when it is a code of the
// protocol (else "unknown" and the length of the code) and the message. For
// any other error, it holds the text. Each text has no control character and
// is at most maxLogText long, so that it cannot forge or flood a record.
func errorAttr(key string, err error) slog.Attr {
	if err == nil {
		return slog.GroupAttrs(key)
	}

	e, found := errors.AsType[*Error](err)
	if !found {
		return slog.GroupAttrs(key, slog.String("text", logText(err.Error())))
	}

	message := slog.String("message", logText(e.Message))

	if !isProtocolCode(e.Code) {
		return slog.GroupAttrs(key, slog.Int("status", e.Status), slog.String("code", unknownCode),
			slog.Int("code_length", len(e.Code)), message)
	}

	return slog.GroupAttrs(key, slog.Int("status", e.Status), slog.String("code", e.Code), message)
}

// logText returns s with no hidden rune (a control character such as CR or
// LF, a format character, a line or a paragraph separator), cut to at most
// maxLogText bytes at a rune boundary.
func logText(s string) string {
	var b strings.Builder

	for _, r := range s {
		if ident.IsHiddenRune(r) {
			continue
		}

		if b.Len()+utf8.RuneLen(r) > maxLogText {
			break
		}

		b.WriteRune(r)
	}

	return b.String()
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

		switch {
		case err == nil:
		case ctx.Err() != nil:
			return
		case errors.Is(err, ErrUnavailable):
			l.log.WarnContext(ctx, "the heartbeat failed", errorAttr("error", err))

			continue
		case errors.Is(err, ErrRetired), errors.Is(err, ErrBadProof), errors.Is(err, ErrInvalid):
			l.log.WarnContext(ctx, "the server does not accept the runner: the loop ends", errorAttr("error", err))
			stop(err)

			return
		default:
			// Fail closed: a code that the loop does not know ends the loop.
			l.refused(ctx, protocol.PathHeartbeat, "the code of the answer is not known",
				errorAttr("error", err))
			stop(err)

			return
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
