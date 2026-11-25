package client_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/client"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// logRecord is one record that the loop wrote: its level, its message, its
// reason, if it has one, and the text of each attribute.
type logRecord struct {
	level   slog.Level
	message string
	reason  string
	attrs   map[string]string
}

// logCapture is a slog handler that keeps each record.
type logCapture struct {
	mu      *sync.Mutex
	records *[]logRecord
}

func newLogCapture() logCapture {
	return logCapture{mu: &sync.Mutex{}, records: &[]logRecord{}}
}

// Enabled implements slog.Handler.
func (logCapture) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements slog.Handler.
func (c logCapture) Handle(_ context.Context, r slog.Record) error {
	rec := logRecord{level: r.Level, message: r.Message, reason: "", attrs: map[string]string{}}

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "reason" {
			rec.reason = a.Value.String()
		}

		rec.attrs[a.Key] = a.Value.String()

		// A group keeps each of its attributes as group.key, with the raw text.
		if a.Value.Kind() == slog.KindGroup {
			for _, g := range a.Value.Group() {
				rec.attrs[a.Key+"."+g.Key] = g.Value.String()
			}
		}

		return true
	})

	c.mu.Lock()
	defer c.mu.Unlock()

	*c.records = append(*c.records, rec)

	return nil
}

// WithAttrs implements slog.Handler.
func (c logCapture) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup implements slog.Handler.
func (c logCapture) WithGroup(string) slog.Handler { return c }

// has reports whether a record has the level, a message that holds message
// and a reason that holds reason.
func (c logCapture) has(level slog.Level, message, reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.ContainsFunc(*c.records, func(r logRecord) bool {
		return r.level == level && strings.Contains(r.message, message) && strings.Contains(r.reason, reason)
	})
}

// hasAttrs reports whether a record has a message that holds message and
// each attribute of want with its text.
func (c logCapture) hasAttrs(message string, want map[string]string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.ContainsFunc(*c.records, func(r logRecord) bool {
		if !strings.Contains(r.message, message) {
			return false
		}

		for k, v := range want {
			if r.attrs[k] != v {
				return false
			}
		}

		return true
	})
}

// all returns a copy of the records.
func (c logCapture) all() []logRecord {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(*c.records)
}

// refusedMessage is the message of the record of a refused answer.
const refusedMessage = "the runner refused an answer of the server"

// runRefused runs a loop on srv until it ends, with a handler that must not
// run, and returns the log records and the error of Run.
func runRefused(t *testing.T, srv *fakeServer) (logCapture, error) {
	t.Helper()

	logs := newLogCapture()
	cfg := testConfig(t, srv, newFakeTicks())
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = l.Run(t.Context(), func(context.Context, protocol.Work) error {
		t.Error("the handler ran for a refused answer")

		return nil
	})

	return logs, err
}

func TestLoopStopsOnAnAnswerAboveTheLimit(t *testing.T) {
	t.Parallel()

	// Hostile case: a 200 body above the limit. The body is valid JSON work
	// with padding, so that only the size check refuses it.
	big := []byte(`{"operation":"op-1","lease":90000,"pad":"` + strings.Repeat("a", protocol.MaxBody) + `"}`)

	t.Run("poll", func(t *testing.T) {
		t.Parallel()

		logs, err := runRefused(t, newFakeServer(map[string][]answer{
			protocol.PathPoll: {rawAnswer(http.StatusOK, big)},
		}))
		if !errors.Is(err, client.ErrAnswerTooLarge) {
			t.Errorf("Run = %v, want ErrAnswerTooLarge", err)
		}

		if !logs.has(slog.LevelWarn, refusedMessage, "above the limit") {
			t.Error("no log record of the refused answer")
		}
	})

	t.Run("heartbeat", func(t *testing.T) {
		t.Parallel()

		ticks := newFakeTicks()
		srv := newFakeServer(map[string][]answer{
			protocol.PathPoll:      {noWork()},
			protocol.PathHeartbeat: {rawAnswer(http.StatusOK, big)},
		})

		l, err := client.New(testConfig(t, srv, ticks))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		go ticks.fire(t.Context(), t, runner.HeartbeatEvery)

		err = l.Run(t.Context(), func(context.Context, protocol.Work) error { return nil })
		if !errors.Is(err, client.ErrAnswerTooLarge) {
			t.Errorf("Run = %v, want ErrAnswerTooLarge", err)
		}
	})

	t.Run("renew", func(t *testing.T) {
		t.Parallel()

		lease := runner.DefaultLease
		ticks := newFakeTicks()
		srv := newFakeServer(map[string][]answer{
			protocol.PathPoll:       {ok(work(lease))},
			protocol.PathClaimRenew: {rawAnswer(http.StatusOK, big)},
		})

		l, err := client.New(testConfig(t, srv, ticks))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		go ticks.fire(t.Context(), t, runner.RenewEvery(lease))

		var stopped error

		err = l.Run(t.Context(), func(ctx context.Context, _ protocol.Work) error {
			<-ctx.Done()
			stopped = context.Cause(ctx)

			return nil
		})
		if !errors.Is(err, client.ErrAnswerTooLarge) || !errors.Is(stopped, client.ErrAnswerTooLarge) {
			t.Errorf("Run = %v, work stopped with %v; want ErrAnswerTooLarge for both", err, stopped)
		}
	})
}

