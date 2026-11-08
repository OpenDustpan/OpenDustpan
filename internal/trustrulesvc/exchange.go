package trustrulesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
// Keys, Refusals, InstallationURLs, Now and Log is required.
type ExchangeConfig struct {
	Rules      trustrule.Store
	Exchanges  trustrule.ExchangeStore
	Namespaces Namespaces
	Principals Principals
	Tokens     TokenIssuer
	// Audit keeps the entries of the refused exchanges.
	Audit audit.Store
	// ServerAudit keeps the entries of the installation: the count of the
	// refusals that the overflow of the limiter held back, which can hold
	// the refusals of many namespaces.
	ServerAudit audit.ServerStore
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
	// OwnIssuer is the check of the issuers that this installation signs
	// for itself. Nil means that the installation has no issuer, and only
	// InstallationURLs apply.
	OwnIssuer OwnIssuer
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Log is the logger of the warnings. Nil means a logger that discards.
	Log *slog.Logger
}

// Exchanger exchanges the JWT of a workload for an access token, through
// the trust rules of a namespace.
type Exchanger struct {
	cfg      ExchangeConfig
	own      *Service
	refusals *denylimit.Limiter
	// warnings bounds the warnings that the keys of an issuer could not be
	// read to one warning for each issuer in each minute: a failing issuer
	// must not fill the log.
	warnings *denylimit.Limiter
	log      *slog.Logger
	now      func() time.Time
}

