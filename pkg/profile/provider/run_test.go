package provider_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/provider"
)

func clock() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }

// fake is an adapter whose Open runs a function of the test.
type fake struct {
	open  func(ctx context.Context, r provider.Request) (provider.Result, error)
	check func(in provider.Value) error
	log   *calls
	name  string
	kind  provider.Kind
	spec  provider.Spec
}

// calls records the order of the calls of the adapters.
type calls struct {
	list []string
	mu   sync.Mutex
}

func (c *calls) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.list = append(c.list, s)
}

func (c *calls) get() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]string(nil), c.list...)
}

func (f *fake) Adapter() string     { return f.name }
func (f *fake) Kind() provider.Kind { return f.kind }
func (f *fake) Spec() provider.Spec { return f.spec }
func (f *fake) Check(in provider.Value) error {
	if f.log != nil {
		f.log.add("check " + f.name)
	}

	if f.check != nil {
		return f.check(in)
	}

	return nil
}

func (f *fake) Open(ctx context.Context, r provider.Request) (provider.Result, error) {
	if f.log != nil {
		f.log.add("open " + f.name)
	}

	return f.open(ctx, r)
}

func field(name string, t provider.Type, required, secret bool) provider.Field {
	return provider.Field{Name: name, Type: t, Description: "The " + name + ".", Required: required, Secret: secret}
}

// loginSpec has a plain input, a secret input and a secret output.
func loginSpec() provider.Spec {
	return provider.Spec{
		Inputs: []provider.Field{
			field("audience", provider.TypeString, true, false),
			field("key", provider.TypeString, false, true),
		},
		Outputs: []provider.Field{
			field("token", provider.TypeString, true, true),
			field("note", provider.TypeString, false, false),
		},
		MintsTokens: false,
	}
}

func okLogin(context.Context, provider.Request) (provider.Result, error) {
	return provider.Result{
		Value:    provider.Value{"token": {Value: "tok-value", Secret: true}},
		Expires:  clock().Add(time.Hour),
		Identity: "role/a",
	}, nil
}

func newLogin(log *calls, open func(context.Context, provider.Request) (provider.Result, error)) *fake {
	if open == nil {
		open = okLogin
	}

	return &fake{open: open, check: nil, log: log, name: "test", kind: provider.KindLogin, spec: loginSpec()}
}

func newSecrets(log *calls, open func(context.Context, provider.Request) (provider.Result, error)) *fake {
	return &fake{
		open: open, check: nil, log: log, name: "test", kind: provider.KindSecrets,
		spec: provider.Spec{
			Inputs:      []provider.Field{field("path", provider.TypeString, true, false)},
			Outputs:     []provider.Field{field("value", provider.TypeString, true, true)},
			MintsTokens: false,
		},
	}
}

func registry(t *testing.T, ps ...provider.Provider) *provider.Registry {
	t.Helper()

	r, err := provider.NewRegistry(clock, ps...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	return r
}

func login(name string) provider.Block {
	return provider.Block{
		Name: name, Use: "login/test", Inputs: provider.Value{"audience": {Value: "aud", Secret: false}},
		Login: "", Timeout: 0,
	}
}

func noCall() provider.Call {
	return provider.Call{Claims: nil, Tokens: nil, Late: ignoreLate, Limit: 0}
}

func TestRunOpensEachBlockInOrderAfterEveryCheck(t *testing.T) {
	t.Parallel()

	log := &calls{list: nil, mu: sync.Mutex{}}

	var gotLogin *provider.Result

	sec := newSecrets(log, func(_ context.Context, r provider.Request) (provider.Result, error) {
		gotLogin = r.Login

		return provider.Result{
			Value:    provider.Value{"value": {Value: "v", Secret: true}},
			Expires:  time.Time{},
			Identity: "",
		}, nil
	})
	reg := registry(t, newLogin(log, nil), sec)

	blocks := []provider.Block{
		login("a"),
		{
			Name:    "b",
			Use:     "secrets/test",
			Inputs:  provider.Value{"path": {Value: "p", Secret: false}},
			Login:   "a",
			Timeout: 0,
		},
	}

	got, err := reg.Run(t.Context(), blocks, noCall())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := []string{"check test", "check test", "open test", "open test"}
	if fmt.Sprint(log.get()) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", log.get(), want)
	}

	if gotLogin == nil || gotLogin.Value["token"].Value != "tok-value" {
		t.Errorf("the secrets block got the login %v", gotLogin)
	}

	if got.Results["a"].Value["token"].Value != "tok-value" || got.Results["b"].Value["value"].Value != "v" {
		t.Errorf("results = %v", got)
	}
}

