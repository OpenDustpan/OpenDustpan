package trustrulesvc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// ActionExchange is the audit action of a token exchange.
const ActionExchange = "trust-rule:exchange"

// Errors of an exchange. Each refusal of a token answers ErrNoMatch to the
// caller, so the answer never tells which check failed. The audit entry of
// the refusal names the check.
var (
	// ErrNoMatch reports a token that no trust rule of the namespace
	// accepts.
	ErrNoMatch = errors.New("the token matches no trust rule of the namespace")
	// ErrAmbiguous reports a token that more than one rule accepts, with no
	// rule named in the request.
	ErrAmbiguous = errors.New("the token matches more than one trust rule: name the rule in the request")
)

// TokenIssuer makes the access token of an exchange. *accesstoken.Service
// meets it.
type TokenIssuer interface {
	// NewTrustToken returns a new token of the kind trust, with its secret
	// and its digest, for the principal principalID, made at the time at.
	// It does not store the token. The lifetime is the exchange lifetime of
	// the token policy.
	NewTrustToken(ctx context.Context, at time.Time, principalID, name, createdBy string) (accesstoken.Issued, error)
}

// ExchangeConfig holds the dependencies of an Exchanger. Every field except
// Keys, Refusals, InstallationURLs and Now is required.
type ExchangeConfig struct {
	Rules      trustrule.Store
	Exchanges  trustrule.ExchangeStore
	Namespaces Namespaces
	Principals Principals
	Tokens     TokenIssuer
	// Audit keeps the entries of the refused exchanges.
	Audit audit.Store
	// Refusals bounds the audit entries of refused exchanges: one entry for
	// each namespace in each window, with the count of the refusals that it
	// stands for. Nil means a limiter of the exchanger only.
	Refusals *denylimit.Limiter
	// Keys reads the keys of a discovery rule. Nil refuses every discovery
	// rule: only static rules work.
	Keys trustrule.KeyFetcher
	// InstallationURLs are the base URLs of the issuers of this
	// installation.
	InstallationURLs []string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Exchanger exchanges the JWT of a workload for an access token, through
// the trust rules of a namespace.
type Exchanger struct {
	cfg      ExchangeConfig
	own      *Service
	refusals *denylimit.Limiter
	now      func() time.Time
}

// NewExchanger returns an Exchanger, or trustrule.ErrMissingDependency.
func NewExchanger(cfg ExchangeConfig) (*Exchanger, error) {
	switch {
	case cfg.Rules == nil, cfg.Exchanges == nil, cfg.Namespaces == nil, cfg.Principals == nil, cfg.Tokens == nil,
		cfg.Audit == nil:
		return nil, fmt.Errorf("%w: the exchange needs rules, exchanges, namespaces, principals, tokens and audit",
			trustrule.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	own, err := NewService(Config{
		Store: cfg.Rules, Namespaces: cfg.Namespaces, Principals: cfg.Principals, Authorizer: denyAll{},
		InstallationURLs: cfg.InstallationURLs, CheckURL: nil, Now: now,
	})
	if err != nil {
		return nil, err
	}

	refusals := cfg.Refusals
	if refusals == nil {
		refusals = denylimit.New(denylimit.Config{
			Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: now,
		})
	}

	return &Exchanger{cfg: cfg, own: own, refusals: refusals, now: now}, nil
}

// ExchangeInput is one exchange request. Rule is empty, or the ID of the
// rule that the caller wants. Request describes where the request came
// from, for the audit entries.
type ExchangeInput struct {
	Namespace namespace.Name
	Token     string
	Rule      trustrule.ID
	Request   audit.Entry
}

// ExchangeResult is the access token of an exchange and the rule that
// matched. The secret shows once.
type ExchangeResult struct {
	Token  accesstoken.Token
	Secret accesstoken.Secret
	Rule   trustrule.Rule
}

// Exchange checks the JWT in.Token against the trust rules of the namespace
// and issues an access token for the principal of the one rule that accepts
// it. Each check runs on its own:
//
//  1. the namespace exists;
//  2. the form of the token, and its issuer, which selects the rules;
//  3. for each rule of that issuer: the keys of the rule, the signature with
//     an allowed algorithm, the exact issuer, the exact audience, the time
//     claims with ClockSkew, and each condition;
//  4. the issuer is not an issuer of this installation;
//  5. the principal of the rule exists, is not a person, is not disabled,
//     and belongs to the namespace;
//  6. the store checks the fence, the rule and the principal again, and
//     stores the token with the audit entry in one transaction.
//
// A refusal writes an audit entry with the outcome denied, and answers
// ErrNoMatch or ErrAmbiguous. It never names the failed check to the caller.
func (x *Exchanger) Exchange(ctx context.Context, in ExchangeInput) (ExchangeResult, error) {
	ns, err := x.cfg.Namespaces.WritableHere(ctx, in.Namespace)
	if err != nil {
		// An unknown namespace has no audit log. The answer is the same as
		// for a token that matches no rule.
		return ExchangeResult{}, fmt.Errorf("%w: namespace: %w", ErrNoMatch, err)
	}

	rule, tok, err := x.match(ctx, in)
	if err != nil {
		return ExchangeResult{}, x.refuse(ctx, ns, in, err)
	}

	owner, err := x.checkGrant(ctx, &rule)
	if err != nil {
		return ExchangeResult{}, x.refuse(ctx, ns, in, err)
	}

	at := x.now().UTC().Truncate(time.Millisecond)

	issued, err := x.cfg.Tokens.NewTrustToken(ctx, at, rule.PrincipalID, "trust rule "+string(rule.ID),
		trustrule.Ref(rule.ID))
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("make the access token: %w", err)
	}

	entry := exchangeEntry(in.Request, at, &rule, &tok, issued.Token)
	entry.Subject = owner.Subject
	entry.PrincipalKind = string(owner.Kind)

	xch := trustrule.Exchange{Rule: rule.ID, Token: issued.Token, Digest: issued.Digest}
	if err := x.cfg.Exchanges.RecordExchange(ctx, xch, ns.Fence(), entry); err != nil {
		if errors.Is(err, trustrule.ErrNotFound) || errors.Is(err, trustrule.ErrPrincipal) {
			return ExchangeResult{}, x.refuse(ctx, ns, in, err)
		}

		return ExchangeResult{}, fmt.Errorf("record the exchange: %w", err)
	}

	return ExchangeResult{Token: issued.Token, Secret: issued.Secret, Rule: rule}, nil
}

// match returns the one rule that accepts the token of in, with the checked
// token.
func (x *Exchanger) match(ctx context.Context, in ExchangeInput) (trustrule.Rule, trustrule.Token, error) {
	issuer, err := trustrule.UnverifiedIssuer(in.Token)
	if err != nil {
		return trustrule.Rule{}, trustrule.Token{}, err
	}

	h, err := trustrule.Parse(in.Token)
	if err != nil {
		return trustrule.Rule{}, trustrule.Token{}, err
	}

	rules, err := x.cfg.Rules.List(ctx, in.Namespace)
	if err != nil {
		return trustrule.Rule{}, trustrule.Token{}, fmt.Errorf("list trust rules: %w", err)
	}

	return pick(x.candidates(ctx, rules, in, issuer, h.Kid))
}

// candidate is a rule of the issuer of a token, with the outcome of its
// checks.
type candidate struct {
	err  error
	tok  trustrule.Token
	rule trustrule.Rule
}

// candidates runs the checks of each rule of rules whose issuer is issuer,
// and of the rule in.Rule only when the request names one.
func (x *Exchanger) candidates(
	ctx context.Context, rules []trustrule.Rule, in ExchangeInput, issuer, kid string,
) []candidate {
	var out []candidate

	for i := range rules {
		r := &rules[i]
		if r.Issuer != issuer || (in.Rule != "" && r.ID != in.Rule) {
			continue
		}

		tok, err := x.check(ctx, r, in.Token, kid)
		out = append(out, candidate{err: err, tok: tok, rule: *r})
	}

	return out
}

// pick returns the one candidate that passed its checks. No candidate gives
// trustrule.ErrToken with the reason of each rule, and more than one gives
// ErrAmbiguous.
func pick(cs []candidate) (trustrule.Rule, trustrule.Token, error) {
	var (
		passed  []candidate
		reasons []string
	)

	for i := range cs {
		if cs[i].err != nil {
			reasons = append(reasons, string(cs[i].rule.ID)+": "+cs[i].err.Error())

			continue
		}

		passed = append(passed, cs[i])
	}

	switch {
	case len(passed) == 1:
		return passed[0].rule, passed[0].tok, nil
	case len(passed) > 1:
		return trustrule.Rule{}, trustrule.Token{}, ErrAmbiguous
	case len(reasons) == 0:
		return trustrule.Rule{}, trustrule.Token{}, fmt.Errorf("%w: no rule of the issuer", trustrule.ErrToken)
	default:
		return trustrule.Rule{}, trustrule.Token{}, fmt.Errorf(
			"%w: %s",
			trustrule.ErrToken,
			strings.Join(reasons, "; "),
		)
	}
}

// check runs the checks of the rule r on the token raw.
func (x *Exchanger) check(ctx context.Context, r *trustrule.Rule, raw, kid string) (trustrule.Token, error) {
	keys, err := x.keysOf(ctx, r, kid)
	if err != nil {
		return trustrule.Token{}, err
	}

	tok, err := trustrule.Verify(raw, keys)
	if err != nil {
		return trustrule.Token{}, err
	}

	if err := tok.CheckIssuer(r.Issuer); err != nil {
		return trustrule.Token{}, err
	}

	if err := tok.CheckAudience(r.Audience); err != nil {
		return trustrule.Token{}, err
	}

	if err := tok.CheckTime(x.now()); err != nil {
		return trustrule.Token{}, err
	}

	if !r.Matches(&tok) {
		return trustrule.Token{}, fmt.Errorf("%w: a condition of the rule does not match", trustrule.ErrToken)
	}

	return tok, nil
}

// keysOf returns the keys of r: the JWKS of a static rule, or the keys that
// the fetcher reads for a discovery rule.
func (x *Exchanger) keysOf(ctx context.Context, r *trustrule.Rule, kid string) (trustrule.KeySet, error) {
	switch r.Keys {
	case trustrule.KeySourceStatic:
		keys, err := trustrule.ParseKeySet([]byte(r.StaticKeys))
		if err != nil {
			return trustrule.KeySet{}, fmt.Errorf("%w: %w", trustrule.ErrKeysUnavailable, err)
		}

		return keys, nil
	case trustrule.KeySourceDiscovery:
		if x.cfg.Keys == nil {
			return trustrule.KeySet{}, fmt.Errorf(
				"%w: the server reads no discovery keys",
				trustrule.ErrKeysUnavailable,
			)
		}

		keys, err := x.cfg.Keys.Keys(ctx, r.Issuer, kid)
		if err != nil {
			return trustrule.KeySet{}, fmt.Errorf("read the keys of the issuer: %w", err)
		}

		return keys, nil
	default:
		return trustrule.KeySet{}, fmt.Errorf("%w: key source %q", trustrule.ErrKeysUnavailable, r.Keys)
	}
}

// checkGrant checks the issuer and the principal of the matched rule r again,
// at the time of the exchange, and returns the principal.
func (x *Exchanger) checkGrant(ctx context.Context, r *trustrule.Rule) (principal.Principal, error) {
	var none principal.Principal

	// Defense in depth: the create of the rule refused an issuer of this
	// installation. This check stays so that a rule of a restore or of an
	// earlier setting of the public URL never turns a token of this
	// installation into an access token.
	if err := x.own.CheckIssuer(r.Issuer); err != nil {
		return none, err
	}

	// Defense in depth: the store checks the principal again in the
	// transaction of the exchange. This check stays so that a refused
	// principal costs no token and no write.
	p, err := x.cfg.Principals.GetPrincipal(ctx, r.PrincipalID)
	if errors.Is(err, principal.ErrNotFound) {
		return none, fmt.Errorf("%w: no principal %q", trustrule.ErrPrincipal, r.PrincipalID)
	}

	if err != nil {
		return none, fmt.Errorf("read principal %q: %w", r.PrincipalID, err)
	}

	switch {
	case p.Kind == principal.KindPerson:
		return none, fmt.Errorf("%w: principal %q is a person", trustrule.ErrPrincipal, p.ID)
	case p.Namespace != r.Namespace:
		return none, fmt.Errorf("%w: principal %q is not a principal of the namespace", trustrule.ErrPrincipal, p.ID)
	case !p.DisabledAt.IsZero():
		return none, fmt.Errorf("%w: principal %q is disabled", trustrule.ErrPrincipal, p.ID)
	default:
		return p, nil
	}
}

// refuse writes the audit entry of a refused exchange and returns the error
// for the caller: ErrAmbiguous as it is, and ErrNoMatch for each other
// refusal. The limiter of the refusals decides whether this refusal writes
// an entry: the first of a window does, with the count of the refusals that
// the window before it held back. A failed write of the entry is joined to
// the answer, so the caller fails closed.
func (x *Exchanger) refuse(ctx context.Context, ns namespace.Namespace, in ExchangeInput, cause error) error {
	answer := ErrNoMatch
	if errors.Is(cause, ErrAmbiguous) {
		answer = ErrAmbiguous
	}

	// The key names the namespace twice: as the principal, so the bound of
	// keys of one principal never joins two namespaces, and as the scope,
	// which a flush reads.
	d := x.refusals.Allow(denylimit.Key{
		Principal: refusedSubject + ":" + ns.Name.String(), Action: ActionExchange, Kind: "trustrules:exchange",
		Scope: ns.Name.String(),
	})

	flushErr := x.flush(ctx, in.Request, d.Flush)

	if !d.Write {
		if flushErr != nil {
			return errors.Join(answer, flushErr)
		}

		return answer
	}

	e := in.Request
	e.At = x.now().UTC()
	e.Namespace = ns.Name
	e.Subject = refusedSubject
	e.TokenID = ""
	e.Action = ActionExchange
	e.Object = identity.NamespaceObject(ns.Name).String()
	e.ObjectID = string(in.Rule)
	e.Kind = audit.KindSecurity
	e.Outcome = audit.OutcomeDenied
	e.PrincipalKind = ""
	e.Changes = nil
	e.Reason = clean(cause.Error(), maxReasonBytes)

	if d.Suppressed > 0 {
		e.Reason = clean(fmt.Sprintf("%d refused exchanges before this one wrote no entry; %s", d.Suppressed,
			cause.Error()), maxReasonBytes)
	}

	if _, err := x.cfg.Audit.Append(ctx, ns.Fence(), e); err != nil {
		return errors.Join(answer, flushErr, fmt.Errorf("write the audit entry of a refused exchange: %w", err))
	}

	if flushErr != nil {
		return errors.Join(answer, flushErr)
	}

	return answer
}

// flush writes one entry for each count of refusals that the limiter held
// back in an earlier window, in the namespace of the count. A count of the
// aggregate of the limiter names no namespace, so it has no audit log to go
// to: the next entry of a namespace carries the counts of that namespace.
func (x *Exchanger) flush(ctx context.Context, request audit.Entry, pending []denylimit.Pending) error {
	var errs []error

	for _, p := range pending {
		if p.Overflow || p.Key.Scope == "" || p.Key.Scope == denylimit.AggregateMark || p.Suppressed == 0 {
			continue
		}

		ns, err := x.cfg.Namespaces.Get(ctx, namespace.Name(p.Key.Scope))
		if err != nil {
			errs = append(errs, fmt.Errorf("read namespace %s: %w", p.Key.Scope, err))

			continue
		}

		e := request
		e.At = x.now().UTC()
		e.Namespace = ns.Name
		e.Subject = refusedSubject
		e.TokenID = ""
		e.Action = ActionExchange
		e.Object = identity.NamespaceObject(ns.Name).String()
		e.ObjectID = ""
		e.Kind = audit.KindSecurity
		e.Outcome = audit.OutcomeDenied
		e.PrincipalKind = ""
		e.Changes = nil
		e.Reason = fmt.Sprintf("%d refused exchanges wrote no entry of their own", p.Suppressed)

		if _, err := x.cfg.Audit.Append(ctx, ns.Fence(), e); err != nil {
			errs = append(errs, fmt.Errorf("write the count of refused exchanges: %w", err))
		}
	}

	return errors.Join(errs...)
}

// refusedSubject is the subject of the audit entry of a refused exchange: no
// principal acted. It starts with '@', which no subject of a principal holds.
const refusedSubject = "@workload"

// Bounds of the text of a token in an audit entry.
const (
	maxReasonBytes = 1024
	maxClaimBytes  = 256
)

// exchangeEntry returns the audit entry of the exchange of tok through r,
// which issued t. It names the claims that identify the run: the issuer, the
// subject and the jti of the token. It never holds the token.
func exchangeEntry(
	e audit.Entry,
	at time.Time,
	r *trustrule.Rule,
	tok *trustrule.Token,
	t accesstoken.Token,
) audit.Entry {
	e.At = at
	e.Namespace = r.Namespace
	e.TokenID = t.ID
	e.Action = ActionExchange
	e.Object = identity.NamespaceObject(r.Namespace).String()
	e.ObjectID = string(r.ID)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = []audit.Change{
		{Key: "principal", Old: nil, New: r.PrincipalID, Layer: "", TakesEffect: ""},
		{Key: "issuer", Old: nil, New: r.Issuer, Layer: "", TakesEffect: ""},
		{Key: "subject", Old: nil, New: clean(tok.Subject(), maxClaimBytes), Layer: "", TakesEffect: ""},
		{Key: "jti", Old: nil, New: clean(tok.ID(), maxClaimBytes), Layer: "", TakesEffect: ""},
		{Key: "expiresAt", Old: nil, New: t.ExpiresAt.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	return e
}

// clean returns s with each control or format character replaced by its
// escape, cut to at most n bytes, so a claim never adds a line to a log.
func clean(s string, n int) string {
	var b strings.Builder

	for _, c := range s {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			b.WriteString(strconv.QuoteRune(c))

			continue
		}

		if b.Len()+len(string(c)) > n {
			break
		}

		b.WriteRune(c)
	}

	return b.String()
}

// denyAll is the authorizer of the rule checks of the exchange: the exchange
// makes no rule, so it allows nothing.
type denyAll struct{}

func (denyAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return identity.ErrDenied
}

func (denyAll) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}, nil
}
