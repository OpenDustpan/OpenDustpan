package provider_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

func nested(depth int) any {
	var v any = "leaf"
	for range depth {
		v = []any{v}
	}

	return v
}

func TestCheckTree(t *testing.T) {
	t.Parallel()

	text := "x"

	for _, tc := range []struct {
		v    any
		name string
		ok   bool
	}{
		{"s", "string", true},
		{nil, "nil", true},
		{int64(1), "int64", true},
		{[]any{"a", 1.5, true, nil, map[string]any{"k": []any{int(2)}}}, "nested JSON types", true},
		{nested(provider.MaxDepth), "deepest allowed", true},
		{nested(provider.MaxDepth + 1), "too deep", false},
		{[]string{"a"}, "[]string", false},
		{map[string]string{"a": "b"}, "map[string]string", false},
		{[]byte("a"), "[]byte", false},
		{&text, "pointer", false},
		{[]any{[]string{"a"}}, "[]string in a list", false},
		{map[string]any{"k": map[string]any{"j": []int{1}}}, "[]int in a map in a map", false},
		{float32(1), "float32", false},
		{struct{}{}, "struct", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := provider.CheckTree(tc.v)
			if tc.ok != (err == nil) || (err != nil && !errors.Is(err, profile.ErrInvalid)) {
				t.Errorf("CheckTree = %v, want ok: %v", err, tc.ok)
			}
		})
	}
}

func TestCheckInputsRefusesASharedValueInAList(t *testing.T) {
	t.Parallel()

	spec := provider.Spec{
		Inputs: []provider.Field{field("l", provider.TypeList, true, false)}, Outputs: nil, MintsTokens: false,
	}

	err := provider.CheckInputs(&spec, provider.Value{"l": {Value: []any{[]string{"a"}}, Secret: false}})
	if !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("CheckInputs = %v, want profile.ErrInvalid", err)
	}
}

// TestRunRefusesClaimsThatACopyWouldShare sends claims with a []string: Run
// refuses them before any block runs, so no block can change them for the
// next block or for the caller.
func TestRunRefusesClaimsThatACopyWouldShare(t *testing.T) {
	t.Parallel()

	log := &calls{list: nil, mu: sync.Mutex{}}
	reg := registry(t, newLogin(log, nil))

	claims := provider.Claims{"groups": []string{"team"}}

	_, err := reg.Run(t.Context(), []provider.Block{login("a")}, provider.Call{
		Claims: claims, Tokens: nil, Late: ignoreLate, Limit: 0,
	})
	if !errors.Is(err, profile.ErrInvalid) || len(log.get()) != 0 {
		t.Errorf("Run = %v, calls %v, want profile.ErrInvalid and no call", err, log.get())
	}
}

// TestRunRefusesAResultThatACopyWouldShare sends a result whose list
// output holds a []string.
func TestRunRefusesAResultThatACopyWouldShare(t *testing.T) {
	t.Parallel()

	f := newSecrets(nil, func(context.Context, provider.Request) (provider.Result, error) {
		return provider.Result{
			Value: provider.Value{
				"value": {Value: "v", Secret: true},
				"names": {Value: []any{[]string{"a"}}, Secret: true},
			},
			Expires: time.Time{}, Identity: "",
		}, nil
	})
	f.spec.Outputs = append(f.spec.Outputs, field("names", provider.TypeList, false, true))
	reg := registry(t, f)

	out, err := reg.Run(t.Context(), []provider.Block{
		{
			Name:    "s",
			Use:     "secrets/test",
			Inputs:  provider.Value{"path": {Value: "p", Secret: false}},
			Login:   "",
			Timeout: 0,
		},
	}, noCall())
	if !errors.Is(err, provider.ErrAdapter) || out.Results != nil {
		t.Errorf("Run = %v, %v, want provider.ErrAdapter and no result", out, err)
	}
}

// TestRunCallsNoIssuerAfterTheDeadline sends a token request from an
// adapter that ignores its context and asks after the deadline of its block.
func TestRunCallsNoIssuerAfterTheDeadline(t *testing.T) {
	t.Parallel()

	src := &entrySource{calls: atomic.Int32{}}
	asked := make(chan error, 1)

	f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		<-ctx.Done()

		_, err := r.Tokens.Token(context.WithoutCancel(ctx), "aud", time.Minute)
		asked <- err

		return provider.Result{}, err
	})
	f.spec.MintsTokens = true
	reg := registry(t, f)

	b := login("a")
	b.Timeout = 20 * time.Millisecond

	var late atomic.Int32

	out, err := reg.Run(t.Context(), []provider.Block{b}, provider.Call{
		Claims: nil, Tokens: func(string) provider.TokenSource { return src },
		Late: func(provider.Issued) { late.Add(1) }, Limit: 0,
	})

	askErr := <-asked

	if !errors.Is(err, provider.ErrUnavailable) || len(out.Tokens) != 0 {
		t.Fatalf("Run = %v, %v, want provider.ErrUnavailable and no token", out, err)
	}

	if !errors.Is(askErr, provider.ErrUnavailable) || src.calls.Load() != 0 || late.Load() != 0 {
		t.Errorf("the late request = %v, issuer calls %d, late tokens %d, want provider.ErrUnavailable and none",
			askErr, src.calls.Load(), late.Load())
	}
}

func TestRunNamesTheCallerWhenTheOpenEndsDuringACheck(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	f := newLogin(nil, nil)
	f.check = func(provider.Value) error { <-release; return nil }
	reg := registry(t, f)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	_, err := reg.Run(ctx, []provider.Block{login("a")}, noCall())
	if !errors.Is(err, provider.ErrUnavailable) || !strings.Contains(err.Error(), "the open ended before the check") {
		t.Errorf("Run = %v, want provider.ErrUnavailable that names the end of the open", err)
	}
}