// NewExchanger returns an Exchanger, or trustrule.ErrMissingDependency.
func NewExchanger(cfg ExchangeConfig) (*Exchanger, error) {
	switch {
	case cfg.Rules == nil, cfg.Exchanges == nil, cfg.Namespaces == nil, cfg.Principals == nil, cfg.Tokens == nil,
		cfg.Audit == nil, cfg.ServerAudit == nil:
		return nil, fmt.Errorf("%w: the exchange needs rules, exchanges, namespaces, principals, tokens, audit "+
			"and server audit", trustrule.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	own, err := NewService(Config{
		Store: cfg.Rules, Namespaces: cfg.Namespaces, Principals: cfg.Principals, Authorizer: denyAll{},
		Reach:            nil,
		InstallationURLs: cfg.InstallationURLs, OwnIssuer: cfg.OwnIssuer, CheckURL: nil, Now: now,
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

	// The warning limiter takes the clock of the exchange, so the window of
	// an issuer follows the configured clock.
	warnings := denylimit.New(denylimit.Config{Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: now})

	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Exchanger{cfg: cfg, own: own, refusals: refusals, warnings: warnings, log: log, now: now}, nil
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

	// Defense in depth: the handler refuses a rule field that is not a rule
	// ID. This check stays so that no other caller puts request text in the
	// audit log or in a query.
	if in.Rule != "" {
		if ruleErr := in.Rule.Validate(); ruleErr != nil {
			return ExchangeResult{}, x.refuse(ctx, ns, in, nil, fmt.Errorf("%w: the rule field: %w",
				trustrule.ErrToken, ruleErr))
		}
	}

	m, err := x.match(ctx, in)
	if err != nil {
		return ExchangeResult{}, x.refuse(ctx, ns, in, m.signed, err)
	}

	rule, tok, signed := m.rule, m.tok, m.signed

	owner, err := x.checkGrant(ctx, &rule)
	if err != nil {
		return ExchangeResult{}, x.refuse(ctx, ns, in, signed, err)
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
			return ExchangeResult{}, x.refuse(ctx, ns, in, signed, err)
		}

		return ExchangeResult{}, fmt.Errorf("record the exchange: %w", err)
	}

	return ExchangeResult{Token: issued.Token, Secret: issued.Secret, Rule: rule}, nil
}

// signedBy names a token whose signature the keys of a rule verified: the
// rule and the subject of the token. Only the holder of a key of the issuer
// can make such a token, so its refusal has its own limiter key, and junk
// tokens cannot hide it. Nil means that no rule verified the signature.
type signedBy struct {
	rule    trustrule.ID
	subject string
}

// matched is the outcome of match: the one rule that accepts a token, with
// the checked token. signed is set when a rule verified the signature of the
// token, also when match returns an error.
type matched struct {
	signed *signedBy
	tok    trustrule.Token
	rule   trustrule.Rule
}

// match returns the one rule that accepts the token of in.
func (x *Exchanger) match(ctx context.Context, in ExchangeInput) (matched, error) {
	var none matched

	issuer, err := trustrule.UnverifiedIssuer(in.Token)
	if err != nil {
		return none, err
	}

	h, err := trustrule.Parse(in.Token)
	if err != nil {
		return none, err
	}

	rules, err := x.cfg.Rules.List(ctx, in.Namespace)
	if err != nil {
		return none, fmt.Errorf("list trust rules: %w", err)
	}

	return pick(x.candidates(ctx, rules, in, issuer, h.Kid))
}

// candidate is a rule of the issuer of a token, with the outcome of its
// checks. verified is true when the keys of the rule verified the signature
// of the token; tok is then set, also when a later check failed.
type candidate struct {
	err      error
	tok      trustrule.Token
	rule     trustrule.Rule
	verified bool
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

		tok, verified, err := x.check(ctx, r, in.Token, kid)
		out = append(out, candidate{err: err, tok: tok, rule: *r, verified: verified})
	}

	return out
}

// pick returns the one candidate that passed its checks. No candidate gives
// trustrule.ErrToken with the reason of each rule, and more than one gives
// ErrAmbiguous. signed names the first candidate whose keys verified the
// signature, also with an error.
func pick(cs []candidate) (matched, error) {
	var (
		passed  []candidate
		reasons []string
		signed  *signedBy
	)

	for i := range cs {
		if cs[i].verified && signed == nil {
			signed = &signedBy{rule: cs[i].rule.ID, subject: cs[i].tok.Subject()}
		}

		if cs[i].err != nil {
			reasons = append(reasons, string(cs[i].rule.ID)+": "+cs[i].err.Error())

			continue
		}

		passed = append(passed, cs[i])
	}

	var failed matched

	failed.signed = signed

	switch {
	case len(passed) == 1:
		return matched{signed: signed, tok: passed[0].tok, rule: passed[0].rule}, nil
	case len(passed) > 1:
		return failed, ErrAmbiguous
	case len(reasons) == 0:
		return failed, fmt.Errorf("%w: no rule of the issuer", trustrule.ErrToken)
	default:
		return failed, fmt.Errorf("%w: %s", trustrule.ErrToken, strings.Join(reasons, "; "))
	}
}

// check runs the checks of the rule r on the token raw. verified is true
// when the keys of r verified the signature; tok is then set, also with an
// error of a later check.
func (x *Exchanger) check(ctx context.Context, r *trustrule.Rule, raw, kid string) (trustrule.Token, bool, error) {
	keys, err := x.keysOf(ctx, r, kid)
	if err != nil {
		return trustrule.Token{}, false, err
	}

	tok, err := trustrule.Verify(raw, keys)
	if err != nil {
		return trustrule.Token{}, false, err
	}

	if err := tok.CheckIssuer(r.Issuer); err != nil {
		return tok, true, err
	}

	if err := tok.CheckAudience(r.Audience); err != nil {
		return tok, true, err
	}

	if err := tok.CheckTime(x.now()); err != nil {
		return tok, true, err
	}

	if !r.Matches(&tok) {
		return tok, true, fmt.Errorf("%w: a condition of the rule does not match", trustrule.ErrToken)
	}

	return tok, true, nil
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
			x.warnKeys(ctx, r, err)

			return trustrule.KeySet{}, fmt.Errorf("read the keys of the issuer: %w", err)
		}

		return keys, nil
	default:
		return trustrule.KeySet{}, fmt.Errorf("%w: key source %q", trustrule.ErrKeysUnavailable, r.Keys)
	}
}

// warnKeys writes the warning that the keys of the issuer of r could not be
// read. The warning is rate-limited for each issuer: at most one warning for
// each issuer in each minute. A failing issuer must not fill the log.
func (x *Exchanger) warnKeys(ctx context.Context, r *trustrule.Rule, err error) {
	if !x.warnings.AllowWindow(keysWarnKey(r.Issuer), time.Minute).Write {
		return
	}

	x.log.WarnContext(ctx, "trust rule keys could not be read",
		slog.String("namespace", r.Namespace.String()),
		slog.String("rule", string(r.ID)),
		slog.String("error", err.Error()))
}

// keysWarnKey is the limiter key of the warnings that the keys of an issuer
// could not be read: one key for each issuer, so one failing issuer writes at
// most one warning in each minute.
func keysWarnKey(issuer string) denylimit.Key {
	return denylimit.Key{Principal: issuer, Action: ActionExchange, Kind: "trustrules:keys", Scope: issuer}
}

