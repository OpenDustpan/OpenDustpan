package client_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

const (
	testServer = "https://dustpan.example"
	testOp     = "op-1"
	testRunner = "rnr-1"
)

// answer is one scripted answer of the fake server.
type answer struct {
	status     int
	body       any
	retryAfter time.Duration
}

// ok is an answer of 200 with body.
func ok(body any) answer {
	return answer{status: http.StatusOK, body: body, retryAfter: 0}
}

// noWork is an answer of 204.
func noWork() answer {
	return answer{status: http.StatusNoContent, body: nil, retryAfter: 0}
}

// refusal is an error answer with the protocol code.
func refusal(status int, code string, retryAfter time.Duration) answer {
	return answer{
		status:     status,
		body:       protocol.Error{Code: code, Message: "refused", Supported: ""},
		retryAfter: retryAfter,
	}
}

// fakeServer answers the routes of the runner protocol from a script for each
// path, and records each request.
type fakeServer struct {
	mu     sync.Mutex
	script map[string][]answer
	seen   []string
}

func newFakeServer(script map[string][]answer) *fakeServer {
	return &fakeServer{mu: sync.Mutex{}, script: script, seen: nil}
}

// Do implements session.Transport.
func (f *fakeServer) Do(_ context.Context, req *session.Request) (*session.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	path := req.Path
	if strings.HasPrefix(path, "/runner/v1/claims/") {
		path = protocol.PathClaimRenew
	}

	f.seen = append(f.seen, path)

	queue := f.script[path]
	if len(queue) == 0 {
		return &session.Response{Status: http.StatusNoContent, Body: nil, RetryAfter: 0}, nil
	}

	a := queue[0]
	if len(queue) > 1 {
		f.script[path] = queue[1:]
	}

	var raw []byte

	if a.body != nil {
		var err error
		if raw, err = json.Marshal(a.body); err != nil {
			return nil, fmt.Errorf("encode the answer: %w", err)
		}
	}

	return &session.Response{Status: a.status, Body: raw, RetryAfter: a.retryAfter}, nil
}

func (f *fakeServer) count(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, p := range f.seen {
		if p == path {
			n++
		}
	}

	return n
}

// waitFor checks cond every millisecond until it is true or ctx ends. It
// returns false when ctx ends first.
func waitFor(ctx context.Context, cond func() bool) bool {
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for !cond() {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}

	return true
}

// fakeTicks is a ticker that the test fires by hand. It records each period.
type fakeTicks struct {
	mu      sync.Mutex
	periods []time.Duration
	chans   map[time.Duration]chan time.Time
}

func newFakeTicks() *fakeTicks {
	return &fakeTicks{mu: sync.Mutex{}, periods: nil, chans: map[time.Duration]chan time.Time{}}
}

func (f *fakeTicks) ticker(d time.Duration) (<-chan time.Time, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.periods = append(f.periods, d)

	c := make(chan time.Time, 1)
	f.chans[d] = c

	return c, func() {}
}

// send sends one tick to the ticker with the period d, when it exists and
// has room.
func (f *fakeTicks) send(d time.Duration) bool {
	f.mu.Lock()
	c := f.chans[d]
	f.mu.Unlock()

	if c == nil {
		return false
	}

	select {
	case c <- time.Now():
		return true
	default:
		return false
	}
}

// fire sends one tick to the ticker with the period d, once it exists.
func (f *fakeTicks) fire(ctx context.Context, t *testing.T, d time.Duration) {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if !waitFor(ctx, func() bool { return f.send(d) }) {
		t.Errorf("no ticker with the period %v", d)
	}
}

func (f *fakeTicks) has(d time.Duration) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Contains(f.periods, d)
}

func testConfig(t *testing.T, srv *fakeServer, ticks *fakeTicks) client.Config {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return client.Config{
		Server: testServer, Transport: srv, RunnerID: testRunner, Key: priv, Release: "0.0.1",
		Lease: runner.DefaultLease, Wait: time.Second, Ticker: ticks.ticker, Now: nil, Logger: nil,
	}
}

func work(lease time.Duration) protocol.Work {
	return protocol.Work{
		Operation: testOp, Token: 1, ExpiresAt: time.Now().Add(lease), Lease: protocol.FromStd(lease),
		Job: "job-1", Phase: 1, Sweep: "", Image: "", ServiceAccount: "",
		Deadline: protocol.FromStd(time.Hour), Grace: protocol.FromStd(time.Minute),
	}
}

func TestLoopPollsAndRenewsAfterAThirdOfTheLease(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(work(lease))},
		protocol.PathClaimRenew: {ok(protocol.RenewResponse{
			ExpiresAt: time.Now().Add(lease), Canceled: false, SessionOpen: false,
		})},
	})
	ticks := newFakeTicks()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	renewed := make(chan struct{})

	go func() {
		ticks.fire(ctx, t, runner.RenewEvery(lease))
		waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) > 0 })
		close(renewed)
	}()

	err = l.Run(ctx, func(context.Context, protocol.Work) error {
		<-renewed
		cancel()

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !ticks.has(runner.RenewEvery(lease)) {
		t.Errorf("no renew ticker of %v", runner.RenewEvery(lease))
	}

	if !ticks.has(runner.HeartbeatEvery) {
		t.Errorf("no heartbeat ticker of %v", runner.HeartbeatEvery)
	}

	if srv.count(protocol.PathPoll) < 1 || srv.count(protocol.PathClaimRenew) != 1 {
		t.Errorf(
			"polls = %d, renews = %d; want >= 1 and 1",
			srv.count(protocol.PathPoll), srv.count(protocol.PathClaimRenew),
		)
	}
}

func TestLoopHeartbeatsEveryFifteenSeconds(t *testing.T) {
	t.Parallel()

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {noWork()},
		protocol.PathHeartbeat: {ok(protocol.HeartbeatResponse{
			Cancel: nil, Lost: nil, RotateKey: false, Draining: false,
		})},
	})
	ticks := newFakeTicks()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	go func() {
		ticks.fire(ctx, t, runner.HeartbeatEvery)
		waitFor(ctx, func() bool { return srv.count(protocol.PathHeartbeat) > 0 })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if srv.count(protocol.PathHeartbeat) != 1 {
		t.Errorf("heartbeats = %d, want 1", srv.count(protocol.PathHeartbeat))
	}
}

