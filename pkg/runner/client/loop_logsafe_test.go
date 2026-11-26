package client_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// maxLoggedText is the longest attribute text that a test accepts in a log
// record. The loop cuts each text from the server to a shorter limit.
const maxLoggedText = 1024

// hostileMessage returns a text with CR, LF and other control characters, a
// forged record and more text than a record may hold.
func hostileMessage() string {
	return "first line\r\nlevel=ERROR msg=forged\x00\x1b[31m\u0085 " + strings.Repeat("a", 10_000)
}

// checkSafeRecords fails the test when a record of logs has an attribute text
// with a control character or above maxLoggedText.
func checkSafeRecords(t *testing.T, logs logCapture) {
	t.Helper()

	for _, r := range logs.all() {
		for k, v := range r.attrs {
			if len(v) > maxLoggedText {
				t.Errorf("record %q: %s has %d bytes, want at most %d", r.message, k, len(v), maxLoggedText)
			}

			if strings.ContainsFunc(v, unicode.IsControl) {
				t.Errorf("record %q: %s holds a control character: %q", r.message, k, v[:min(len(v), 80)])
			}
		}
	}
}

func TestLoopLogsAnUnknownCodeAsUnknown(t *testing.T) {
	t.Parallel()

	// Hostile case: a code of 1 MiB in a heartbeat answer.
	code := strings.Repeat("x", 1<<20)

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:      {noWork()},
		protocol.PathHeartbeat: {refusal(http.StatusBadRequest, code, 0)},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrRefused) {
		t.Errorf("Run = %v, want ErrRefused", res.run)
	}

	// The record names the length of the code of the server, not the length
	// of the text that the log cuts.
	want := map[string]string{"error.code": "unknown", "error.code_length": strconv.Itoa(1 << 20)}
	if !logs.hasAttrs(refusedMessage, want) {
		t.Error("no refusal record with the code unknown and its length")
	}

	checkSafeRecords(t, logs)
}

func TestLoopStripsControlCharactersFromLoggedMessages(t *testing.T) {
	t.Parallel()

	t.Run("message of a protocol answer", func(t *testing.T) {
		t.Parallel()

		ticks := newFakeTicks()
		// Hostile case: a 429 whose message holds CR, LF and control
		// characters, and more text than a record holds.
		srv := newFakeServer(map[string][]answer{
			protocol.PathPoll: {
				{
					status: http.StatusTooManyRequests, retryAfter: 7 * time.Second, raw: nil,
					body: protocol.Error{Code: protocol.ErrorRateLimited, Message: hostileMessage(), Supported: ""},
				},
				noWork(),
			},
		})
		l, logs := loggedLoop(t, srv, ticks)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		go func() {
			ticks.fire(ctx, t, 7*time.Second)
			waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 2 })
			cancel()
		}()

		if err := l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}

		if !logs.hasAttrs("the server limits the polls", map[string]string{"error.code": protocol.ErrorRateLimited}) {
			t.Error("no record of the rate limit with its code")
		}

		checkSafeRecords(t, logs)
	})

	t.Run("text of a transport error", func(t *testing.T) {
		t.Parallel()

		logs := newLogCapture()
		tr := &errorTransport{err: fmt.Errorf("dial %s: %w", hostileMessage(), errReader)}

		cfg := testConfig(t, newFakeServer(nil), newFakeTicks())
		cfg.Transport = tr
		cfg.Ticker = (&waitClock{mu: sync.Mutex{}, waits: nil}).ticker
		cfg.Random = func(int64) int64 { return 0 }
		cfg.Logger = slog.New(logs)

		l, err := client.New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		go func() {
			waitFor(ctx, func() bool { return logs.has(slog.LevelWarn, "the poll failed", "") })
			cancel()
		}()

		if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}

		checkSafeRecords(t, logs)
	})
}

func TestRunReturnsASanitizedServerMessage(t *testing.T) {
	t.Parallel()

	// Hostile case: a message with a CR, an LF, a hidden rune and more text
	// than a log record may hold.
	hostile := "ab\r\ncd\u0085ef" + strings.Repeat("x", 10_000)
	want := "abcdef" + strings.Repeat("x", 250)

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {{
			status:     http.StatusBadRequest,
			body:       protocol.Error{Code: protocol.ErrorBadProof, Message: hostile, Supported: ""},
			retryAfter: 0,
			raw:        nil,
		}},
	})

	l, _ := loggedLoop(t, srv, newFakeTicks())

	err := l.Run(t.Context(), func(context.Context, protocol.Work) error {
		t.Error("the handler ran for a refused answer")

		return nil
	})
	if err == nil {
		t.Fatal("Run = nil, want the error of the refused answer")
	}

	ce, found := errors.AsType[*client.Error](err)
	if !found {
		t.Fatalf("Run = %v, want a *client.Error", err)
	}

	if got := ce.Message; got != want {
		t.Errorf("the returned error message = %q, want %q", got, want)
	}

	if got := ce.Code; got != protocol.ErrorBadProof {
		t.Errorf("the returned error code = %q, want %q", got, protocol.ErrorBadProof)
	}
}

