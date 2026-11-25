package client_test

import (
	"bytes"
	"context"
	"errors"
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

// errReader is the error of the reader and the stream that fail.
var errReader = errors.New("the reader failed")

// failingReader is a random source that always fails.
type failingReader struct{}

// Read implements io.Reader.
func (failingReader) Read([]byte) (int, error) { return 0, errReader }

func TestCryptoRandomTakesTheUpperBoundWhenTheReaderFails(t *testing.T) {
	t.Parallel()

	logs := newLogCapture()
	random := client.CryptoRandom(failingReader{}, slog.New(logs))

	const n = 1000

	if got := random(n); got != n-1 {
		t.Errorf("random(%d) = %d, want the upper bound %d", n, got, n-1)
	}

	if !logs.hasAttrs("the random source failed", map[string]string{"error.text": errReader.Error()}) {
		t.Error("no log record that names the error of the reader")
	}

	// A reader that works gives a number from 0 to n-1 and no record.
	good := newLogCapture()
	if got := client.CryptoRandom(bytes.NewReader(make([]byte, 64)), slog.New(good))(n); got < 0 || got >= n {
		t.Errorf("random(%d) = %d, want 0 to %d", n, got, n-1)
	}

	if good.has(slog.LevelWarn, "", "") {
		t.Error("a reader that works wrote a warning")
	}
}

// runResult is the end of a bounded run: the error of Run and the cause that
// stopped the work, if work ran.
type runResult struct {
	run     error
	stopped error
}

// boundedRun runs l for at most 5 s with a handler that waits for the end of
// its work.
func boundedRun(t *testing.T, l *client.Loop) runResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var res runResult

	res.run = l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		<-wctx.Done()
		res.stopped = context.Cause(wctx)

		return nil
	})

	return res
}

func TestLoopRefusesAnUndecodableHeartbeatAnswer(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:      {noWork()},
		protocol.PathHeartbeat: {rawAnswer(http.StatusOK, []byte("not json"))},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

	if res := boundedRun(t, l); !errors.Is(res.run, client.ErrInvalid) {
		t.Errorf("Run = %v, want ErrInvalid", res.run)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, "the answer is not a heartbeat answer") {
		t.Error("no refusal record of the heartbeat answer")
	}
}

func TestLoopStopsTheWorkOnAnUndecodableRenewalAnswer(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	ticks := newFakeTicks()
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:       {ok(work(lease))},
		protocol.PathClaimRenew: {rawAnswer(http.StatusOK, []byte("not json"))},
	})
	l, logs := loggedLoop(t, srv, ticks)

	go ticks.fire(t.Context(), t, runner.RenewEvery(lease))

	if res := boundedRun(t, l); !errors.Is(res.stopped, client.ErrInvalid) {
		t.Errorf("work stopped with %v, want ErrInvalid", res.stopped)
	}

	if !logs.has(slog.LevelWarn, refusedMessage, "the answer is not a renewal") {
		t.Error("no refusal record of the renewal answer")
	}
}

// nilTransport returns no answer and no error: a broken transport.
type nilTransport struct {
	answer *client.Answer
}

// Do implements client.Transport.
func (n nilTransport) Do(context.Context, *session.Request) (*client.Answer, error) {
	return n.answer, nil
}

func TestLoopRefusesANilAnswerWithNoError(t *testing.T) {
	t.Parallel()

	if err := runBodyTransport(t, nilTransport{answer: nil}); !errors.Is(err, client.ErrInvalid) {
		t.Errorf("Run = %v, want ErrInvalid", err)
	}
}

// failingStream is an answer body whose read or close fails.
type failingStream struct {
	readFails  bool
	closeFails bool
}

// Read implements io.Reader.
func (f failingStream) Read([]byte) (int, error) {
	if f.readFails {
		return 0, errReader
	}

	return 0, io.EOF
}

// Close implements io.Closer.
func (f failingStream) Close() error {
	if f.closeFails {
		return errReader
	}

	return nil
}

// streamTransport answers the first poll with 200 and the body stream, and
// each later call with 204.
type streamTransport struct {
	stream failingStream
	calls  atomic.Int64
}

// Do implements client.Transport.
func (s *streamTransport) Do(context.Context, *session.Request) (*client.Answer, error) {
	if s.calls.Add(1) == 1 {
		return &client.Answer{Status: http.StatusOK, RetryAfter: 0, Body: s.stream}, nil
	}

	return &client.Answer{Status: http.StatusNoContent, RetryAfter: 0, Body: nil}, nil
}

func TestLoopMapsAFailingStreamToUnavailable(t *testing.T) {
	t.Parallel()

	for name, stream := range map[string]failingStream{
		"read error":  {readFails: true, closeFails: false},
		"close error": {readFails: false, closeFails: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkFailingStream(t, stream)
		})
	}
}

// checkFailingStream runs a loop whose first poll answer is stream. The loop
// must take the backoff of a call with no answer and poll again.
func checkFailingStream(t *testing.T, stream failingStream) {
	t.Helper()

	tr := &streamTransport{stream: stream, calls: atomic.Int64{}}
	clock := &waitClock{mu: sync.Mutex{}, waits: nil}
	logs := newLogCapture()

	cfg := testConfig(t, newFakeServer(nil), newFakeTicks())
	cfg.Transport = tr
	cfg.Ticker = clock.ticker
	cfg.Logger = slog.New(logs)
	cfg.Random = func(int64) int64 { return 0 }

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go func() {
		waitFor(ctx, func() bool { return tr.calls.Load() >= 2 })
		cancel()
	}()

	err = l.Run(ctx, func(context.Context, protocol.Work) error {
		t.Error("the handler ran for a body that failed")

		return nil
	})
	if err != nil {
		t.Errorf("Run = %v, want nil after the caller cancel", err)
	}

	// A call with no answer takes the backoff: one wait of 1 s.
	if waits := clock.recorded(); len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("waits = %v, want one backoff of 1s", waits)
	}

	if !logs.has(slog.LevelWarn, "the poll failed", "") {
		t.Error("no warning of the failed poll")
	}
}

func TestLoopMapsA429WithNoBodyToRateLimited(t *testing.T) {
	t.Parallel()

	ticks := newFakeTicks()
	// A proxy answers 429 with a Retry-After and no protocol body.
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {
			{status: http.StatusTooManyRequests, body: nil, retryAfter: 7 * time.Second, raw: nil},
			noWork(),
		},
	})

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	go func() {
		ticks.fire(ctx, t, 7*time.Second)
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 2 })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil: the loop waits for a 429 with no body", err)
	}

	if !ticks.has(7 * time.Second) {
		t.Error("no wait of the Retry-After 7s")
	}
}