// waitClock is the clock of the backoff tests. Each wait ticks at once, and
// the clock records its period. The heartbeat ticker never ticks.
type waitClock struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (c *waitClock) ticker(d time.Duration) (<-chan time.Time, func()) {
	if d == runner.HeartbeatEvery {
		return make(chan time.Time), func() {}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.waits = append(c.waits, d)

	tick := make(chan time.Time, 1)
	tick <- time.Now()

	return tick, func() {}
}

func (c *waitClock) recorded() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	return slices.Clone(c.waits)
}

// runBackoff runs a loop whose polls get script, then 204, with the clock
// and the random source random, until the server saw polls polls. It returns
// the waits of the loop.
func runBackoff(t *testing.T, script []answer, polls int, random func(int64) int64) []time.Duration {
	t.Helper()

	srv := newFakeServer(map[string][]answer{protocol.PathPoll: append(script, noWork())})
	clock := &waitClock{mu: sync.Mutex{}, waits: nil}

	cfg := testConfig(t, srv, newFakeTicks())
	cfg.Ticker = clock.ticker
	cfg.Random = random

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= polls })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	return clock.recorded()
}

func TestLoopBacksOffOnNoAnswerAndServerErrors(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		asked []int64
	)

	// The random source gives the largest value, so each wait is the upper
	// bound of its step.
	random := func(n int64) int64 {
		mu.Lock()
		defer mu.Unlock()

		asked = append(asked, n)

		return n - 1
	}

	waits := runBackoff(t, []answer{
		noAnswer(),
		rawAnswer(http.StatusServiceUnavailable, nil),
		refusal(http.StatusInternalServerError, "internal", 0),
		noAnswer(),
	}, 5, random)

	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}
	if !slices.Equal(waits, want) {
		t.Errorf("waits = %v, want %v", waits, want)
	}

	mu.Lock()
	defer mu.Unlock()

	wantAsked := []int64{1, int64(time.Second) + 1, int64(3*time.Second) + 1, int64(7*time.Second) + 1}
	if !slices.Equal(asked, wantAsked) {
		t.Errorf("random asked for %v, want %v", asked, wantAsked)
	}
}

func TestLoopBackoffStaysFromOneToThirtySeconds(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		random func(n int64) int64
		want   time.Duration
	}{
		{"smallest random part", func(int64) int64 { return 0 }, time.Second},
		{"largest random part", func(n int64) int64 { return n - 1 }, 30 * time.Second},
		// Hostile cases: a random source that leaves its range.
		{"random above its range", func(n int64) int64 { return n * 10 }, 30 * time.Second},
		{"negative random", func(int64) int64 { return -5 }, 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkBackoffBounds(t, tc.random, tc.want)
		})
	}
}

