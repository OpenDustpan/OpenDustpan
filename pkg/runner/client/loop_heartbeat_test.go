package client_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// noAnswerReason is the reason of the record of a transport that gave no
// answer and no error.
const noAnswerReason = "the transport gave no answer and no error"

// nilOnPath answers the calls to its path with no answer and no error, and
// each other call from srv.
type nilOnPath struct {
	srv  *fakeServer
	path string
}

// Do implements client.Transport.
func (n nilOnPath) Do(ctx context.Context, req *session.Request) (*client.Answer, error) {
	if req.Path == n.path || strings.HasPrefix(req.Path, "/runner/v1/claims/") && n.path == protocol.PathClaimRenew {
		var none *client.Answer

		return none, nil
	}

	return n.srv.Do(ctx, req)
}

// nilLoop returns a loop whose transport answers path with no answer and no
// error, and a log capture.
func nilLoop(t *testing.T, srv *fakeServer, ticks *fakeTicks, path string) (*client.Loop, logCapture) {
	t.Helper()

	logs := newLogCapture()
	cfg := testConfig(t, srv, ticks)
	cfg.Transport = nilOnPath{srv: srv, path: path}
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return l, logs
}

func TestLoopRefusesANilRenewalAnswer(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {ok(work(lease))}})
	l, logs := nilLoop(t, srv, ticks, protocol.PathClaimRenew)

	go ticks.fire(t.Context(), t, runner.RenewEvery(lease))

	res := boundedRun(t, l)
	if !errors.Is(res.stopped, client.ErrInvalid) || !errors.Is(res.run, client.ErrInvalid) {
		t.Errorf("Run = %v, work stopped with %v; want ErrInvalid for both", res.run, res.stopped)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, noAnswerReason) {
		t.Error("no refusal record of the renewal with no answer")
	}
}

func TestLoopRefusesANilHeartbeatAnswer(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {noWork()}})
	l, logs := nilLoop(t, srv, ticks, protocol.PathHeartbeat)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrInvalid) {
		t.Errorf("Run = %v, want ErrInvalid", res.run)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, noAnswerReason) {
		t.Error("no refusal record of the heartbeat with no answer")
	}
}

// endedMessage is the record of a heartbeat answer that ends the loop with
// the code retired or bad-proof.
const endedMessage = "the server does not accept the runner: the loop ends"

func TestLoopEndsOnAHeartbeatAnsweredRetiredOrBadProof(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code   string
		status int
		want   error
	}{
		{protocol.ErrorRetired, http.StatusForbidden, client.ErrRetired},
		{protocol.ErrorBadProof, http.StatusUnauthorized, client.ErrBadProof},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			ticks := newFakeTicks()
			srv := newFakeServer(map[string][]answer{
				protocol.PathPoll:      {noWork()},
				protocol.PathHeartbeat: {refusal(tc.status, tc.code, 0)},
			})
			l, logs := loggedLoop(t, srv, ticks)

			go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

			if res := boundedRun(t, l); !errors.Is(res.run, tc.want) {
				t.Errorf("Run = %v, want %v", res.run, tc.want)
			}

			if !logs.hasAttrs(endedMessage, map[string]string{"error.code": tc.code}) {
				t.Errorf("no record that the loop ends on %s", tc.code)
			}

			if logs.has(slog.LevelWarn, refusedMessage, unknownCodeMessage) {
				t.Errorf("the code %s was logged as a code that the loop does not know", tc.code)
			}
		})
	}
}

// TestHeartbeatRefusalStopsTheWorkAndKeepsTheLoop checks that a heartbeat
// answered claim-lost or not-found stops the work, records the code of the
// answer and lets the loop poll again.
func TestHeartbeatRefusalStopsTheWorkAndKeepsTheLoop(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		code   string
		status int
		want   error
	}{
		{protocol.ErrorClaimLost, http.StatusConflict, client.ErrClaimLost},
		{protocol.ErrorNotFound, http.StatusNotFound, client.ErrNotFound},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			checkHeartbeatRefusalStopsWorkOnly(t, tc.code, tc.status, tc.want)
		})
	}
}

func checkHeartbeatRefusalStopsWorkOnly(t *testing.T, code string, status int, want error) {
	t.Helper()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {
			ok(work(lease)), ok(work(lease)), noWork(),
		},
		protocol.PathHeartbeat: {
			refusal(status, code, 0),
			ok(protocol.HeartbeatResponse{Cancel: nil, Lost: nil, RotateKey: false, Draining: false}),
		},
	})
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	calls := 0
	var stopped error

	err := l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		calls++

		if calls > 1 {
			cancel()

			return nil
		}

		ticks.fire(ctx, t, runner.HeartbeatEvery)
		<-wctx.Done()
		stopped = context.Cause(wctx)

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if calls != 2 {
		t.Fatalf("the handler ran %d times, want 2: the loop did not go on", calls)
	}

	if !errors.Is(stopped, want) {
		t.Errorf("work stopped with %v, want %v", stopped, want)
	}

	if !logs.hasAttrs(refusedMessage, map[string]string{"error.code": code}) {
		t.Errorf("no refusal record with the code %s", code)
	}

	checkNoUnknownCodeInRecords(t, logs, code)
}

