// Package providertest is the conformance suite of the profile provider port.
// Each adapter runs RunProvider with a fake of its remote system.
package providertest

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

// Env is one call of Open: the inputs, the token source and the texts that no
// error and no identity may hold.
type Env struct {
	Inputs provider.Value
	Tokens provider.TokenSource
	// Secrets are texts of the fake that must not leave in an error, for
	// example a credential or a byte of an answer. The suite adds each
	// secret input and each token that the adapter got.
	Secrets []string
}

// ErrorCase makes the fake fail in one way.
type ErrorCase struct {
	Env  func(t *testing.T) Env
	Want error
	Name string
}

// Harness tells the suite how to run one adapter.
type Harness struct {
	// New returns the adapter under test.
	New func(t *testing.T) provider.Provider
	// Valid returns an Env that Open accepts.
	Valid func(t *testing.T) Env
	// Slow returns an Env whose remote system answers only after the
	// context of the call ends.
	Slow func(t *testing.T) Env
	// CheckResult runs the checks of the adapter on the result of Valid. It
	// may be nil.
	CheckResult func(t *testing.T, env Env, r provider.Result)
	// Now is the clock of the result check.
	Now func() time.Time
	// Audience is the audience that the adapter asks the token source for,
	// with the inputs of Valid. It is empty for an adapter that mints no
	// token.
	Audience string
	// Refused holds inputs that Check refuses. Open must refuse them too,
	// with no call to the token source.
	Refused []provider.Value
	// Errors holds one case for each error class that the fake can give.
	Errors []ErrorCase
}

// deadline is the deadline of the case Slow, and wait is how long the suite
// waits for Open to return after it.
const (
	deadline = 100 * time.Millisecond
	wait     = 5 * time.Second
)

// RunProvider runs the conformance suite on the adapter of h.
func RunProvider(t *testing.T, h *Harness) {
	t.Helper()

	t.Run("spec", func(t *testing.T) { t.Parallel(); testSpec(t, h) })
	t.Run("check refuses each bad input", func(t *testing.T) { t.Parallel(); testCheckInputs(t, h) })
	t.Run("refused input calls no network", func(t *testing.T) { t.Parallel(); testRefused(t, h) })
	t.Run("open", func(t *testing.T) { t.Parallel(); testOpen(t, h) })
	t.Run("errors", func(t *testing.T) { t.Parallel(); testErrors(t, h) })
	t.Run("deadline", func(t *testing.T) { t.Parallel(); testDeadline(t, h) })
}

func testSpec(t *testing.T, h *Harness) {
	t.Helper()

	p := h.New(t)
	spec := p.Spec()

	if !p.Kind().Valid() {
		t.Fatalf("Kind = %q, want a valid kind", p.Kind())
	}

	if err := provider.CheckSpec(p.Kind(), &spec); err != nil {
		t.Fatalf("CheckSpec: %v", err)
	}

	if _, err := provider.NewRegistry(h.Now, p); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if spec.MintsTokens != (h.Audience != "") {
		t.Errorf("MintsTokens = %v, but the harness audience is %q", spec.MintsTokens, h.Audience)
	}
}

// badInputs returns, for each input of the spec, inputs with one fault:
// a missing required input, a value of another type, a plain value for a
// secret input, and an input that the spec does not name.
func badInputs(spec *provider.Spec, valid provider.Value) map[string]provider.Value {
	out := make(map[string]provider.Value)

	for i := range spec.Inputs {
		f := &spec.Inputs[i]

		if f.Required {
			in := maps.Clone(valid)
			delete(in, f.Name)
			out["missing "+f.Name] = in
		}

		in := maps.Clone(valid)
		in[f.Name] = provider.Leaf{Value: otherType(f.Type), Secret: f.Secret}
		out["wrong type "+f.Name] = in

		if f.Secret {
			in := maps.Clone(valid)
			leaf := in[f.Name]
			leaf.Secret = false
			in[f.Name] = leaf
			out["plain secret "+f.Name] = in
		}
	}

	in := maps.Clone(valid)
	in["notAnInput"] = provider.Leaf{Value: "x", Secret: false}
	out["unknown input"] = in

	return out
}

// otherType returns a value that is not of the type t.
func otherType(t provider.Type) any {
	if t == provider.TypeBoolean {
		return "true"
	}

	return true
}

func testCheckInputs(t *testing.T, h *Harness) {
	t.Helper()

	p := h.New(t)
	spec := p.Spec()
	env := h.Valid(t)

	if err := p.Check(env.Inputs); err != nil {
		t.Fatalf("Check of the valid inputs: %v", err)
	}

	for name, in := range badInputs(&spec, env.Inputs) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// The generic check of the runtime refuses these inputs too.
			// The adapter must refuse them alone.
			if err := p.Check(in); !errors.Is(err, profile.ErrInvalid) {
				t.Errorf("Check = %v, want profile.ErrInvalid", err)
			}
		})
	}
}

