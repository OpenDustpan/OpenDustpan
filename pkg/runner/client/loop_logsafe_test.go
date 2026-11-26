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
