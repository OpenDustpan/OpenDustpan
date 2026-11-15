package provider_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

// TestRunDoesNotWaitForAnAdapterThatIgnoresItsContext sends Run an adapter
// that blocks with no look at its context: the deadline of Run ends the
// block alone.
func TestRunDoesNotWaitForAnAdapterThatIgnoresItsContext(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	reg := registry(t, newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		<-release

		return okLogin(ctx, r)
	}))

	b := login("a")
	b.Timeout = 50 * time.Millisecond
	start := time.Now()

	got, err := reg.Run(t.Context(), []provider.Block{b}, noCall())
	if !errors.Is(err, provider.ErrUnavailable) || got.Results != nil {
		t.Fatalf("Run = %v, %v, want provider.ErrUnavailable and no result", got, err)
	}

	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("Run returned after %s, want soon after the deadline of 50ms", took)
	}
}

func TestRunTurnsAPanicIntoAnError(t *testing.T) {
	t.Parallel()

	reg := registry(t, newLogin(nil, func(context.Context, provider.Request) (provider.Result, error) {
		panic("adapter fault")
	}))

	_, err := reg.Run(t.Context(), []provider.Block{login("a")}, noCall())
	if !errors.Is(err, provider.ErrAdapter) {
		t.Errorf("Run = %v, want provider.ErrAdapter", err)
	}
}

// TestRunLimitOfTheWholeOpen runs blocks that each end in time, but not all
// of them within the limit of the open.
func TestRunLimitOfTheWholeOpen(t *testing.T) {
	t.Parallel()

	var opened atomic.Int32

	reg := registry(t, newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		opened.Add(1)

		select {
		case <-time.After(60 * time.Millisecond):
			return okLogin(ctx, r)
		case <-ctx.Done():
			return provider.Result{}, fmt.Errorf("wait: %w", ctx.Err())
		}
	}))

	blocks := []provider.Block{login("a"), login("b"), login("c"), login("d"), login("e")}

	_, err := reg.Run(
		t.Context(),
		blocks,
		provider.Call{Claims: nil, Tokens: nil, Late: ignoreLate, Limit: 150 * time.Millisecond},
	)
	if !errors.Is(err, provider.ErrUnavailable) || !strings.Contains(err.Error(), "did not end in 150ms") {
		t.Fatalf("Run = %v, want provider.ErrUnavailable for the limit of the open", err)
	}

	if n := opened.Load(); n < 2 || n > 4 {
		t.Errorf("%d blocks opened, want the open to stop at the limit", n)
	}
}

func TestRunRefusesALimitOutOfRange(t *testing.T) {
	t.Parallel()

	for _, limit := range []time.Duration{-1, provider.MaxRunTime + 1} {
		t.Run(limit.String(), func(t *testing.T) {
			t.Parallel()

			log := &calls{list: nil, mu: sync.Mutex{}}
			reg := registry(t, newLogin(log, nil))

			_, err := reg.Run(
				t.Context(),
				[]provider.Block{login("a")},
				provider.Call{Claims: nil, Tokens: nil, Late: ignoreLate, Limit: limit},
			)
			if !errors.Is(err, profile.ErrInvalid) || len(log.get()) != 0 {
				t.Errorf("Run = %v, calls %v, want profile.ErrInvalid and no call", err, log.get())
			}
		})
	}
}

// entrySource gives tokens with an audit entry, and counts its calls.
type entrySource struct {
	calls atomic.Int32
}

func (s *entrySource) Token(_ context.Context, aud string, lifetime time.Duration) (profile.Token, error) {
	n := s.calls.Add(1)

	var tok profile.Token

	tok.ID = fmt.Sprintf("jti-%d", n)
	tok.Value = fmt.Sprintf("eyJhbGciOiJSUzI1NiJ9.eyJqdGkiOiJqdGktJWQifQ.signature-number-%04d", n)
	tok.Audience = aud
	tok.ExpiresAt = clock().Add(lifetime)
	tok.Warnings = []string{"the copy check failed"}
	tok.Entry.Action = "profile:issue-token"
	tok.Entry.ObjectID = tok.ID

	return tok, nil
}

// TestRunGivesTheAuditEntryOfEachToken checks that the caller gets the
// entry of each token that a block got, on success and on failure, so that
// no token leaves without its entry, whatever the adapter does with it.
func TestRunGivesTheAuditEntryOfEachToken(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fail error
		name string
	}{
		{nil, "the block succeeds"},
		{provider.ErrRemoteDenied, "the block fails after it got the token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			src := &entrySource{calls: atomic.Int32{}}
			f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
				if _, err := r.Tokens.Token(ctx, "aud", time.Minute); err != nil {
					return provider.Result{}, fmt.Errorf("token: %w", err)
				}

				if tc.fail != nil {
					return provider.Result{}, fmt.Errorf("%w: the remote refused the token", tc.fail)
				}

				return okLogin(ctx, r)
			})
			f.spec.MintsTokens = true
			reg := registry(t, f)

			got, err := reg.Run(t.Context(), []provider.Block{login("a")}, provider.Call{
				Claims: nil, Tokens: func(string) provider.TokenSource { return src }, Late: ignoreLate, Limit: 0,
			})
			if !errors.Is(err, tc.fail) {
				t.Fatalf("Run = %v, want %v", err, tc.fail)
			}

			checkIssued(t, got.Tokens)
		})
	}
}