func TestErrorStringHidesInvalidCode(t *testing.T) {
	t.Parallel()

	// A code that is not a protocol code must not appear in Error(). The
	// record logs code=unknown with the length; Error() follows the same rule.
	err := &client.Error{Status: http.StatusTooManyRequests, Code: "rate-limited\r", Message: "refused", RetryAfter: 0}
	if got, want := err.Error(), "the server answered 429 unknown: refused"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// errorTransport fails each call with err.
type errorTransport struct {
	err error
}

// Do implements client.Transport.
func (e *errorTransport) Do(context.Context, *session.Request) (*client.Answer, error) {
	return nil, e.err
}

// heartbeatStops runs one claim, sends one heartbeat with the answer hr, and
// checks that the work stops with want.
func heartbeatStops(t *testing.T, hr protocol.HeartbeatResponse, want error) {
	t.Helper()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:      {ok(work(runner.DefaultLease)), noWork()},
		protocol.PathHeartbeat: {ok(hr)},
	})
	l, _ := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var stopped error

	err := l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		ticks.fire(ctx, t, runner.HeartbeatEvery)
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

func TestLoopStopsTheWorkOnAHeartbeatWithLost(t *testing.T) {
	t.Parallel()

	heartbeatStops(t, protocol.HeartbeatResponse{
		Cancel: nil, Lost: []string{testOp}, RotateKey: false, Draining: false,
	}, client.ErrClaimLost)
}

func TestLoopStopsTheWorkOnAHeartbeatWithCancel(t *testing.T) {
	t.Parallel()

	heartbeatStops(t, protocol.HeartbeatResponse{
		Cancel: []string{testOp}, Lost: nil, RotateKey: false, Draining: false,
	}, client.ErrCanceled)
}

// TestLoopRefusesACodeThatIsNotExactlyAProtocolCode checks that a code with a
// control or format character, next to a protocol code, is refused on each
// call: the loop matches the code as the server sent it.
func TestLoopRefusesACodeThatIsNotExactlyAProtocolCode(t *testing.T) {
	t.Parallel()

	for _, code := range hostileCodes() {
		t.Run("poll "+strconv.Quote(code), func(t *testing.T) {
			t.Parallel()

			checkHostilePoll(t, code)
		})

		t.Run("renew "+strconv.Quote(code), func(t *testing.T) {
			t.Parallel()

			checkHostileRenew(t, code)
		})

		t.Run("heartbeat "+strconv.Quote(code), func(t *testing.T) {
			t.Parallel()

			checkHostileHeartbeat(t, code)
		})
	}
}

// hostileCodes returns codes that are each a protocol code with a control or
// format character added. The loop refuses each of them as an unknown code.
func hostileCodes() []string {
	return []string{"rate-limited\r", "bad-proof" + string(rune(0x200b)), "claim-lost\n"}
}

// checkHostilePoll checks that a poll answered with code is refused as a code
// that the loop does not know.
func checkHostilePoll(t *testing.T, code string) {
	t.Helper()

	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {refusal(http.StatusBadRequest, code, 0)}})

	logs, err := runRefused(t, srv)
	if !errors.Is(err, client.ErrRefused) {
		t.Errorf("Run = %v, want ErrRefused", err)
	}

	if !logs.hasAttrs(refusedMessage, map[string]string{"error.code": "unknown"}) {
		t.Error("no refusal record with the code unknown")
	}
}

// checkHostileRenew checks that a renewal answered with code stops the work as
// a code that the loop does not know.
func checkHostileRenew(t *testing.T, code string) {
	t.Helper()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:       {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {refusal(http.StatusBadRequest, code, 0)},
	})
	l, logs := loggedLoop(t, srv, ticks)

	checkWorkStops(t, l, ticks, client.ErrRefused)

	if !logs.hasAttrs(refusedMessage, map[string]string{"error.code": "unknown"}) {
		t.Error("no refusal record with the code unknown")
	}
}

// checkHostileHeartbeat checks that a heartbeat answered with code ends the
// loop as a code that the loop does not know.
func checkHostileHeartbeat(t *testing.T, code string) {
	t.Helper()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:      {noWork()},
		protocol.PathHeartbeat: {refusal(http.StatusBadRequest, code, 0)},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrRefused) {
		t.Errorf("Run = %v, want ErrRefused", res.run)
	}

	if !logs.hasAttrs(refusedMessage, map[string]string{"error.code": "unknown"}) {
		t.Error("no refusal record with the code unknown")
	}
}

