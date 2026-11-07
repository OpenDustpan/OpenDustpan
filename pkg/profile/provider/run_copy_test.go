package provider_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

// slowSource signs a token only after the test releases it, as an issuer
// that answers after the end of the block.
type slowSource struct {
	release chan struct{}
	src     entrySource
}

func (s *slowSource) Token(ctx context.Context, aud string, lifetime time.Duration) (profile.Token, error) {
	<-s.release

	return s.src.Token(ctx, aud, lifetime)
}

// TestRunGivesALateTokenToTheCaller checks that a token that the issuer
// signs after the end of its block still reaches the caller with its entry,
// marked as not delivered, and that its value never reaches the adapter.
func TestRunGivesALateTokenToTheCaller(t *testing.T) {
	t.Parallel()

	src := &slowSource{release: make(chan struct{}), src: entrySource{calls: atomic.Int32{}}}
	late := make(chan provider.Issued, 1)
	got := make(chan error, 1)

	f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		_, err := r.Tokens.Token(ctx, "aud", time.Minute)
		got <- err

		return provider.Result{}, err
	})
	f.spec.MintsTokens = true
	reg := registry(t, f)

	b := login("a")
	b.Timeout = 20 * time.Millisecond

	out, err := reg.Run(t.Context(), []provider.Block{b}, provider.Call{
		Claims: nil, Tokens: func(string) provider.TokenSource { return src },
		Late:  func(i provider.Issued) { late <- i },
		Limit: 0,
	})
	close(src.release)

	adapterErr := <-got
	issued := <-late

	if !errors.Is(err, provider.ErrUnavailable) || len(out.Tokens) != 0 {
		t.Fatalf("Run = %v, %v, want provider.ErrUnavailable and no token in the outcome", out, err)
	}

	if n := src.src.calls.Load(); n != 1 {
		t.Fatalf("the issuer signed %d tokens, want 1", n)
	}

	if issued.Delivered || issued.ID != "jti-1" || issued.Entry.ObjectID != "jti-1" || issued.Block != "a" {
		t.Errorf("late token = %+v, want the entry of jti-1, not delivered", issued)
	}

	if !errors.Is(adapterErr, provider.ErrUnavailable) {
		t.Errorf("the adapter got %v, want provider.ErrUnavailable and no token", adapterErr)
	}
}

func TestRunNeedsAReceiverOfLateTokens(t *testing.T) {
	t.Parallel()

	f := newLogin(nil, nil)
	f.spec.MintsTokens = true
	reg := registry(t, f)

	_, err := reg.Run(t.Context(), []provider.Block{login("a")}, provider.Call{
		Claims: nil, Tokens: func(string) provider.TokenSource { return tokenFunc(nil) }, Late: nil, Limit: 0,
	})
	if !errors.Is(err, profile.ErrMissingDependency) {
		t.Errorf("Run = %v, want profile.ErrMissingDependency", err)
	}
}

// TestRunBoundsTheCheckOfAnAdapter sends Run a Check that panics and a
// Check that blocks with no look at a context.
func TestRunBoundsTheCheckOfAnAdapter(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	for _, tc := range []struct {
		check func(provider.Value) error
		want  error
		name  string
	}{
		{func(provider.Value) error { panic("check fault") }, provider.ErrAdapter, "panic"},
		{func(provider.Value) error { <-release; return nil }, provider.ErrUnavailable, "blocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &calls{list: nil, mu: sync.Mutex{}}
			f := newLogin(log, nil)
			f.check = tc.check
			reg := registry(t, f)

			_, err := reg.Run(t.Context(), []provider.Block{login("a")}, provider.Call{
				Claims: nil, Tokens: nil, Late: ignoreLate, Limit: 100 * time.Millisecond,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want %v", err, tc.want)
			}

			for _, c := range log.get() {
				if c == "open test" {
					t.Errorf("an adapter opened after a failed check: %v", log.get())
				}
			}
		})
	}
}

// TestRunGivesEachBlockACopy sends a block that changes the login result of
// an earlier block and the claims of the open.
func TestRunGivesEachBlockACopy(t *testing.T) {
	t.Parallel()

	sec := newSecrets(nil, func(_ context.Context, r provider.Request) (provider.Result, error) {
		r.Login.Value["token"] = provider.Leaf{Value: "changed-by-b", Secret: false}
		r.Login.Value["extra"] = provider.Leaf{Value: "x", Secret: false}

		tags, ok := r.Claims["tags"].([]any)
		if !ok {
			return provider.Result{}, fmt.Errorf("%w: no tags", profile.ErrInvalid)
		}

		tags[0] = "changed-by-b"

		return provider.Result{
			Value:    provider.Value{"value": {Value: "v", Secret: true}},
			Expires:  time.Time{},
			Identity: "",
		}, nil
	})
	reg := registry(t, newLogin(nil, nil), sec)
	claims := provider.Claims{"tags": []any{"team"}}

	out, err := reg.Run(t.Context(), []provider.Block{
		login("a"),
		{
			Name:    "b",
			Use:     "secrets/test",
			Inputs:  provider.Value{"path": {Value: "p", Secret: false}},
			Login:   "a",
			Timeout: 0,
		},
	}, provider.Call{Claims: claims, Tokens: nil, Late: ignoreLate, Limit: 0})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	a := out.Results["a"].Value
	if tok := a["token"]; tok.Value != "tok-value" || !tok.Secret || len(a) != 1 {
		t.Errorf("block b changed the result of block a: %v", a)
	}

	if tags, ok := claims["tags"].([]any); !ok || tags[0] != "team" {
		t.Errorf("block b changed the claims of the open: %v", claims)
	}
}
