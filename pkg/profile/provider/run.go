package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
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
	// Late receives each token that the issuer signed for a block after
	// the block ended. Its value never reached the adapter, but the issuer
	// signed it, so the caller commits its entry too. Run can call Late from
	// other goroutines, also after Run returned, and two calls can run at
	// the same time, so the receiver must be safe for concurrent calls. A
	// call that comes after the transaction of the open commits its entry
	// on its own. An open with a block that mints tokens needs Late.
	Late func(Issued)
	// Limit is the deadline of the whole open, the checks of the blocks
	// included. Zero means MaxRunTime. A caller can set a shorter limit,
	// never a longer one.
	Limit time.Duration
}

// Outcome is what Run gives the open.
//
// The caller commits the Entry of each item of Tokens in the transaction
// that records the open, and gives no value of Results to anyone before
// that commit. When Run fails, Results is nil, and the caller still commits
// the entries of Tokens: the issuer signed each of them, and an adapter
// may have sent it to a remote system. A token that the issuer signed after
// the end of its block goes to Call.Late instead, and the caller commits its
// entry too. So each token that the issuer signs reaches the caller.
type Outcome struct {
	// Results holds the result of each block by name.
	Results map[string]Result
	// Tokens holds each token that the issuer signed for a block while the
	// block ran, in the order of the calls.
	Tokens []Issued
}