// checkGrant checks the issuer and the principal of the matched rule r again,
// at the time of the exchange, and returns the principal.
func (x *Exchanger) checkGrant(ctx context.Context, r *trustrule.Rule) (principal.Principal, error) {
	var none principal.Principal

	// Defense in depth: the create of the rule refused an issuer of this
	// installation. This check stays so that a rule of a restore, of an
	// earlier setting of the public URL, or of a namespace that set its
	// issuerURL after the rule existed never turns a token of this
	// installation into an access token.
	if err := x.own.CheckOwnIssuer(ctx, r.Issuer); err != nil {
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
	case p.Kind != principal.KindService:
		return none, fmt.Errorf("%w: principal %q is a %s, not a service principal", trustrule.ErrPrincipal,
			p.ID, p.Kind)
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
// the window before it held back.
//
// The refusals of a token that no rule verified share one limiter key for
// each namespace. A refusal of a token whose signature a rule verified
// (signed is not nil) has a key of its own for each rule and subject, so
// junk tokens never hide it. A failed write of the entry gives the window
// back to the limiter and is joined to the answer, so the caller fails
// closed.
func (x *Exchanger) refuse(
	ctx context.Context, ns namespace.Namespace, in ExchangeInput, signed *signedBy, cause error,
) error {
	answer := ErrNoMatch
	if errors.Is(cause, ErrAmbiguous) {
		answer = ErrAmbiguous
	}

	key, objectID, reason := unsignedKey(ns.Name), in.Rule, cause.Error()
	if signed != nil {
		key, objectID = signedKey(ns.Name, signed), signed.rule
		reason = "a token signed for the subject " + strconv.Quote(signed.subject) + ": " + reason
	}

	// Defense in depth: the handler and Exchange refuse a rule field that is
	// not a rule ID, and a signed refusal names a stored rule. This check
	// stays so that no request text reaches the object of an entry.
	if objectID.Validate() != nil {
		objectID = ""
	}

	d := x.refusals.Allow(key)

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
	e.ObjectID = string(objectID)
	e.Kind = audit.KindSecurity
	e.Outcome = audit.OutcomeDenied
	e.PrincipalKind = ""
	e.Changes = nil
	e.Reason = clean(reason, audit.MaxReasonLen)

	// The count of the overflow of the limiter holds the refusals of every
	// namespace, so it goes to the log of the installation, and the entry of
	// this namespace carries no count.
	var overflowErr error
	if d.Overflow {
		overflowErr = x.serverEntry(ctx, in.Request, fmt.Sprintf("the limiter of refused exchanges is full: the "+
			"refusals of new keys share one entry in each minute; %d refused exchanges of any namespace before "+
			"this one wrote no entry", d.Suppressed))
	} else if d.Suppressed > 0 {
		e.Reason = clean(fmt.Sprintf("%d refused exchanges before this one wrote no entry; %s", d.Suppressed,
			reason), audit.MaxReasonLen)
	}

	if _, err := x.cfg.Audit.Append(ctx, ns.Fence(), e); err != nil || overflowErr != nil {
		x.refusals.Release(key, d)

		if err != nil {
			err = fmt.Errorf("write the audit entry of a refused exchange: %w", err)
		}

		return errors.Join(answer, flushErr, overflowErr, err)
	}

	if flushErr != nil {
		return errors.Join(answer, flushErr)
	}

	return answer
}

// unsignedKey is the limiter key of the refusals in ns of tokens that no rule
// verified. The key names the namespace twice: as the principal, so the bound
// of keys of one principal never joins two namespaces, and as the scope,
// which a flush reads.
func unsignedKey(ns namespace.Name) denylimit.Key {
	return denylimit.Key{
		Principal: refusedSubject + ":" + ns.String(), Action: ActionExchange, Kind: "trustrules:exchange",
		Scope: ns.String(),
	}
}

// signedKey is the limiter key of the refusals in ns of tokens that the rule
// of s verified, for the subject of s. Its principal is not the principal of
// unsignedKey, so the bound of keys of each principal of the limiter keeps
// the two apart. The subject goes in as a digest, so the key has a fixed
// length.
func signedKey(ns namespace.Name, s *signedBy) denylimit.Key {
	sum := sha256.Sum256([]byte(s.subject))

	return denylimit.Key{
		Principal: refusedSubject + "-signed:" + ns.String(), Action: ActionExchange,
		Kind:  "trustrules:exchange:" + string(s.rule) + ":" + hex.EncodeToString(sum[:16]),
		Scope: ns.String(),
	}
}

// flush writes one entry for each count of refusals that the limiter held
// back in an earlier window, in the namespace of the count. The aggregate of
// a limiter principal goes to the namespace that the principal names. The
// overflow of the whole limiter (more than denylimit.DefaultMaxKeys keys)
// names no namespace: its count goes to the log of the installation.
func (x *Exchanger) flush(ctx context.Context, request audit.Entry, pending []denylimit.Pending) error {
	var errs []error

	for _, p := range pending {
		scope := p.Key.Scope
		if scope == denylimit.AggregateMark {
			// The aggregate of a limiter principal has no scope of its own,
			// but each limiter principal of the exchange names one namespace.
			scope = namespaceOfLimiterPrincipal(p.Key.Principal)
		}

		if p.Overflow && p.Suppressed > 0 {
			errs = append(errs, x.serverEntry(ctx, request, fmt.Sprintf("%d refused exchanges of any namespace "+
				"wrote no entry of their own: the limiter of refused exchanges was full", p.Suppressed)))

			continue
		}

		if p.Overflow || scope == "" || p.Suppressed == 0 {
			continue
		}

		// Defense in depth: the keys of the exchange hold only names of
		// namespaces that exist. This check stays so that a key of another
		// form never reaches the read of a namespace.
		name, err := namespace.ParseName(scope)
		if err != nil {
			errs = append(errs, fmt.Errorf("the count of refused exchanges names no namespace: %w", err))

			continue
		}

		ns, err := x.cfg.Namespaces.Get(ctx, name)
		if err != nil {
			errs = append(errs, fmt.Errorf("read namespace %s: %w", name, err))

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

// serverEntry writes an entry of refused exchanges with the given reason in
// the log of the installation.
func (x *Exchanger) serverEntry(ctx context.Context, request audit.Entry, reason string) error {
	e := request
	e.At = x.now().UTC()
	e.Namespace = audit.ServerScope
	e.Subject = refusedSubject
	e.TokenID = ""
	e.Action = ActionExchange
	e.Object = identity.ServerObject().String()
	e.ObjectID = ""
	e.Kind = audit.KindSecurity
	e.Outcome = audit.OutcomeDenied
	e.PrincipalKind = ""
	e.Changes = nil
	e.Reason = clean(reason, audit.MaxReasonLen)

	if _, err := x.cfg.ServerAudit.AppendServer(ctx, e); err != nil {
		return fmt.Errorf("write the count of refused exchanges in the log of the installation: %w", err)
	}

	return nil
}

// namespaceOfLimiterPrincipal returns the namespace of a limiter principal
// of unsignedKey or signedKey, or the empty string for another principal.
func namespaceOfLimiterPrincipal(p string) string {
	for _, prefix := range []string{refusedSubject + "-signed:", refusedSubject + ":"} {
		if ns, ok := strings.CutPrefix(p, prefix); ok {
			return ns
		}
	}

	return ""
}

// refusedSubject is the subject of the audit entry of a refused exchange: no
// principal acted. It starts with '@', which no subject of a principal holds.
const refusedSubject = "@workload"

// maxClaimRunes bounds the text of one claim in an audit entry, in
// characters. A reason is bounded by audit.MaxReasonLen.
const maxClaimRunes = 256

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
		{Key: "subject", Old: nil, New: clean(tok.Subject(), maxClaimRunes), Layer: "", TakesEffect: ""},
		{Key: "jti", Old: nil, New: clean(tok.ID(), maxClaimRunes), Layer: "", TakesEffect: ""},
		{Key: "expiresAt", Old: nil, New: t.ExpiresAt.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	return e
}

// clean returns s with each character that the audit log refuses (see
// audit.IsHiddenRune) replaced by its escape, cut to at most n characters, so
// a claim never adds a line to a log and a reason never passes the limit of
// the audit log. Bytes that are not UTF-8 become U+FFFD.
func clean(s string, n int) string {
	var (
		b     strings.Builder
		count int
	)

	for _, c := range s {
		piece := string(c)
		if audit.IsHiddenRune(c) {
			piece = strconv.QuoteRune(c)
		}

		size := utf8.RuneCountInString(piece)
		if count+size > n {
			break
		}

		b.WriteString(piece)

		count += size
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