// renewCase is a renewal answer that stops the work or ends the loop.
type renewCase struct {
	name     string
	answer   answer
	wantFail error // the error of Run, nil when the loop goes on
	wantStop error // the cause that the work sees
}

func TestLoopMapsProtocolCodes(t *testing.T) {
	t.Parallel()

	renewCases := []renewCase{
		{
			"claim-lost stops the work", refusal(http.StatusConflict, protocol.ErrorClaimLost, 0),
			nil, client.ErrClaimLost,
		},
		{
			"retired ends the loop", refusal(http.StatusForbidden, protocol.ErrorRetired, 0),
			client.ErrRetired, client.ErrRetired,
		},
		{
			"bad-proof ends the loop", refusal(http.StatusUnauthorized, protocol.ErrorBadProof, 0),
			client.ErrBadProof, client.ErrBadProof,
		},
	}

	for _, tc := range renewCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRenewCase(t, tc)
		})
	}

	t.Run("rate-limited waits for Retry-After", func(t *testing.T) {
		t.Parallel()
		checkRetryAfter(t)
	})

	t.Run("retired on a poll ends the loop", func(t *testing.T) {
		t.Parallel()
		checkRetiredOnPoll(t)
	})
}

func checkRenewCase(t *testing.T, tc renewCase) {
	t.Helper()

	lease := runner.DefaultLease
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:       {ok(work(lease))},
		protocol.PathClaimRenew: {tc.answer},
	})
	ticks := newFakeTicks()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	go ticks.fire(ctx, t, runner.RenewEvery(lease))

	var stopped error

	err = l.Run(ctx, func(ctx context.Context, _ protocol.Work) error {
		select {
		case <-ctx.Done():
			stopped = context.Cause(ctx)
		case <-time.After(5 * time.Second):
		}

		if tc.wantFail == nil {
			cancel()
		}

		return nil
	})

	if !errors.Is(stopped, tc.wantStop) {
		t.Errorf("work stopped with %v, want %v", stopped, tc.wantStop)
	}

	if tc.wantFail == nil && err != nil {
		t.Errorf("Run = %v, want nil", err)
	}

	if tc.wantFail != nil && !errors.Is(err, tc.wantFail) {
		t.Errorf("Run = %v, want %v", err, tc.wantFail)
	}
}

func checkRetryAfter(t *testing.T) {
	t.Helper()

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {
			refusal(http.StatusTooManyRequests, protocol.ErrorRateLimited, 7*time.Second),
			noWork(),
		},
	})
	ticks := newFakeTicks()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	go func() {
		ticks.fire(ctx, t, 7*time.Second)
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 2 })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !ticks.has(7 * time.Second) {
		t.Error("no wait of the Retry-After 7s")
	}
}

func checkRetiredOnPoll(t *testing.T) {
	t.Helper()

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {refusal(http.StatusForbidden, protocol.ErrorRetired, 0)},
	})

	l, err := client.New(testConfig(t, srv, newFakeTicks()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = l.Run(t.Context(), func(context.Context, protocol.Work) error { return nil })
	if !errors.Is(err, client.ErrRetired) {
		t.Errorf("Run = %v, want ErrRetired", err)
	}
}

func TestNewRejectsHostileConfig(t *testing.T) {
	t.Parallel()

	good := testConfig(t, newFakeServer(nil), newFakeTicks())

	cases := map[string]func(c *client.Config){
		"empty key":           func(c *client.Config) { c.Key = nil },
		"short key":           func(c *client.Config) { c.Key = ed25519.PrivateKey(make([]byte, 5)) },
		"lease below minimum": func(c *client.Config) { c.Lease = runner.MinLease - time.Second },
		"lease above maximum": func(c *client.Config) { c.Lease = runner.MaxLease + time.Second },
		"file scheme":         func(c *client.Config) { c.Server = "file:///etc/passwd" },
		"ftp scheme":          func(c *client.Config) { c.Server = "ftp://dustpan.example" },
		"no host":             func(c *client.Config) { c.Server = "https://" },
		"empty runner":        func(c *client.Config) { c.RunnerID = "" },
		"empty release":       func(c *client.Config) { c.Release = "" },
		"no transport":        func(c *client.Config) { c.Transport = nil },
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := good
			mutate(&cfg)

			if _, err := client.New(cfg); err == nil {
				t.Errorf("New accepted %s", name)
			}
		})
	}

	if _, err := client.New(good); err != nil {
		t.Errorf("New refused a good config: %v", err)
	}
}

func TestLoopRefusesAHostileOperation(t *testing.T) {
	t.Parallel()

	hostile := work(runner.DefaultLease)
	hostile.Operation = "../../admin"

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(hostile)},
	})

	l, err := client.New(testConfig(t, srv, newFakeTicks()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = l.Run(t.Context(), func(context.Context, protocol.Work) error {
		t.Error("the handler ran for a hostile operation")

		return nil
	})
	if err == nil {
		t.Fatal("Run = nil, want an error")
	}
}
