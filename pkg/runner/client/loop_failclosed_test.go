package client_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// unknownCodeMessage is the reason of the record of a code that the loop does
// not know.
const unknownCodeMessage = "the code of the answer is not known"

// unknownCodes are the answers with a code that the loop does not map: a
// code that the protocol does not have, and a 4xx with no code.
func unknownCodes() map[string]answer {
	return map[string]answer{
		"not-a-code":   refusal(http.StatusBadRequest, "not-a-code", 0),
		"4xx, no code": rawAnswer(http.StatusTeapot, nil),
	}
}

// loggedLoop returns a loop on srv with the fake ticker and a log capture.
func loggedLoop(t *testing.T, srv *fakeServer, ticks *fakeTicks) (*client.Loop, logCapture) {
	t.Helper()

	logs := newLogCapture()
	cfg := testConfig(t, srv, ticks)
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return l, logs
}

// checkWorkStops runs l, fires one renewal of lease, and checks that the
// cause that stopped the work matches want. The caller cancel then ends Run
// with nil.
func checkWorkStops(t *testing.T, l *client.Loop, ticks *fakeTicks, lease time.Duration, want error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go ticks.fire(ctx, t, runner.RenewEvery(lease))

	var stopped error

	err := l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		<-wctx.Done()
		stopped = context.Cause(wctx)

		cancel()

		return nil
	})
	if err != nil {
		t.Errorf("Run = %v, want nil", err)
	}

	if !errors.Is(stopped, want) {
		t.Errorf("work stopped with %v, want %v", stopped, want)
	}
}

func TestLoopRefusesWorkWithNoExpiry(t *testing.T) {
	t.Parallel()

	// Hostile case: work with no expiry.
	w := work(runner.DefaultLease)
	w.ExpiresAt = time.Time{}

	logs, err := runRefused(t, newFakeServer(map[string][]answer{protocol.PathPoll: {ok(w)}}))
	if !errors.Is(err, client.ErrInvalid) {
		t.Errorf("Run = %v, want ErrInvalid", err)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, "the work has no expiry") {
		t.Error("no log record of the work with no expiry")
	}
}

func TestLoopStopsTheWorkOnARenewalCanceled(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	// Hostile case: a renewal that cancels the work.
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {
			ok(protocol.RenewResponse{ExpiresAt: testNow().Add(lease), Canceled: true, SessionOpen: false}),
		},
	})
	l, _ := loggedLoop(t, srv, ticks)

	checkWorkStops(t, l, ticks, lease, client.ErrCanceled)
}

func TestLoopStopsTheWorkOnARenewalWithNoExpiry(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	// Hostile case: a renewal answer with no expiry.
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {ok(protocol.RenewResponse{
			ExpiresAt: time.Time{}, Canceled: false, SessionOpen: false,
		})},
	})
	l, logs := loggedLoop(t, srv, ticks)

	checkWorkStops(t, l, ticks, lease, client.ErrInvalid)

	if !logs.has(slog.LevelWarn, refusedMessage, "the renewal has no expiry") {
		t.Error("no log record of the renewal with no expiry")
	}
}

func TestLoopFailsClosedOnAnUnknownCode(t *testing.T) {
	t.Parallel()

	for name, a := range unknownCodes() {
		t.Run("renew "+name, func(t *testing.T) {
			t.Parallel()

			lease := runner.DefaultLease
			ticks := newFakeTicks()
			srv := newFakeServer(map[string][]answer{
				protocol.PathPoll:       {ok(work(lease)), noWork()},
				protocol.PathClaimRenew: {a},
			})
			l, logs := loggedLoop(t, srv, ticks)

			checkWorkStops(t, l, ticks, lease, client.ErrRefused)

			if !logs.has(slog.LevelWarn, refusedMessage, unknownCodeMessage) {
				t.Error("no log record of the unknown code")
			}
		})

		t.Run("heartbeat "+name, func(t *testing.T) {
			t.Parallel()

			ticks := newFakeTicks()
			srv := newFakeServer(map[string][]answer{
				protocol.PathPoll:      {noWork()},
				protocol.PathHeartbeat: {a},
			})
			l, logs := loggedLoop(t, srv, ticks)

			go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

			err := l.Run(t.Context(), func(context.Context, protocol.Work) error { return nil })
			if !errors.Is(err, client.ErrRefused) {
				t.Errorf("Run = %v, want ErrRefused", err)
			}

			if !logs.has(slog.LevelWarn, refusedMessage, unknownCodeMessage) {
				t.Error("no log record of the unknown code")
			}
		})
	}
}

func TestLoopRefusesANon200Success(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		status int
	}{
		{"202 with a JSON body", http.StatusAccepted},
		{"302", http.StatusFound},
		{"299", 299},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := answer{status: tc.status, body: work(runner.DefaultLease), retryAfter: 0, raw: nil}

			_, err := runRefused(t, newFakeServer(map[string][]answer{protocol.PathPoll: {a}}))
			if err == nil {
				t.Error("Run = nil, want an error: only a 200 and a 204 are a success")
			}

			_, found := errors.AsType[*client.Error](err)
			if !found {
				t.Errorf("Run = %v, want a *client.Error", err)
			}
		})
	}
}

// endless is a body stream that never ends. It counts the bytes that the
// reader takes.
type endless struct {
	read atomic.Int64
}

// Read implements io.Reader.
func (e *endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}

	e.read.Add(int64(len(p)))

	return len(p), nil
}

// Close implements io.Closer.
func (*endless) Close() error { return nil }