// TestRunRefusesBeforeAnyOpen sends each bad block to Run: the boundary
// check refuses it, and no adapter opens.
func TestRunRefusesBeforeAnyOpen(t *testing.T) {
	t.Parallel()

	bad := func(mod func(b *provider.Block)) []provider.Block {
		b := login("b")
		mod(&b)

		return []provider.Block{login("a"), b}
	}

	tooMany := make([]provider.Block, profile.MaxProviderBlocks+1)
	for i := range tooMany {
		tooMany[i] = login(fmt.Sprintf("b%d", i))
	}

	for _, tc := range []struct {
		name   string
		blocks []provider.Block
		want   error
	}{
		{"unknown use", bad(func(b *provider.Block) { b.Use = "login/none" }), profile.ErrInvalid},
		{"use with no kind", bad(func(b *provider.Block) { b.Use = "test" }), profile.ErrInvalid},
		{"use with a path", bad(func(b *provider.Block) { b.Use = "login/../test" }), profile.ErrInvalid},
		{"name repeats", bad(func(b *provider.Block) { b.Name = "a" }), profile.ErrInvalid},
		{"name with a newline", bad(func(b *provider.Block) { b.Name = "b\nc" }), profile.ErrInvalid},
		{"empty name", bad(func(b *provider.Block) { b.Name = "" }), profile.ErrInvalid},
		{"timeout above the limit", bad(func(b *provider.Block) { b.Timeout = provider.MaxTimeout + 1 }), profile.ErrInvalid},
		{"negative timeout", bad(func(b *provider.Block) { b.Timeout = -1 }), profile.ErrInvalid},
		{"login names a later block", bad(func(b *provider.Block) { b.Login = "c" }), profile.ErrInvalid},
		{"login names itself", bad(func(b *provider.Block) { b.Login = "b" }), profile.ErrInvalid},
		{"missing input", bad(func(b *provider.Block) { b.Inputs = provider.Value{} }), profile.ErrInvalid},
		{
			"plain secret input",
			bad(func(b *provider.Block) { b.Inputs["key"] = provider.Leaf{Value: "k", Secret: false} }),
			profile.ErrInvalid,
		},
		{
			"input of another type",
			bad(func(b *provider.Block) { b.Inputs["audience"] = provider.Leaf{Value: 3, Secret: false} }),
			profile.ErrInvalid,
		},
		{"too many blocks", tooMany, profile.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			log := &calls{list: nil, mu: sync.Mutex{}}
			reg := registry(t, newLogin(log, nil))

			_, err := reg.Run(t.Context(), tc.blocks, noCall())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want %v", err, tc.want)
			}

			for _, c := range log.get() {
				if strings.HasPrefix(c, "open") {
					t.Errorf("an adapter opened: %v", log.get())
				}
			}
		})
	}
}

func TestRunRefusesALoginOfAnotherKind(t *testing.T) {
	t.Parallel()

	ok := func(context.Context, provider.Request) (provider.Result, error) {
		return provider.Result{Value: provider.Value{}, Expires: time.Time{}, Identity: ""}, nil
	}
	reg := registry(t, newSecrets(nil, ok))
	p := provider.Value{"path": {Value: "p", Secret: false}}

	_, err := reg.Run(t.Context(), []provider.Block{
		{Name: "a", Use: "secrets/test", Inputs: p, Login: "", Timeout: 0},
		{Name: "b", Use: "secrets/test", Inputs: p, Login: "a", Timeout: 0},
	}, noCall())
	if !errors.Is(err, profile.ErrInvalid) {
		t.Errorf("Run = %v, want profile.ErrInvalid", err)
	}
}