func testRefused(t *testing.T, h *Harness) {
	t.Helper()

	p := h.New(t)

	for i, in := range h.Refused {
		if err := p.Check(in); !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("refused %d: Check = %v, want profile.ErrInvalid", i, err)
		}

		tokens := &Recorder{Next: nil, mu: sync.Mutex{}, calls: nil, values: nil}

		_, err := p.Open(t.Context(), provider.Request{Inputs: in, Claims: nil, Tokens: tokens, Login: nil})
		if !errors.Is(err, profile.ErrInvalid) {
			t.Errorf("refused %d: Open = %v, want profile.ErrInvalid", i, err)
		}

		if n := len(tokens.Audiences()); n != 0 {
			t.Errorf("refused %d: Open asked for %d tokens, want none", i, n)
		}
	}
}

func testOpen(t *testing.T, h *Harness) {
	t.Helper()

	p := h.New(t)
	spec := p.Spec()
	env := h.Valid(t)
	tokens := &Recorder{Next: env.Tokens, mu: sync.Mutex{}, calls: nil, values: nil}

	res, err := p.Open(t.Context(), provider.Request{Inputs: env.Inputs, Claims: nil, Tokens: tokens, Login: nil})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := provider.CheckResult(p.Kind(), &spec, &res, h.Now()); err != nil {
		t.Errorf("CheckResult: %v", err)
	}

	got := tokens.Audiences()
	if h.Audience == "" && len(got) != 0 {
		t.Errorf("the adapter asked for tokens %q, want none", got)
	}

	if h.Audience != "" && !slices.Equal(got, []string{h.Audience}) {
		t.Errorf("the adapter asked for tokens for %q, want one for %q", got, h.Audience)
	}

	for _, s := range secrets(env, tokens) {
		if strings.Contains(res.Identity, s) {
			t.Errorf("Identity %q holds a secret", res.Identity)
		}
	}

	if h.CheckResult != nil {
		h.CheckResult(t, env, res)
	}
}

func testErrors(t *testing.T, h *Harness) {
	t.Helper()

	for _, tc := range h.Errors {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			p := h.New(t)
			env := tc.Env(t)
			tokens := &Recorder{Next: env.Tokens, mu: sync.Mutex{}, calls: nil, values: nil}

			_, err := p.Open(t.Context(), provider.Request{Inputs: env.Inputs, Claims: nil, Tokens: tokens, Login: nil})
			if !errors.Is(err, tc.Want) {
				t.Fatalf("Open = %v, want %v", err, tc.Want)
			}

			checkErrorText(t, err, secrets(env, tokens))
		})
	}
}

func testDeadline(t *testing.T, h *Harness) {
	t.Helper()

	p := h.New(t)
	env := h.Slow(t)
	tokens := &Recorder{Next: env.Tokens, mu: sync.Mutex{}, calls: nil, values: nil}

	ctx, cancel := context.WithTimeout(t.Context(), deadline)
	defer cancel()

	start := time.Now()

	_, err := p.Open(ctx, provider.Request{Inputs: env.Inputs, Claims: nil, Tokens: tokens, Login: nil})
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("Open past the deadline = %v, want provider.ErrUnavailable", err)
	}

	if took := time.Since(start); took > wait {
		t.Errorf("Open returned %s after the deadline, want at most %s", took-deadline, wait)
	}

	if err != nil {
		checkErrorText(t, err, secrets(env, tokens))
	}
}

// checkErrorText fails t when the message of err holds a secret or is above
// the limit.
func checkErrorText(t *testing.T, err error, secrets []string) {
	t.Helper()

	msg := err.Error()
	if len(msg) > provider.MaxErrorBytes {
		t.Errorf("the error message has %d bytes, want at most %d", len(msg), provider.MaxErrorBytes)
	}

	for _, s := range secrets {
		if strings.Contains(msg, s) {
			t.Errorf("the error message holds a secret: %q", msg)
		}
	}
}

func secrets(env Env, r *Recorder) []string {
	out := slices.Clone(env.Secrets)

	for _, leaf := range env.Inputs {
		if s, ok := leaf.Value.(string); ok && leaf.Secret && s != "" {
			out = append(out, s)
		}
	}

	return append(out, r.Values()...)
}

// Recorder is a TokenSource that records the audience of each call and the
// value of each token that Next gives. With a nil Next it gives no token and
// returns profile.ErrMissingDependency.
type Recorder struct {
	Next   provider.TokenSource
	calls  []string
	values []string
	mu     sync.Mutex
}

// Token implements provider.TokenSource.
func (r *Recorder) Token(ctx context.Context, audience string, lifetime time.Duration) (profile.Token, error) {
	r.mu.Lock()
	r.calls = append(r.calls, audience)
	r.mu.Unlock()

	if r.Next == nil {
		return profile.Token{}, fmt.Errorf("%w: the call has no token source", profile.ErrMissingDependency)
	}

	tok, err := r.Next.Token(ctx, audience, lifetime)

	r.mu.Lock()
	if tok.Value != "" {
		r.values = append(r.values, tok.Value)
	}
	r.mu.Unlock()

	if err != nil {
		return profile.Token{}, fmt.Errorf("token source: %w", err)
	}

	return tok, nil
}

// Audiences returns the audience of each call.
func (r *Recorder) Audiences() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.calls)
}

// Values returns the value of each token that the recorder gave.
func (r *Recorder) Values() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.values)
}
