package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/profile"
)

// Block is one provider block of an open, after the resolve of its inputs.
type Block struct {
	// Name is the key of the block under providers.
	Name string
	// Use is "<kind>/<name>" of the adapter.
	Use string
	// Inputs are the resolved inputs.
	Inputs Value
	// Login names an earlier login block whose result this block reads, or
	// is empty.
	Login string
	// Timeout is the deadline of the block. Zero means DefaultTimeout.
	Timeout time.Duration
}

// Call holds what Run gives each block of one open.
type Call struct {
	// Claims are the facts of the open. Each adapter gets a copy.
	Claims Claims
	// Tokens returns the token source of one block. Run calls it only for an
	// adapter whose spec mints tokens.
	Tokens func(block string) TokenSource
}

// BlockError is the error of one block. Err is the class: profile.ErrInvalid,
// profile.ErrMissingDependency, ErrRemoteDenied, ErrRemoteNotFound,
// ErrUnavailable, ErrTooLarge or ErrAdapter. Message is the text of the
// adapter error with each secret input and each token replaced, with no CR
// or LF, and at most MaxErrorBytes long. BlockError does not keep the error
// of the adapter, so errors.As cannot reach an error of a library through it.
type BlockError struct {
	Err     error
	Block   string
	Use     string
	Message string
}

// Error implements error.
func (e *BlockError) Error() string {
	return "provider block " + e.Block + " (" + e.Use + "): " + e.Message
}

// Unwrap returns the class of the error.
func (e *BlockError) Unwrap() error { return e.Err }

// classes returns the errors that an adapter may return, in the order that
// Run tests them.
func classes() []error {
	return []error{
		profile.ErrInvalid, profile.ErrMissingDependency, ErrRemoteDenied, ErrRemoteNotFound, ErrUnavailable,
		ErrTooLarge,
	}
}

// redacted replaces a secret in an error message.
const redacted = "[secret]"

// minTokenPart is the shortest part of a token that Run removes from an
// error message on its own: a part of a compact JWT is longer.
const minTokenPart = 16

// Run evaluates blocks in order for one open and returns the result of each
// block by name. It checks every block before it opens the first one: an
// unknown use, an input that the spec or the adapter refuses, a block name
// that repeats or is not valid, a timeout above MaxTimeout and a login
// reference that names no earlier login block all fail with
// profile.ErrInvalid, and no adapter is opened. It then opens each block with
// its deadline. On the first error it returns a *BlockError and no result.
func (r *Registry) Run(ctx context.Context, blocks []Block, c Call) (map[string]Result, error) {
	if len(blocks) > profile.MaxProviderBlocks {
		return nil, fmt.Errorf("%w: an open has at most %d provider blocks", profile.ErrInvalid,
			profile.MaxProviderBlocks)
	}

	entries, err := r.checkBlocks(blocks, c)
	if err != nil {
		return nil, err
	}

	results := make(map[string]Result, len(blocks))

	for i := range blocks {
		b := &blocks[i]

		res, err := r.open(ctx, b, &entries[i], c, results)
		if err != nil {
			return nil, err
		}

		results[b.Name] = res
	}

	return results, nil
}

// checkBlocks checks every block with no network and returns the adapter of
// each block.
func (r *Registry) checkBlocks(blocks []Block, c Call) ([]entry, error) {
	entries := make([]entry, len(blocks))
	kinds := make(map[string]Kind, len(blocks))

	for i := range blocks {
		b := &blocks[i]

		e, err := r.checkBlock(b, kinds)
		if err != nil {
			return nil, err
		}

		if e.spec.MintsTokens && c.Tokens == nil {
			return nil, blockFailure(b, profile.ErrMissingDependency, "the open has no token source")
		}

		entries[i] = e
		kinds[b.Name] = e.p.Kind()
	}

	return entries, nil
}