type hostileStatusCase struct {
	name      string
	status    int
	code      string
	wantSent  error
	wantRun   error
	wantStop  error
	wantWait  bool
	wantRetry bool
	logCode   string
	logMsg    string
}

func TestLoopHandlesHostileStatusAndCode(t *testing.T) {
	t.Parallel()

	cases := []hostileStatusCase{
		{
			name: "429 with CR in code", status: http.StatusTooManyRequests, code: "rate-limited\r",
			wantSent: client.ErrRateLimited, wantRun: nil, wantStop: nil, wantWait: true, wantRetry: true,
			logCode: "unknown", logMsg: "",
		},
		{
			name: "429 with unknown code", status: http.StatusTooManyRequests, code: "not-a-code",
			wantSent: client.ErrRateLimited, wantRun: nil, wantStop: nil, wantWait: true, wantRetry: true,
			logCode: "unknown", logMsg: "",
		},
		{
			name: "503 with CRLF in code", status: http.StatusServiceUnavailable, code: "claim-lost\n",
			wantSent: client.ErrUnavailable, wantRun: nil, wantStop: nil, wantWait: false, wantRetry: true,
			logCode: "unknown", logMsg: "",
		},
		{
			name: "503 with unknown code", status: http.StatusServiceUnavailable, code: "not-a-code",
			wantSent: client.ErrUnavailable, wantRun: nil, wantStop: nil, wantWait: false, wantRetry: true,
			logCode: "unknown", logMsg: "",
		},
		{
			name: "503 with retired", status: http.StatusServiceUnavailable, code: protocol.ErrorRetired,
			wantSent: client.ErrUnavailable, wantRun: nil, wantStop: nil, wantWait: false, wantRetry: true,
			logCode: protocol.ErrorRetired, logMsg: "",
		},
		{
			name: "503 with bad-proof", status: http.StatusServiceUnavailable, code: protocol.ErrorBadProof,
			wantSent: client.ErrUnavailable, wantRun: nil, wantStop: nil, wantWait: false, wantRetry: true,
			logCode: protocol.ErrorBadProof, logMsg: "",
		},
		{
			name: "503 with claim-lost", status: http.StatusServiceUnavailable, code: protocol.ErrorClaimLost,
			wantSent: client.ErrUnavailable, wantRun: nil, wantStop: nil, wantWait: false, wantRetry: true,
			logCode: protocol.ErrorClaimLost, logMsg: "",
		},
	}

	for _, tc := range cases {
		t.Run("poll/"+tc.name, func(t *testing.T) {
			t.Parallel()
			checkHostileStatusPoll(t, tc)
		})
		t.Run("renew/"+tc.name, func(t *testing.T) {
			t.Parallel()
			checkHostileStatusRenew(t, tc)
		})
		t.Run("heartbeat/"+tc.name, func(t *testing.T) {
			t.Parallel()
			checkHostileStatusHeartbeat(t, tc)
		})
	}
}

func hostileStatusAnswer(tc hostileStatusCase) answer {
	if tc.wantWait {
		return refusal(tc.status, tc.code, 7*time.Second)
	}

	return refusal(tc.status, tc.code, 0)
}

func checkHostileStatusPoll(t *testing.T, tc hostileStatusCase) {
	t.Helper()

	a := hostileStatusAnswer(tc)
	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {a, noWork()}})
	ticks := newFakeTicks()
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go func() {
		if tc.wantWait {
			ticks.fire(ctx, t, 7*time.Second)
		} else {
			ticks.fire(ctx, t, time.Second)
		}
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 2 })
		cancel()
	}()

	err := l.Run(ctx, func(context.Context, protocol.Work) error { return nil })
	if !errors.Is(err, tc.wantRun) {
		t.Fatalf("Run = %v, want %v", err, tc.wantRun)
	}

	if tc.wantWait && !ticks.has(7*time.Second) {
		t.Error("no wait of the Retry-After 7s")
	}
	if !tc.wantWait && !ticks.has(time.Second) {
		t.Error("no backoff wait")
	}

	checkHostileStatusLog(t, tc, logs, "poll")
}

func checkHostileStatusRenew(t *testing.T, tc hostileStatusCase) {
	t.Helper()

	lease := runner.DefaultLease
	a := hostileStatusAnswer(tc)
	second := ok(protocol.RenewResponse{ExpiresAt: testNow().Add(lease), Canceled: false, SessionOpen: false})
	if tc.wantStop != nil || !tc.wantRetry {
		second = noWork()
	}
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:       {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {a, second},
	})
	ticks := newFakeTicks()
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if tc.wantRetry {
		go fireHostileRenewTicksRetry(ctx, t, cancel, ticks, srv)
	} else {
		go fireHostileRenewTicksOnce(ctx, t, ticks, srv)
	}

	stopped := runHostileRenewLoop(ctx, t, l, tc)
	checkHostileRenewResult(t, tc, srv, logs, stopped)
}