// Issued is one token that a block got from its token source. It holds no
// token value.
type Issued struct {
	// Entry is the audit entry of the token, from profile.Token.
	Entry audit.Entry
	// Block is the name of the block that asked for the token.
	Block string
	// ID is the jti of the token.
	ID string
	// Warnings are the texts for a person from profile.Token.
	Warnings []string
	// Delivered says that the adapter got the value of the token. It is
	// false for a token that the source answered with an error or after
	// the end of the block.
	Delivered bool
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

// MaxRunTime is the longest time of the provider blocks of one open, all
// blocks together.
const MaxRunTime = 60 * time.Second

// errRunLimit is the cause of the end of the deadline of a whole open.
var errRunLimit = errors.New("the provider blocks of the open did not end in time")

// minTokenPart is the shortest part of a token that Run removes from an
// error message on its own: a part of a compact JWT is longer.
const minTokenPart = 16

// Run evaluates blocks in order for one open. It checks every block before
// it opens the first one: an unknown use, an input that the spec or the
// adapter refuses, a block name that repeats or is not valid, a timeout above
// MaxTimeout, a limit above MaxRunTime and a login reference that names no
// earlier login block all fail with profile.ErrInvalid, and no adapter is
// opened. It then opens each block with its deadline, and all blocks within
// the limit of the open. Run enforces each deadline itself: it does not wait
// for an adapter that ignores its context. On the first error it returns a
// *BlockError, no result, and the tokens that the blocks got until then.
func (r *Registry) Run(ctx context.Context, blocks []Block, c Call) (Outcome, error) {
	out := Outcome{Results: nil, Tokens: nil}

	if len(blocks) > profile.MaxProviderBlocks {
		return out, fmt.Errorf("%w: an open has at most %d provider blocks", profile.ErrInvalid,
			profile.MaxProviderBlocks)
	}

	if c.Limit < 0 || c.Limit > MaxRunTime {
		return out, fmt.Errorf("%w: the limit of an open is from 0 to %s", profile.ErrInvalid, MaxRunTime)
	}

	limit := c.Limit
	if limit == 0 {
		limit = MaxRunTime
	}

	// The claims reach every adapter. A value that the deep copy would
	// share is refused before any block runs.
	if err := CheckTree(map[string]any(c.Claims)); err != nil {
		return out, fmt.Errorf("the claims of the open: %w", err)
	}

	rctx, cancel := context.WithTimeoutCause(ctx, limit, errRunLimit)
	defer cancel()

	entries, err := r.checkBlocks(rctx, blocks, c)
	if err != nil {
		return out, limitError(rctx, err, limit)
	}

	results := make(map[string]Result, len(blocks))

	for i := range blocks {
		b := &blocks[i]

		res, issued, err := r.open(rctx, b, &entries[i], c, results)
		out.Tokens = append(out.Tokens, issued...)

		if err != nil {
			return out, limitError(rctx, err, limit)
		}

		results[b.Name] = res
	}

	// The caller gets its own copy: an adapter goroutine that still holds a
	// result cannot change what the caller reads.
	out.Results = make(map[string]Result, len(results))
	for name, res := range results {
		out.Results[name] = cloneResult(res)
	}

	return out, nil
}

// limitError replaces err with the error of the limit of the open when that
// limit ended.
func limitError(ctx context.Context, err error, limit time.Duration) error {
	if !errors.Is(context.Cause(ctx), errRunLimit) {
		return err
	}

	be := &BlockError{Err: ErrUnavailable, Block: "", Use: "", Message: ""}
	if errors.As(err, &be) {
		return blockFailure(&Block{Name: be.Block, Use: be.Use, Inputs: nil, Login: "", Timeout: 0},
			ErrUnavailable, fmt.Sprintf("the provider blocks did not end in %s", limit))
	}

	return fmt.Errorf("%w: the provider blocks did not end in %s", ErrUnavailable, limit)
}

// checkBlocks checks every block with no network and returns the adapter of
// each block.
func (r *Registry) checkBlocks(ctx context.Context, blocks []Block, c Call) ([]entry, error) {
	entries := make([]entry, len(blocks))
	kinds := make(map[string]Kind, len(blocks))

	for i := range blocks {
		b := &blocks[i]

		e, err := r.checkBlock(ctx, b, kinds)
		if err != nil {
			return nil, err
		}

		if e.spec.MintsTokens && (c.Tokens == nil || c.Late == nil) {
			return nil, blockFailure(b, profile.ErrMissingDependency,
				"the open has no token source or no receiver of late tokens")
		}

		entries[i] = e
		kinds[b.Name] = e.p.Kind()
	}

	return entries, nil
}

func (r *Registry) checkBlock(ctx context.Context, b *Block, kinds map[string]Kind) (entry, error) {
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

	if err := callCheck(ctx, e.p, cloneValue(b.Inputs)); err != nil {
		return entry{}, newBlockError(b, err, secretsOf(b.Inputs))
	}

	return e, nil
}

// opened is the answer of one call of Open.
type opened struct {
	err error
	res Result
}

func (r *Registry) open(
	ctx context.Context, b *Block, e *entry, c Call, results map[string]Result,
) (Result, []Issued, error) {
	timeout := b.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	bctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Each adapter gets deep copies, so that it cannot change the inputs,
	// the claims or the result of an earlier block after their checks.
	req := Request{Inputs: cloneValue(b.Inputs), Claims: cloneClaims(c.Claims), Tokens: nil, Login: nil}

	var tokens *recordingTokens

	if e.spec.MintsTokens {
		tokens = &recordingTokens{
			next: c.Tokens(b.Name), late: c.Late, done: bctx.Done(), block: b.Name, values: nil, issued: nil,
			mu: sync.Mutex{}, closed: false,
		}
		req.Tokens = tokens
	}

	if b.Login != "" {
		login := cloneResult(results[b.Login])
		req.Login = &login
	}

	res, err := callOpen(bctx, e.p, req)

	// Close the token source of the block before it reads the tokens: a
	// token that a late adapter asks for after this point is not signed for
	// it, and no token leaves without its entry in issued.
	issued, minted := tokens.close()
	secrets := slices.Concat(secretsOf(b.Inputs), minted, secretsOf(res.Value))

	if bctx.Err() != nil {
		// The deadline of the block or the open ended. The class is
		// ErrUnavailable, also for an adapter that returned another error
		// or a result late: a late result is not used.
		msg := fmt.Sprintf("the block did not end in %s", timeout)
		if ctx.Err() != nil {
			msg = "the open ended before the block"
		}

		return Result{}, issued, blockFailure(b, ErrUnavailable, msg)
	}

	if err != nil {
		return Result{}, issued, newBlockError(b, err, secrets)
	}

	// Check and keep a copy: the adapter goroutine may still hold res.
	res = cloneResult(res)
	if err := CheckResult(e.p.Kind(), &e.spec, &res, r.now()); err != nil {
		return Result{}, issued, newBlockError(b, err, secrets)
	}

	return res, issued, nil
}

// callCheck calls p.Check in its own goroutine and returns when Check
// returns or ctx ends, as callOpen does for Open. A panic of the adapter is
// an ErrAdapter with no text of the panic.
func callCheck(ctx context.Context, p Provider, in Value) error {
	done := make(chan error, 1)

	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- fmt.Errorf("%w: the check of the adapter stopped with a panic", ErrAdapter)
			}
		}()

		done <- p.Check(in)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if errors.Is(context.Cause(ctx), errRunLimit) {
			return fmt.Errorf("%w: the check of the block did not end before the limit of the open", ErrUnavailable)
		}

		return fmt.Errorf("%w: the open ended before the check of the block", ErrUnavailable)
	}
}