func checkNoUnknownCodeInRecords(t *testing.T, logs logCapture, code string) {
	t.Helper()

	for _, r := range logs.all() {
		if strings.Contains(r.reason, unknownCodeMessage) ||
			r.attrs["error.code"] == "unknown" || r.attrs["cause.code"] == "unknown" {
			t.Errorf("the code %s was logged as unknown in %q", code, r.message)
		}
	}
}

// TestLoopEndsOnAHeartbeatAnsweredTwoHundredWithACode checks that a heartbeat
// answer of 200 with an error body ends the loop: a 200 with a code is a
// refusal.
func TestLoopEndsOnAHeartbeatAnsweredTwoHundredWithACode(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {noWork()},
		protocol.PathHeartbeat: {
			ok(protocol.Error{Code: protocol.ErrorRetired, Message: "retired", Supported: ""}),
		},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrRetired) {
		t.Errorf("Run = %v, want ErrRetired", res.run)
	}

	if !logs.hasAttrs(endedMessage, map[string]string{"error.code": protocol.ErrorRetired}) {
		t.Error("no record that the loop ends on the code retired")
	}
}

// gatedHeartbeat sends each request of srv. The first heartbeat waits until
// release is closed, so that a test can start a new claim while the heartbeat
// is in flight. The start of the second heartbeat closes second.
type gatedHeartbeat struct {
	srv     *fakeServer
	calls   atomic.Int32
	sent    chan struct{}
	release chan struct{}
	second  chan struct{}
}

// Do implements client.Transport.
func (g *gatedHeartbeat) Do(ctx context.Context, req *session.Request) (*client.Answer, error) {
	if req.Path != protocol.PathHeartbeat {
		return g.srv.Do(ctx, req)
	}

	switch g.calls.Add(1) {
	case 1:
		close(g.sent)

		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, fmt.Errorf("the gated heartbeat ends: %w", ctx.Err())
		}
	case 2:
		close(g.second)
	default:
	}

	return g.srv.Do(ctx, req)
}

// TestHeartbeatClaimLostDoesNotStopANewClaim checks that a heartbeat for the
// first claim, answered claim-lost after a second claim started, stops only
// the first claim. The second claim goes on.
func TestHeartbeatClaimLostDoesNotStopANewClaim(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease

	first := work(lease)

	second := work(lease)
	second.Operation = "op-2"

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(first), ok(second), noWork()},
		protocol.PathHeartbeat: {
			refusal(http.StatusConflict, protocol.ErrorClaimLost, 0),
			ok(protocol.HeartbeatResponse{Cancel: nil, Lost: nil, RotateKey: false, Draining: false}),
		},
	})
	gate := &gatedHeartbeat{
		srv: srv, calls: atomic.Int32{}, sent: make(chan struct{}), release: make(chan struct{}),
		second: make(chan struct{}),
	}

	logs := newLogCapture()
	cfg := testConfig(t, srv, ticks)
	cfg.Transport = gate
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var (
		calls      int
		newStopped bool
	)

	err = l.Run(ctx, func(wctx context.Context, w protocol.Work) error {
		calls++

		switch w.Operation {
		case first.Operation:
			runFirstClaim(ctx, t, ticks, gate)
		case second.Operation:
			newStopped = runSecondClaim(ctx, wctx, t, ticks, gate)

			cancel()
		default:
			t.Errorf("unexpected claim %s", w.Operation)
		}

		return nil
	})
	if err != nil {
		t.Errorf("Run = %v, want nil", err)
	}

	if calls != 2 {
		t.Errorf("the handler ran %d times, want 2", calls)
	}

	if newStopped {
		t.Error("the claim-lost answer of the first claim stopped the second claim")
	}

	if !logs.hasAttrs(refusedMessage, map[string]string{"error.code": protocol.ErrorClaimLost}) {
		t.Error("no refusal record with the code claim-lost")
	}
}

// runFirstClaim runs the first claim of the test. Its heartbeat is in flight
// when the claim ends.
func runFirstClaim(ctx context.Context, t *testing.T, ticks *fakeTicks, gate *gatedHeartbeat) {
	t.Helper()

	ticks.fire(ctx, t, runner.HeartbeatEvery)

	select {
	case <-gate.sent:
	case <-ctx.Done():
	}
}

// runSecondClaim releases the answer of the first heartbeat while the second
// claim runs, and returns true when the second claim was stopped. The second
// heartbeat starts after that answer, so the stop is seen by then.
func runSecondClaim(ctx, wctx context.Context, t *testing.T, ticks *fakeTicks, gate *gatedHeartbeat) bool {
	t.Helper()

	close(gate.release)

	ticks.fire(ctx, t, runner.HeartbeatEvery)

	select {
	case <-gate.second:
	case <-ctx.Done():
	}

	return wctx.Err() != nil
}