func TestRunFailsClosedWithNoTokenSource(t *testing.T) {
	t.Parallel()

	f := newLogin(nil, nil)
	f.spec.MintsTokens = true
	reg := registry(t, f)

	_, err := reg.Run(t.Context(), []provider.Block{login("a")}, noCall())
	if !errors.Is(err, profile.ErrMissingDependency) {
		t.Errorf("Run = %v, want profile.ErrMissingDependency", err)
	}
}

func TestRunGivesTokensOnlyToAnAdapterThatMints(t *testing.T) {
	t.Parallel()

	for _, mints := range []bool{false, true} {
		t.Run(strconv.FormatBool(mints), func(t *testing.T) {
			t.Parallel()

			var got provider.TokenSource

			f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
				got = r.Tokens

				return okLogin(ctx, r)
			})
			f.spec.MintsTokens = mints
			reg := registry(t, f)

			_, err := reg.Run(t.Context(), []provider.Block{login("a")}, provider.Call{
				Claims: nil,
				Tokens: func(string) provider.TokenSource { return tokenFunc(nil) },
				Late:   ignoreLate,
				Limit:  0,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if (got != nil) != mints {
				t.Errorf("the adapter got a token source %v, want one: %v", got, mints)
			}
		})
	}
}

// TestRunDeadline checks the limit of each block: an adapter that waits for
// its context, and an adapter that ignores it and answers late.
func TestRunDeadline(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		open func(context.Context, provider.Request) (provider.Result, error)
		name string
	}{
		{
			name: "waits for the context",
			open: func(ctx context.Context, _ provider.Request) (provider.Result, error) {
				<-ctx.Done()

				return provider.Result{}, fmt.Errorf("wait: %w", ctx.Err())
			},
		},
		{
			name: "answers late",
			open: func(ctx context.Context, r provider.Request) (provider.Result, error) {
				<-ctx.Done()

				return okLogin(ctx, r)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := registry(t, newLogin(nil, tc.open))
			b := login("a")
			b.Timeout = 50 * time.Millisecond

			got, err := reg.Run(t.Context(), []provider.Block{b}, noCall())
			if !errors.Is(err, provider.ErrUnavailable) || got.Results != nil {
				t.Fatalf("Run = %v, %v, want provider.ErrUnavailable and no result", got, err)
			}

			if !strings.Contains(err.Error(), "did not end in 50ms") {
				t.Errorf("the error does not name the deadline: %v", err)
			}
		})
	}
}

func TestRunStopsWhenTheOpenEnds(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	reg := registry(t, newLogin(nil, func(c context.Context, _ provider.Request) (provider.Result, error) {
		cancel()
		<-c.Done()

		return provider.Result{}, fmt.Errorf("wait: %w", c.Err())
	}))

	_, err := reg.Run(ctx, []provider.Block{login("a")}, noCall())
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Errorf("Run = %v, want provider.ErrUnavailable", err)
	}
}

// errPlain is an error of no class.
var errPlain = errors.New("plain")

// libraryError stands for an error of a client library that must not reach
// the caller.
type libraryError struct{ body string }

func (e *libraryError) Error() string { return "library: " + e.body }