// callOpen calls p.Open in its own goroutine and returns when Open returns
// or ctx ends, whichever is first. An adapter that ignores ctx cannot hold
// the open past its deadline. Its goroutine ends when Open returns, and its
// late answer is dropped. A panic of the adapter is an ErrAdapter.
func callOpen(ctx context.Context, p Provider, req Request) (Result, error) {
	done := make(chan opened, 1)

	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- opened{err: fmt.Errorf("%w: the adapter stopped with a panic", ErrAdapter), res: Result{Expires: time.Time{}, Value: nil, Identity: ""}}
			}
		}()

		res, err := p.Open(ctx, req)
		done <- opened{err: err, res: res}
	}()

	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		return Result{}, fmt.Errorf("%w: the block did not end before its deadline", ErrUnavailable)
	}
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

		if CheckTree(leaf.Value) != nil {
			return fmt.Errorf("%w: the output %s holds a value of a type that a result cannot hold", ErrAdapter,
				f.Name)
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

// errClosed is the error of a token source after the end of its block.
var errClosed = errors.New("the block ended, and its token source is closed")

// recordingTokens keeps the value of each token that it mints, so that Run
// can remove them from an error message, and the audit entry of each token
// that the issuer signed, so that the caller can commit it. After close it
// calls no issuer, and it hands a token that the issuer signed during the
// close to late.
type recordingTokens struct {
	next TokenSource
	late func(Issued)
	// done is the Done channel of the context of the block. The source calls
	// no issuer after it is closed, also for an adapter that ignores its
	// context.
	done   <-chan struct{}
	block  string
	values []string
	issued []Issued
	mu     sync.Mutex
	closed bool
}

func (t *recordingTokens) Token(ctx context.Context, audience string, lifetime time.Duration) (profile.Token, error) {
	if t.next == nil {
		return profile.Token{}, fmt.Errorf("%w: the open has no token source", profile.ErrMissingDependency)
	}

	t.mu.Lock()
	closed := t.closed
	t.mu.Unlock()

	if closed || t.ended() {
		return profile.Token{}, fmt.Errorf("%w: %w", ErrUnavailable, errClosed)
	}

	tok, err := t.next.Token(ctx, audience, lifetime)
	late, refused := t.record(&tok, err)

	if late != nil && t.late != nil {
		// The issuer signed after the end of the block. The value never
		// reaches the adapter, but the entry reaches the caller.
		t.late(*late)
	}

	if err != nil {
		return profile.Token{}, fmt.Errorf("token: %w", err)
	}

	// The block can end while the issuer signs the token. The value does not
	// reach the adapter. The entry of the signing stays with the block, or goes
	// to late when the block closed. record reads the end of the block once, so the entry says Delivered only
	// for a value that this check lets go.
	if refused {
		return profile.Token{}, fmt.Errorf("%w: %w", ErrUnavailable, errClosed)
	}

	if tok.Value == "" {
		return profile.Token{}, fmt.Errorf("%w: the token source gave no token", ErrUnavailable)
	}

	return tok, nil
}

// ended reports whether the context of the block ended.
func (t *recordingTokens) ended() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

// record keeps a token that the issuer signed. It returns the token for late
// when the block closed while the issuer signed, and whether the value must
// not reach the adapter: the block closed or its context ended.
func (t *recordingTokens) record(tok *profile.Token, err error) (*Issued, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	refused := t.closed || t.ended()

	if tok == nil || tok.Value == "" {
		return nil, refused
	}

	t.values = append(t.values, tok.Value)
	issued := Issued{
		Entry: tok.Entry, Block: t.block, ID: tok.ID, Warnings: slices.Clone(tok.Warnings),
		Delivered: err == nil && !refused,
	}

	if t.closed {
		return &issued, true
	}

	t.issued = append(t.issued, issued)

	return nil, refused
}

// close stops the source and returns the tokens that the issuer signed for
// the block and the value of each of them.
func (t *recordingTokens) close() ([]Issued, []string) {
	if t == nil {
		return nil, nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.closed = true

	return slices.Clone(t.issued), slices.Clone(t.values)
}

// cloneValue returns a deep copy of v.
func cloneValue(v Value) Value {
	if v == nil {
		return nil
	}

	out := make(Value, len(v))
	for k, leaf := range v {
		out[k] = Leaf{Value: cloneAny(leaf.Value), Secret: leaf.Secret}
	}

	return out
}

// cloneClaims returns a deep copy of c.
func cloneClaims(c Claims) Claims {
	if c == nil {
		return nil
	}

	out := make(Claims, len(c))
	for k, v := range c {
		out[k] = cloneAny(v)
	}

	return out
}

// cloneResult returns r with a deep copy of its value.
func cloneResult(r Result) Result {
	r.Value = cloneValue(r.Value)

	return r
}

func cloneAny(v any) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = cloneAny(e)
		}

		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = cloneAny(e)
		}

		return out
	default:
		// CheckTree lets only immutable values reach this branch: a
		// string, a bool, a number or nil. Each list and map is copied
		// above, so the copy shares no memory with v.
		return v
	}
}