// bodyTransport answers each poll with 200 and the body of newBody.
type bodyTransport struct {
	newBody func() io.ReadCloser
}

// Do implements client.Transport.
func (b bodyTransport) Do(context.Context, *session.Request) (*client.Answer, error) {
	return &client.Answer{Status: http.StatusOK, RetryAfter: 0, Body: b.newBody()}, nil
}

func TestLoopBoundsTheBodyOfAnyTransport(t *testing.T) {
	t.Parallel()

	t.Run("a stream with no end", func(t *testing.T) {
		t.Parallel()

		stream := &endless{read: atomic.Int64{}}
		err := runBodyTransport(t, bodyTransport{newBody: func() io.ReadCloser { return stream }})

		if !errors.Is(err, client.ErrAnswerTooLarge) {
			t.Errorf("Run = %v, want ErrAnswerTooLarge", err)
		}

		if n := stream.read.Load(); n > protocol.MaxBody+1 {
			t.Errorf("the loop read %d bytes, want at most %d", n, protocol.MaxBody+1)
		}
	})

	t.Run("a transport that read a very large body with no limit", func(t *testing.T) {
		t.Parallel()

		// Hostile case: the transport read 64 MiB before it gave the answer.
		large := bytes.Repeat([]byte(" "), 64<<20)
		err := runBodyTransport(t, bodyTransport{newBody: func() io.ReadCloser {
			return io.NopCloser(bytes.NewReader(large))
		}})

		if !errors.Is(err, client.ErrAnswerTooLarge) {
			t.Errorf("Run = %v, want ErrAnswerTooLarge", err)
		}
	})
}

// runBodyTransport runs a loop with the transport tr until it ends.
func runBodyTransport(t *testing.T, tr client.Transport) error {
	t.Helper()

	cfg := testConfig(t, newFakeServer(nil), newFakeTicks())
	cfg.Transport = tr

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return l.Run(t.Context(), func(context.Context, protocol.Work) error {
		t.Error("the handler ran for an answer above the limit")

		return nil
	})
}

func TestLoopEndsOnADrainAfterPollsWithNoWork(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	// The polls get 204 with no end; the heartbeat answer drains the runner.
	srv := newFakeServer(map[string][]answer{
		protocol.PathHeartbeat: {ok(protocol.HeartbeatResponse{
			Cancel: nil, Lost: nil, RotateKey: false, Draining: true,
		})},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if err := l.Run(t.Context(), func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !logs.has(slog.LevelInfo, "the runner drains", "") {
		t.Error("no log record of the drain")
	}
}

func TestLoopRecordsTheEndOfRenewalsOnAnOpenSession(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {ok(protocol.RenewResponse{
			ExpiresAt: testNow().Add(lease), Canceled: false, SessionOpen: true,
		})},
	})
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go ticks.fire(ctx, t, runner.RenewEvery(lease))

	want := map[string]string{"operation": testOp, "reason": "the launcher opened the job session"}

	err := l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		waitFor(ctx, func() bool { return logs.hasAttrs("the runner stops the renewals", want) })

		// The open session ends the renewals, not the work.
		if wctx.Err() != nil {
			t.Errorf("the work stopped with %v", context.Cause(wctx))
		}

		cancel()

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !logs.hasAttrs("the runner stops the renewals", want) {
		t.Error("no info record with the operation and the reason")
	}

	if n := srv.count(protocol.PathClaimRenew); n != 1 {
		t.Errorf("renewals = %d, want 1", n)
	}
}

// blockingTransport holds each call until ctx ends, then returns its error,
// as a transport does when the caller cancels a call.
type blockingTransport struct {
	calls atomic.Int64
}

// Do implements client.Transport.
func (b *blockingTransport) Do(ctx context.Context, _ *session.Request) (*client.Answer, error) {
	b.calls.Add(1)
	<-ctx.Done()

	return nil, fmt.Errorf("send the request: %w", ctx.Err())
}

func TestLoopCancelDuringAPollNeitherWarnsNorBacksOff(t *testing.T) {
	t.Parallel()

	tr := &blockingTransport{calls: atomic.Int64{}}
	logs := newLogCapture()

	var (
		mu     sync.Mutex
		random int
	)

	cfg := testConfig(t, newFakeServer(nil), newFakeTicks())
	cfg.Transport = tr
	cfg.Logger = slog.New(logs)
	cfg.Random = func(int64) int64 {
		mu.Lock()
		defer mu.Unlock()

		random++

		return 0
	}

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		waitFor(ctx, func() bool { return tr.calls.Load() > 0 })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if logs.has(slog.LevelWarn, "", "") {
		t.Error("a poll cut short by the caller wrote a warning")
	}

	mu.Lock()
	defer mu.Unlock()

	if random != 0 {
		t.Errorf("the loop took %d backoffs, want 0", random)
	}
}

func TestLoopCapsARetryAfterOfOneHour(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {
			refusal(http.StatusTooManyRequests, protocol.ErrorRateLimited, time.Hour),
			noWork(),
		},
	})
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		ticks.fire(ctx, t, runner.MinLease)
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 2 })
		cancel()
	}()

	if err := l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !ticks.has(runner.MinLease) || ticks.has(time.Hour) {
		t.Errorf("the wait is not the cap %v", runner.MinLease)
	}

	want := map[string]string{"asked": time.Hour.String(), "cap": runner.MinLease.String()}
	if !logs.hasAttrs("above the cap", want) {
		t.Error("no log record with the asked wait and the cap")
	}
}