func (r *Registry) checkBlock(b *Block, kinds map[string]Kind) (entry, error) {
	if err := checkName(b.Name); err != nil {
		return entry{}, fmt.Errorf("a provider block: %w", err)
	}

	if _, dup := kinds[b.Name]; dup {
		return entry{}, blockFailure(b, profile.ErrInvalid, "the block name repeats")
	}

	if b.Timeout < 0 || b.Timeout > MaxTimeout {
		return entry{}, blockFailure(b, profile.ErrInvalid, fmt.Sprintf("the timeout is from 0 to %s", MaxTimeout))
	}

	e, err := r.lookup(b.Use)
	if err != nil {
		return entry{}, blockFailure(b, profile.ErrInvalid, "no provider adapter has this use")
	}

	if b.Login != "" && kinds[b.Login] != KindLogin {
		return entry{}, blockFailure(b, profile.ErrInvalid, "login names no earlier login block")
	}

	// Defense in depth: each adapter checks its inputs in Check and again in
	// Open. This generic check of the spec stays, so that an adapter whose
	// Check misses an input cannot get a value of another type or a plain
	// value for a secret input.
	if err := CheckInputs(&e.spec, b.Inputs); err != nil {
		return entry{}, newBlockError(b, err, secretsOf(b.Inputs))
	}

	if err := e.p.Check(b.Inputs); err != nil {
		return entry{}, newBlockError(b, err, secretsOf(b.Inputs))
	}

	return e, nil
}

func (r *Registry) open(
	ctx context.Context, b *Block, e *entry, c Call, results map[string]Result,
) (Result, error) {
	timeout := b.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req := Request{Inputs: maps.Clone(b.Inputs), Claims: maps.Clone(c.Claims), Tokens: nil, Login: nil}

	var tokens *recordingTokens

	if e.spec.MintsTokens {
		tokens = &recordingTokens{next: c.Tokens(b.Name), mu: sync.Mutex{}, values: nil}
		req.Tokens = tokens
	}

	if b.Login != "" {
		login := results[b.Login]
		req.Login = &login
	}

	res, err := e.p.Open(bctx, req)

	secrets := slices.Concat(secretsOf(b.Inputs), tokens.minted(), secretsOf(res.Value))

	if bctx.Err() != nil {
		// The deadline of the block or the open ended. The class is
		// ErrUnavailable, also for an adapter that returned another error
		// or a result late: a late result is not used.
		msg := fmt.Sprintf("the block did not end in %s", timeout)
		if ctx.Err() != nil {
			msg = "the open ended before the block"
		}

		return Result{}, blockFailure(b, ErrUnavailable, msg)
	}

	if err != nil {
		return Result{}, newBlockError(b, err, secrets)
	}

	if err := CheckResult(e.p.Kind(), &e.spec, &res, r.now()); err != nil {
		return Result{}, newBlockError(b, err, secrets)
	}

	return res, nil
}

// CheckResult validates the result of one block against the spec: each
// output is an output of the spec with the type of the spec, each secret
// output has the secret mark, the result has at most MaxResultBytes, a login
// result has an expiry after now, and Identity holds no secret value of the
// result. It returns ErrAdapter for a result that breaks the spec and
// ErrTooLarge for a result above the limit.
func CheckResult(k Kind, s *Spec, res *Result, now time.Time) error {
	if err := checkOutputs(s, res.Value); err != nil {
		return err
	}

	raw, err := json.Marshal(res.Value)
	if err != nil {
		return fmt.Errorf("%w: the result does not encode", ErrAdapter)
	}

	if len(raw)+len(res.Identity) > MaxResultBytes {
		return fmt.Errorf("%w: the result is above %d bytes", ErrTooLarge, MaxResultBytes)
	}

	if k == KindLogin && !res.Expires.After(now) {
		return fmt.Errorf("%w: a login result needs an expiry after now", ErrAdapter)
	}

	for _, secret := range secretsOf(res.Value) {
		if secret != "" && strings.Contains(res.Identity, secret) {
			return fmt.Errorf("%w: the identity holds a secret output", ErrAdapter)
		}
	}

	return nil
}