// checkBackoffBounds runs ten failed polls with the random source random. Each
// wait must be from 1 s to 30 s, and the last one want.
func checkBackoffBounds(t *testing.T, random func(int64) int64, want time.Duration) {
	t.Helper()

	// Ten failed polls: the upper bound of the last wait is 30 s.
	script := make([]answer, 0, 10)
	for range 10 {
		script = append(script, noAnswer())
	}

	waits := runBackoff(t, script, 11, random)
	if len(waits) != 10 {
		t.Fatalf("waits = %v, want 10", waits)
	}

	for _, d := range waits {
		if d < time.Second || d > 30*time.Second {
			t.Errorf("a wait of %v is not from 1s to 30s", d)
		}
	}

	if last := waits[len(waits)-1]; last != want {
		t.Errorf("last wait = %v, want %v", last, want)
	}
}

func TestLoopStopsTheWorkOnAClaimLeaseOutOfRange(t *testing.T) {
	t.Parallel()

	for name, lease := range map[string]time.Duration{
		"below the minimum": runner.MinLease - time.Second,
		"above the maximum": runner.MaxLease + time.Second,
		"zero":              0,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkClaimLeaseOutOfRange(t, lease)
		})
	}
}

// checkClaimLeaseOutOfRange runs a claim with the lease lease. The work must
// stop with ErrInvalid, with no renewal and a log record of the refusal.
func checkClaimLeaseOutOfRange(t *testing.T, lease time.Duration) {
	t.Helper()

	w := work(time.Minute)
	w.Lease = protocol.FromStd(lease)

	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {ok(w)}})
	ticks := newFakeTicks()
	logs := newLogCapture()

	cfg := testConfig(t, srv, ticks)
	cfg.Logger = slog.New(logs)

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var stopped error

	err = l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		<-wctx.Done()
		stopped = context.Cause(wctx)

		cancel()

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !errors.Is(stopped, client.ErrInvalid) {
		t.Errorf("work stopped with %v, want ErrInvalid", stopped)
	}

	// No renewal ticker with a lease of another source.
	if ticks.has(runner.RenewEvery(cfg.Lease)) || srv.count(protocol.PathClaimRenew) != 0 {
		t.Error("the loop renewed with the lease of the configuration")
	}

	if !logs.has(slog.LevelWarn, refusedMessage, "lease of the claim is out of range") {
		t.Error("no log record of the refused lease")
	}
}

func TestLoopChecksTheLengthOfTheOperation(t *testing.T) {
	t.Parallel()

	t.Run("129 characters is refused", func(t *testing.T) {
		t.Parallel()

		// Hostile case: only the length is wrong; each character is allowed.
		hostile := work(runner.DefaultLease)
		hostile.Operation = strings.Repeat("a", 129)

		logs, err := runRefused(t, newFakeServer(map[string][]answer{protocol.PathPoll: {ok(hostile)}}))
		if !errors.Is(err, client.ErrInvalid) {
			t.Errorf("Run = %v, want ErrInvalid", err)
		}

		if !logs.has(slog.LevelWarn, refusedMessage, "operation ID of the work is not valid") {
			t.Error("no log record of the refused operation")
		}
	})

	t.Run("128 characters runs", func(t *testing.T) {
		t.Parallel()

		w := work(runner.DefaultLease)
		w.Operation = strings.Repeat("a", 128)

		l, err := client.New(testConfig(t, newFakeServer(map[string][]answer{protocol.PathPoll: {ok(w)}}),
			newFakeTicks()))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		ran := false

		err = l.Run(ctx, func(context.Context, protocol.Work) error {
			ran = true

			cancel()

			return nil
		})
		if err != nil || !ran {
			t.Errorf("Run = %v, handler ran = %v; want nil and true", err, ran)
		}
	})
}