func TestRunClassifiesEachError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want error
	}{
		{fmt.Errorf("%w: bad", profile.ErrInvalid), profile.ErrInvalid},
		{fmt.Errorf("%w: no", provider.ErrRemoteDenied), provider.ErrRemoteDenied},
		{fmt.Errorf("%w: none", provider.ErrRemoteNotFound), provider.ErrRemoteNotFound},
		{fmt.Errorf("%w: down", provider.ErrUnavailable), provider.ErrUnavailable},
		{fmt.Errorf("%w: big", provider.ErrTooLarge), provider.ErrTooLarge},
		{&libraryError{body: "x"}, provider.ErrAdapter},
		{errPlain, provider.ErrAdapter},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			reg := registry(t, newLogin(nil, func(context.Context, provider.Request) (provider.Result, error) {
				return provider.Result{}, tc.err
			}))

			_, err := reg.Run(t.Context(), []provider.Block{login("a")}, noCall())
			if !errors.Is(err, tc.want) {
				t.Fatalf("Run = %v, want %v", err, tc.want)
			}

			lib, ok := errors.AsType[*libraryError](err)
			if ok {
				t.Errorf("the error of the library reaches the caller: %v", lib)
			}

			var be *provider.BlockError
			if !errors.As(err, &be) || be.Block != "a" || be.Use != "login/test" {
				t.Errorf("the error does not name the block: %v", err)
			}
		})
	}
}

// tokenFunc is a TokenSource that returns a token with a fixed value.
type tokenFunc func() string

func (f tokenFunc) Token(context.Context, string, time.Duration) (profile.Token, error) {
	v := "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJuYW1lc3BhY2U6YWNtZSJ9.c2lnbmF0dXJlLW9mLXRoZS10b2tlbg"
	if f != nil {
		v = f()
	}

	var tok profile.Token

	tok.Value = v

	return tok, nil
}

// TestRunCleansTheErrorText sends an adapter error with secrets to the
// cleaner of Run.
func TestRunCleansTheErrorText(t *testing.T) {
	t.Parallel()

	const (
		key = "the-secret-key"
		jwt = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJuYW1lc3BhY2U6YWNtZSJ9.c2lnbmF0dXJlLW9mLXRoZS10b2tlbg"
	)

	sig := strings.Split(jwt, ".")[2]

	for _, tc := range []struct {
		name   string
		msg    func(tok string) string
		absent []string
	}{
		{"secret input", func(string) string { return "denied for " + key }, []string{key}},
		{"token", func(tok string) string { return "refused " + tok }, []string{jwt, sig}},
		{"signature of the token", func(tok string) string { return "bad sig " + strings.Split(tok, ".")[2] }, []string{sig}},
		{"newline", func(string) string { return "a\r\nforged: line" }, []string{"\n", "\r"}},
		{"long", func(string) string { return strings.Repeat("é", provider.MaxErrorBytes) }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := runWithSecrets(t, key, tc.msg)

			var be *provider.BlockError
			if !errors.As(err, &be) || !errors.Is(err, provider.ErrRemoteDenied) ||
				len(be.Message) > provider.MaxErrorBytes {
				t.Fatalf("Run = %v, want a provider.ErrRemoteDenied of at most %d bytes", err, provider.MaxErrorBytes)
			}

			for _, s := range tc.absent {
				if strings.Contains(err.Error(), s) {
					t.Errorf("the error holds %q: %q", s, err)
				}
			}
		})
	}
}

// runWithSecrets runs one block with the secret input key, whose adapter
// gets a token and fails with the text msg(token).
func runWithSecrets(t *testing.T, key string, msg func(tok string) string) error {
	t.Helper()

	f := newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		tok, err := r.Tokens.Token(ctx, "aud", time.Minute)
		if err != nil {
			return provider.Result{}, fmt.Errorf("token: %w", err)
		}

		return provider.Result{}, fmt.Errorf("%w: %s", provider.ErrRemoteDenied, msg(tok.Value))
	})
	f.spec.MintsTokens = true
	reg := registry(t, f)

	b := login("a")
	b.Inputs["key"] = provider.Leaf{Value: key, Secret: true}

	_, err := reg.Run(t.Context(), []provider.Block{b}, provider.Call{
		Claims: nil, Tokens: func(string) provider.TokenSource { return tokenFunc(nil) }, Late: ignoreLate, Limit: 0,
	})

	return err
}