func fireHostileRenewTicksOnce(ctx context.Context, t *testing.T, ticks *fakeTicks, srv *fakeServer) {
	t.Helper()

	ticks.fire(ctx, t, runner.RenewEvery(runner.DefaultLease))
	waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 1 })
}

func fireHostileRenewTicksRetry(
	ctx context.Context,
	t *testing.T,
	cancel context.CancelFunc,
	ticks *fakeTicks,
	srv *fakeServer,
) {
	t.Helper()

	ticks.fire(ctx, t, runner.RenewEvery(runner.DefaultLease))
	waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 1 })
	ticks.fire(ctx, t, runner.RenewEvery(runner.DefaultLease))
	waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 2 })
	cancel()
}

func runHostileRenewLoop(ctx context.Context, t *testing.T, l *client.Loop, tc hostileStatusCase) error {
	t.Helper()

	var stopped error

	err := l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		<-wctx.Done()
		stopped = context.Cause(wctx)

		return nil
	})
	if !errors.Is(err, tc.wantRun) {
		t.Fatalf("Run = %v, want %v", err, tc.wantRun)
	}

	if stopped == nil {
		return nil
	}
	return fmt.Errorf("%w", stopped)
}

func checkHostileRenewResult(t *testing.T, tc hostileStatusCase, srv *fakeServer, logs logCapture, stopped error) {
	t.Helper()

	if tc.wantStop != nil {
		if !errors.Is(stopped, tc.wantStop) {
			t.Fatalf("work stopped with %v, want %v", stopped, tc.wantStop)
		}
	} else if tc.wantRetry && stopped != nil && !errors.Is(stopped, context.Canceled) && !errors.Is(stopped, context.DeadlineExceeded) {
		t.Fatalf("work stopped with %v, want no stop", stopped)
	}

	if tc.wantRetry && srv.count(protocol.PathClaimRenew) < 2 {
		t.Fatalf("renewals = %d, want >= 2", srv.count(protocol.PathClaimRenew))
	}

	checkHostileStatusLog(t, tc, logs, "renew")
}

func checkHostileStatusHeartbeat(t *testing.T, tc hostileStatusCase) {
	t.Helper()

	a := hostileStatusAnswer(tc)
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {noWork()},
		protocol.PathHeartbeat: {
			a,
			ok(protocol.HeartbeatResponse{Cancel: nil, Lost: nil, RotateKey: false, Draining: false}),
		},
	})
	ticks := newFakeTicks()
	l, logs := loggedLoop(t, srv, ticks)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go func() {
		ticks.fire(ctx, t, runner.HeartbeatEvery)
		if tc.wantRetry {
			waitFor(ctx, func() bool { return srv.count(protocol.PathHeartbeat) >= 1 })
			ticks.fire(ctx, t, runner.HeartbeatEvery)
			waitFor(ctx, func() bool { return srv.count(protocol.PathHeartbeat) >= 2 })
			cancel()

			return
		}
		waitFor(ctx, func() bool { return logs.has(slog.LevelWarn, "", "") })
		cancel()
	}()

	err := l.Run(ctx, func(context.Context, protocol.Work) error { return nil })
	if !errors.Is(err, tc.wantRun) {
		t.Fatalf("Run = %v, want %v", err, tc.wantRun)
	}

	if tc.wantRetry && srv.count(protocol.PathHeartbeat) < 2 {
		t.Fatalf("heartbeats = %d, want >= 2", srv.count(protocol.PathHeartbeat))
	}

	checkHostileStatusLog(t, tc, logs, "heartbeat")
}

func checkHostileStatusLog(t *testing.T, tc hostileStatusCase, logs logCapture, path string) {
	t.Helper()

	wantCode := tc.logCode
	if wantCode == "" {
		wantCode = "unknown"
	}

	msg := tc.logMsg
	if msg == "" {
		msg = hostileStatusLogMessage(path, tc.status)
	}

	attrs := map[string]string{"error.code": wantCode}
	if !logs.hasAttrs(msg, attrs) {
		t.Errorf("no %q record with code=%s; records: %+v", msg, wantCode, logs.all())
	}

	if isProtocolCode(tc.code) {
		return
	}
	for _, r := range logs.all() {
		if v, ok := r.attrs["error.code"]; ok && v == tc.code {
			t.Errorf("record %q logged raw hostile code in error.code", r.message)
		}
	}
}

func hostileStatusLogMessage(path string, status int) string {
	if path == "poll" && status == http.StatusTooManyRequests {
		return "the server limits the polls"
	}
	if path == "renew" {
		return "the renewal failed"
	}
	if path == "heartbeat" {
		return "the heartbeat failed"
	}
	return "the poll failed"
}

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