func TestLoopLogsNoWorkAndEachRefusal(t *testing.T) {
	t.Parallel()

	t.Run("204 is normal", func(t *testing.T) {
		t.Parallel()

		srv := newFakeServer(map[string][]answer{protocol.PathPoll: {noWork()}})
		logs := newLogCapture()

		cfg := testConfig(t, srv, newFakeTicks())
		cfg.Logger = slog.New(logs)

		l, err := client.New(cfg)
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		go func() {
			waitFor(ctx, func() bool { return logs.has(slog.LevelInfo, "the poll found no work", "") })
			cancel()
		}()

		if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
			t.Fatalf("Run = %v, want nil", err)
		}

		if logs.has(slog.LevelWarn, "", "") {
			t.Error("a 204 answer wrote a warning")
		}
	})

	hostile := work(runner.DefaultLease)
	hostile.Operation = "../../admin"
	tooLarge := rawAnswer(http.StatusOK, bytes.Repeat([]byte(" "), protocol.MaxBody+1))

	for _, tc := range []struct {
		name   string
		answer answer
		reason string
	}{
		{"hostile work", ok(hostile), "operation ID of the work is not valid"},
		{"undecodable answer", rawAnswer(http.StatusOK, []byte("not json")), "the answer is not work"},
		{"answer above the limit", tooLarge, "above the limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logs, err := runRefused(t, newFakeServer(map[string][]answer{protocol.PathPoll: {tc.answer}}))
			if !errors.Is(err, client.ErrInvalid) {
				t.Errorf("Run = %v, want ErrInvalid", err)
			}

			if !logs.has(slog.LevelWarn, refusedMessage, tc.reason) {
				t.Errorf("no log record with the reason %q", tc.reason)
			}
		})
	}
}

func TestRunReturnsTheDeadlineOfTheContext(t *testing.T) {
	t.Parallel()

	l, err := client.New(testConfig(t, newFakeServer(nil), newFakeTicks()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Run = %v, want an error that wraps context.DeadlineExceeded", err)
	}
}

// The tests below each cover one scenario of the runner claims spec, on the
// side of the loop.

// TestScenarioFenceMoved: the server denies the renewal of a claim whose fence
// moved with claim-lost. The loop stops the work at once and sends no more
// renewals for it.
func TestScenarioFenceMoved(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll:       {ok(work(lease)), noWork()},
		protocol.PathClaimRenew: {refusal(http.StatusConflict, protocol.ErrorClaimLost, 0)},
	})
	ticks := newFakeTicks()

	l, err := client.New(testConfig(t, srv, ticks))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go ticks.fire(ctx, t, runner.RenewEvery(lease))

	var stopped error

	err = l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		<-wctx.Done()
		stopped = context.Cause(wctx)

		// One more renew tick after the stop: no renewal follows.
		ticks.send(runner.RenewEvery(lease))
		cancel()

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	if !errors.Is(stopped, client.ErrClaimLost) {
		t.Errorf("work stopped with %v, want ErrClaimLost", stopped)
	}

	if n := srv.count(protocol.PathClaimRenew); n != 1 {
		t.Errorf("renewals = %d, want 1", n)
	}
}

// TestScenarioLeaseOfThePool: the loop renews after a third of the lease that
// the claim carries, the effective lease of the pool: 90 s when the pool sets
// none, and the lease of the pool otherwise.
func TestScenarioLeaseOfThePool(t *testing.T) {
	t.Parallel()

	for name, lease := range map[string]time.Duration{
		"default lease": runner.DefaultLease,
		"pool lease":    time.Minute,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := newFakeServer(map[string][]answer{protocol.PathPoll: {ok(work(lease))}})
			ticks := newFakeTicks()
			logs := newLogCapture()

			cfg := testConfig(t, srv, ticks)
			cfg.Logger = slog.New(logs)

			l, err := client.New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			err = l.Run(ctx, func(context.Context, protocol.Work) error {
				waitFor(ctx, func() bool { return ticks.has(runner.RenewEvery(lease)) })
				cancel()

				return nil
			})
			if err != nil {
				t.Fatalf("Run = %v, want nil", err)
			}

			if !ticks.has(runner.RenewEvery(lease)) {
				t.Errorf("no renew ticker of %v", runner.RenewEvery(lease))
			}

			differs := logs.has(slog.LevelInfo, "the lease of the claim is not the lease of the pool", "")
			if differs != (lease != cfg.Lease) {
				t.Errorf("record of another lease = %v, want %v", differs, lease != cfg.Lease)
			}
		})
	}
}