func TestCleanMessage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, msg, want string
		secrets         []string
	}{
		{"no secret", "plain", "plain", nil},
		{"empty secret", "plain", "plain", []string{""}},
		{"secret", "a s3cr3t b", "a [secret] b", []string{"s3cr3t"}},
		{"line breaks", "a\rb\nc", "a b c", nil},
		{"part of a token", "x abcdefghijklmnopq y", "x [secret] y", []string{"head.abcdefghijklmnopq.sig"}},
		{"cut on a rune", strings.Repeat("a", provider.MaxErrorBytes-1) + "é", strings.Repeat("a", provider.MaxErrorBytes-1), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := provider.CleanMessage(tc.msg, tc.secrets); got != tc.want {
				t.Errorf("CleanMessage = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunChecksEachResult sends a bad result to the result check of Run.
func TestRunChecksEachResult(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		res  provider.Result
		want error
		name string
	}{
		{
			name: "output not in the spec",
			res:  provider.Result{Value: provider.Value{"token": {Value: "t", Secret: true}, "other": {Value: "x", Secret: false}}, Expires: clock().Add(time.Hour), Identity: ""},
			want: provider.ErrAdapter,
		},
		{
			name: "secret output with no mark",
			res:  provider.Result{Value: provider.Value{"token": {Value: "t", Secret: false}}, Expires: clock().Add(time.Hour), Identity: ""},
			want: provider.ErrAdapter,
		},
		{
			name: "output of another type",
			res:  provider.Result{Value: provider.Value{"token": {Value: 1, Secret: true}}, Expires: clock().Add(time.Hour), Identity: ""},
			want: provider.ErrAdapter,
		},
		{
			name: "login with no expiry",
			res:  provider.Result{Value: provider.Value{"token": {Value: "t", Secret: true}}, Expires: time.Time{}, Identity: ""},
			want: provider.ErrAdapter,
		},
		{
			name: "login that expired",
			res:  provider.Result{Value: provider.Value{"token": {Value: "t", Secret: true}}, Expires: clock(), Identity: ""},
			want: provider.ErrAdapter,
		},
		{
			name: "identity holds the credential",
			res:  provider.Result{Value: provider.Value{"token": {Value: "tok-secret", Secret: true}}, Expires: clock().Add(time.Hour), Identity: "id tok-secret"},
			want: provider.ErrAdapter,
		},
		{
			name: "too large",
			res: provider.Result{
				Value:   provider.Value{"token": {Value: "t", Secret: true}, "note": {Value: strings.Repeat("a", provider.MaxResultBytes), Secret: false}},
				Expires: clock().Add(time.Hour), Identity: "",
			},
			want: provider.ErrTooLarge,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reg := registry(t, newLogin(nil, func(context.Context, provider.Request) (provider.Result, error) {
				return tc.res, nil
			}))

			got, err := reg.Run(t.Context(), []provider.Block{login("a")}, noCall())
			if !errors.Is(err, tc.want) || got.Results != nil {
				t.Fatalf("Run = %v, %v, want %v and no result", got, err, tc.want)
			}

			if strings.Contains(err.Error(), "tok-secret") {
				t.Errorf("the error holds the credential: %v", err)
			}
		})
	}
}

func TestRunGivesACopyOfTheClaims(t *testing.T) {
	t.Parallel()

	reg := registry(t, newLogin(nil, func(ctx context.Context, r provider.Request) (provider.Result, error) {
		r.Claims["access"] = "admin"

		return okLogin(ctx, r)
	}))
	claims := provider.Claims{"access": "read"}

	_, err := reg.Run(
		t.Context(),
		[]provider.Block{login("a")},
		provider.Call{Claims: claims, Tokens: nil, Late: ignoreLate, Limit: 0},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if claims["access"] != "read" {
		t.Errorf("the adapter changed the claims of the open: %v", claims)
	}
}

// ignoreLate is a receiver of late tokens for a test that expects none.
func ignoreLate(provider.Issued) {}