func checkOutputs(s *Spec, v Value) error {
	for name, leaf := range v {
		i := slices.IndexFunc(s.Outputs, func(f Field) bool { return f.Name == name })
		if i < 0 {
			return fmt.Errorf("%w: the output %s is not an output of the spec", ErrAdapter, safeName(name))
		}

		f := &s.Outputs[i]
		if f.Secret && !leaf.Secret {
			return fmt.Errorf("%w: the output %s has no secret mark", ErrAdapter, f.Name)
		}

		if checkType(f.Type, leaf.Value) != nil {
			return fmt.Errorf("%w: the output %s is not of the type %s", ErrAdapter, f.Name, f.Type)
		}
	}

	return nil
}

func blockFailure(b *Block, class error, msg string) *BlockError {
	return &BlockError{Err: class, Block: safeName(b.Name), Use: safeUse(b.Use), Message: msg}
}

// newBlockError classifies err and cleans its message.
func newBlockError(b *Block, err error, secrets []string) *BlockError {
	class := ErrAdapter

	for _, c := range classes() {
		if errors.Is(err, c) {
			class = c

			break
		}
	}

	return blockFailure(b, class, CleanMessage(err.Error(), secrets))
}

// CleanMessage returns msg with each secret replaced, CR and LF replaced by a
// space, and cut to MaxErrorBytes on a rune boundary. A secret of a compact
// JWT is removed whole and also part by part, so that a message that holds
// only the signature or the payload of a token loses it too.
func CleanMessage(msg string, secrets []string) string {
	for _, s := range expand(secrets) {
		msg = strings.ReplaceAll(msg, s, redacted)
	}

	msg = strings.NewReplacer("\r", " ", "\n", " ").Replace(msg)

	if len(msg) <= MaxErrorBytes {
		return msg
	}

	cut := MaxErrorBytes
	for cut > 0 && !utf8.RuneStart(msg[cut]) {
		cut--
	}

	return msg[:cut]
}

// expand returns the secrets, longest first, with the parts of each token.
func expand(secrets []string) []string {
	out := make([]string, 0, len(secrets))

	for _, s := range secrets {
		if s == "" {
			continue
		}

		out = append(out, s)

		for part := range strings.SplitSeq(s, ".") {
			if len(part) >= minTokenPart && part != s {
				out = append(out, part)
			}
		}
	}

	// The longest first, so that a part never breaks a longer secret apart
	// before its own replace.
	slices.SortStableFunc(out, func(a, b string) int { return len(b) - len(a) })

	return out
}

// secretsOf returns the text of each secret leaf of v, nested values too.
func secretsOf(v Value) []string {
	var out []string

	for _, leaf := range v {
		if leaf.Secret {
			out = appendStrings(out, leaf.Value)
		}
	}

	return out
}

func appendStrings(out []string, v any) []string {
	switch x := v.(type) {
	case string:
		return append(out, x)
	case []any:
		for _, e := range x {
			out = appendStrings(out, e)
		}
	case map[string]any:
		for _, e := range x {
			out = appendStrings(out, e)
		}
	default:
	}

	return out
}

// safeUse returns use for a message when it has the form of a use.
func safeUse(use string) string {
	kind, name, ok := strings.Cut(use, "/")
	if !ok || safeName(kind) != kind || safeName(name) != name {
		return "(a use that is not valid)"
	}

	return use
}

// recordingTokens keeps the value of each token that it mints, so that Run
// can remove them from an error message.
type recordingTokens struct {
	next   TokenSource
	values []string
	mu     sync.Mutex
}

func (t *recordingTokens) Token(ctx context.Context, audience string, lifetime time.Duration) (profile.Token, error) {
	if t.next == nil {
		return profile.Token{}, fmt.Errorf("%w: the open has no token source", profile.ErrMissingDependency)
	}

	tok, err := t.next.Token(ctx, audience, lifetime)
	if tok.Value != "" {
		t.mu.Lock()
		t.values = append(t.values, tok.Value)
		t.mu.Unlock()
	}

	if err != nil {
		return profile.Token{}, fmt.Errorf("token: %w", err)
	}

	return tok, nil
}

func (t *recordingTokens) minted() []string {
	if t == nil {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	return append([]string(nil), t.values...)
}