// TestScenarioBadProofOnPoll: a poll that the server answers 401 bad-proof
// ends the loop, and no work starts.
func TestScenarioBadProofOnPoll(t *testing.T) {
	t.Parallel()

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {refusal(http.StatusUnauthorized, protocol.ErrorBadProof, 0)},
	})

	l, err := client.New(testConfig(t, srv, newFakeTicks()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	err = l.Run(t.Context(), func(context.Context, protocol.Work) error {
		t.Error("work started after bad-proof")

		return nil
	})
	if !errors.Is(err, client.ErrBadProof) {
		t.Errorf("Run = %v, want ErrBadProof", err)
	}

	if n := srv.count(protocol.PathPoll); n != 1 {
		t.Errorf("polls = %d, want 1", n)
	}
}

// TestScenarioHolderRenews: the loop takes the expiry that the server sends
// in each renewal answer. A later renewal that fails keeps the work until
// that expiry, and stops it after.
func TestScenarioHolderRenews(t *testing.T) {
	t.Parallel()

	lease := runner.DefaultLease
	start := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	w := work(lease)
	w.ExpiresAt = start.Add(lease)
	moved := start.Add(2 * lease)

	srv := newFakeServer(map[string][]answer{
		protocol.PathPoll: {ok(w), noWork()},
		protocol.PathClaimRenew: {
			ok(protocol.RenewResponse{ExpiresAt: moved, Canceled: false, SessionOpen: false}),
			noAnswer(),
			noAnswer(),
		},
	})
	ticks := newFakeTicks()

	var now atomic.Int64

	now.Store(start.UnixNano())

	cfg := testConfig(t, srv, ticks)
	cfg.Now = func() time.Time { return time.Unix(0, now.Load()) }

	l, err := client.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	every := runner.RenewEvery(lease)

	err = l.Run(ctx, func(wctx context.Context, _ protocol.Work) error {
		ticks.fire(ctx, t, every)
		waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 1 })

		// After the first expiry, before the moved one: a failed renewal keeps
		// the work, so a third renewal follows.
		now.Store(start.Add(lease + time.Second).UnixNano())
		ticks.fire(ctx, t, every)
		waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 2 })
		ticks.fire(ctx, t, every)
		waitFor(ctx, func() bool { return srv.count(protocol.PathClaimRenew) >= 3 || wctx.Err() != nil })

		// After the moved expiry: a failed renewal stops the work.
		now.Store(moved.Add(time.Second).UnixNano())
		ticks.fire(ctx, t, every)
		<-wctx.Done()

		if !errors.Is(context.Cause(wctx), client.ErrClaimLost) {
			t.Errorf("work stopped with %v, want ErrClaimLost", context.Cause(wctx))
		}

		cancel()

		return nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	// A work that stopped at the second renewal sends no third one.
	if n := srv.count(protocol.PathClaimRenew); n < 3 {
		t.Errorf("renewals = %d, want 3 or more: the work stopped before the expiry that the renewal sent", n)
	}
}

// TestScenarioPollProofIsNeverReplayed: each poll carries a new proof with a
// new nonce, so the loop never sends a proof that the server already used.
func TestScenarioPollProofIsNeverReplayed(t *testing.T) {
	t.Parallel()

	srv := newFakeServer(map[string][]answer{protocol.PathPoll: {noWork()}})

	l, err := client.New(testConfig(t, srv, newFakeTicks()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	go func() {
		waitFor(ctx, func() bool { return srv.count(protocol.PathPoll) >= 3 })
		cancel()
	}()

	if err = l.Run(ctx, func(context.Context, protocol.Work) error { return nil }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}

	proofs := srv.proofsOf(protocol.PathPoll)
	seen := map[string]bool{}

	for _, p := range proofs {
		if !strings.HasPrefix(p, "v1 sub="+testRunner+" ") {
			t.Errorf("proof %q does not name the runner", p)
		}

		nonce := proofField(p, "n=")
		if nonce == "" || seen[nonce] {
			t.Errorf("proof %q repeats a nonce or has none", p)
		}

		seen[nonce] = true
	}

	if len(proofs) < 3 {
		t.Errorf("proofs = %d, want at least 3", len(proofs))
	}
}

// proofField returns the value of the field with prefix of a proof header.
func proofField(proof, prefix string) string {
	for f := range strings.FieldsSeq(proof) {
		if v, found := strings.CutPrefix(f, prefix); found {
			return v
		}
	}

	return ""
}