func checkIssued(t *testing.T, issued []provider.Issued) {
	t.Helper()

	if len(issued) != 1 {
		t.Fatalf("Tokens = %v, want one token", issued)
	}

	i := issued[0]
	if i.Block != "a" || i.ID != "jti-1" || i.Entry.Action != "profile:issue-token" || i.Entry.ObjectID != "jti-1" ||
		len(i.Warnings) != 1 {
		t.Errorf("Tokens[0] = %+v", i)
	}
}

// TestRunGivesNoTokenAfterTheBlockEnds sends a late adapter to the token
// source of its block: the source is closed, the issuer is not called, and
// no token leaves without its entry.
func TestRunGivesNoTokenAfterTheBlockEnds(t *testing.T) {
	t.Parallel()

	src := &entrySource{calls: atomic.Int32{}}
	release := make(chan struct{})
	late := make(chan error, 1)

	f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		<-release

		_, err := r.Tokens.Token(ctx, "aud", time.Minute)
		late <- err

		return provider.Result{}, err
	})
	f.spec.MintsTokens = true
	reg := registry(t, f)

	b := login("a")
	b.Timeout = 20 * time.Millisecond

	got, err := reg.Run(t.Context(), []provider.Block{b}, provider.Call{
		Claims: nil, Tokens: func(string) provider.TokenSource { return src }, Late: ignoreLate, Limit: 0,
	})
	close(release)

	lateErr := <-late

	if !errors.Is(err, provider.ErrUnavailable) || len(got.Tokens) != 0 {
		t.Fatalf("Run = %v, %v, want provider.ErrUnavailable and no token", got, err)
	}

	if !errors.Is(lateErr, provider.ErrUnavailable) || src.calls.Load() != 0 {
		t.Errorf("the late token call = %v with %d issuer calls, want provider.ErrUnavailable and none",
			lateErr, src.calls.Load())
	}
}

// cancelSource cancels the open while the issuer signs the token, as a block
// that the caller cancels during the issuance.
type cancelSource struct {
	cancel context.CancelFunc
	src    entrySource
}

func (s *cancelSource) Token(ctx context.Context, aud string, lifetime time.Duration) (profile.Token, error) {
	tok, err := s.src.Token(ctx, aud, lifetime)
	s.cancel()

	return tok, err
}

// tokenAnswer is what the adapter read from the token source.
type tokenAnswer struct {
	tok profile.Token
	err error
}

// TestTokenCanceledDuringIssuanceBeforeClose cancels the open while the
// issuer signs the token, before the recorder of the block closes. The
// adapter gets no value, and the entry of the signed token stays.
//
// The recorder can close before or after it records the token. The test
// repeats the scenario, so that a value that leaves in either order fails.
func TestTokenCanceledDuringIssuanceBeforeClose(t *testing.T) {
	t.Parallel()

	for range 20 {
		ctx, cancel := context.WithCancel(t.Context())

		src := &cancelSource{cancel: cancel, src: entrySource{calls: atomic.Int32{}}}
		answer := make(chan tokenAnswer, 1)

		f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
			tok, err := r.Tokens.Token(ctx, "aud", time.Minute)
			answer <- tokenAnswer{tok: tok, err: err}

			return provider.Result{}, err
		})
		f.spec.MintsTokens = true
		reg := registry(t, f)

		var mu sync.Mutex

		var late []provider.Issued

		out, err := reg.Run(ctx, []provider.Block{login("a")}, provider.Call{
			Claims: nil, Tokens: func(string) provider.TokenSource { return src },
			Late: func(i provider.Issued) {
				mu.Lock()
				late = append(late, i)
				mu.Unlock()
			},
			Limit: 0,
		})

		got := <-answer
		cancel()

		if got.tok.Value != "" || !errors.Is(got.err, provider.ErrUnavailable) {
			t.Fatalf("the adapter got err %v and a token value: %t, want provider.ErrUnavailable and no token",
				got.err, got.tok.Value != "")
		}

		if !errors.Is(err, provider.ErrUnavailable) {
			t.Fatalf("Run = %v, want provider.ErrUnavailable", err)
		}

		mu.Lock()
		issued := append(append([]provider.Issued(nil), out.Tokens...), late...)
		mu.Unlock()

		if len(issued) != 1 {
			t.Fatalf("the caller got %d entries, want the one entry of the signed token", len(issued))
		}

		if i := issued[0]; i.Delivered || i.Block != "a" || i.ID != "jti-1" || i.Entry.ObjectID != "jti-1" {
			t.Fatalf("the entry = %+v, want the entry of jti-1, not delivered", i)
		}
	}
}
