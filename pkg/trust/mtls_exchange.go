// Package trust verifies the client certificate of an mTLS workload exchange
// and issues a stored trust token for the matched rule.
//
// A workload presents a TLS client certificate over direct TLS. The server
// checks the chain against the trust bundle of the namespace, checks the time
// with no skew, checks revocation, and matches the SPIFFE ID in the URI SAN
// exactly against the SPIFFE ID of one rule of the namespace. A trust rule has
// no disabled state: a stored rule is an enabled rule. On a match it issues a
// stored access token of the kind mtls-exchange with the exchanged-token
// lifetime capped at the leaf not-after time, and records the exchange in one
// transaction. The store matches the rule again in that transaction.
package trust

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Sentinel errors of the mTLS exchange. Each refusal gives no token.
var (
	// ErrNoCertificate reports a call with no verified client certificate.
	ErrNoCertificate = errors.New("no verified client certificate")
	// ErrUnknownIssuer reports a chain that does not lead to the bundle.
	ErrUnknownIssuer = errors.New("the chain does not lead to the bundle")
	// ErrExpired reports a leaf past its not-after time.
	ErrExpired = errors.New("the leaf certificate expired")
	// ErrNotYetValid reports a leaf before its not-before time.
	ErrNotYetValid = errors.New("the leaf certificate is not yet valid")
	// ErrRevoked reports a revoked leaf.
	ErrRevoked = errors.New("the leaf certificate is revoked")
	// ErrRevocationUnknown reports a revocation check with no answer.
	ErrRevocationUnknown = errors.New("the revocation check has no answer")
	// ErrNoMatchingRule reports a SPIFFE ID that no rule names.
	ErrNoMatchingRule = errors.New("no rule names the SPIFFE ID")
	// ErrAmbiguousRule reports a SPIFFE ID that two rules name.
	ErrAmbiguousRule = errors.New("two rules name the SPIFFE ID")
	// ErrBundleUnknown reports a bundle that is not the active bundle of the namespace.
	ErrBundleUnknown = errors.New("the trust bundle is not an active bundle of the namespace")
	// ErrInvalid reports input outside the bounds.
	ErrInvalid = errors.New("invalid mTLS exchange input")
	// ErrIssueFailed reports a store failure with no token and no entry.
	ErrIssueFailed = errors.New("the token issue failed")
	// ErrMissingDependency reports a Config without a required port.
	ErrMissingDependency = errors.New("missing dependency")
)

// Bounds of the verified chain.
const (
	// MaxChainBytes caps the DER bytes of a chain.
	MaxChainBytes = 32 << 10
	// MaxChainDepth caps the certificates of a chain.
	MaxChainDepth = 8
)

// ActionExchange is the audit action of an mTLS exchange.
const ActionExchange = "trust-rule:exchange"

// RefusedSubject is the subject of the denial entry of a call with no
// verified SPIFFE ID: no principal acted. It starts with '@', which no
// subject of a principal holds.
const RefusedSubject = "@workload"

// VerifiedChain is the checked client chain. Only VerifyChain makes a
// VerifiedChain with a leaf, so a value with a leaf passed every check of
// VerifyChain. The zero value has no leaf, and Exchange refuses it.
type VerifiedChain struct {
	leaf       *x509.Certificate
	spiffeID   string
	verifiedAt time.Time
}

// Leaf returns the parsed leaf certificate, or nil for the zero value.
func (c VerifiedChain) Leaf() *x509.Certificate { return c.leaf }

// SPIFFEID returns the single SPIFFE URI of the leaf.
func (c VerifiedChain) SPIFFEID() string { return c.spiffeID }

// VerifiedAt returns the time of the check.
func (c VerifiedChain) VerifiedAt() time.Time { return c.verifiedAt }

// VerifyChain checks rawChain against roots at now, with no clock skew. check
// reports revocation of the leaf: nil means good, ErrRevoked means revoked,
// any other error means unknown. A nil check gives ErrRevocationUnknown.
// VerifyChain writes no audit entry: Exchanger.Verify calls it and writes the
// denial entry of each refusal.
func VerifyChain(
	rawChain [][]byte, roots *x509.CertPool, now time.Time, check func(*x509.Certificate) error,
) (VerifiedChain, error) {
	if len(rawChain) == 0 {
		return VerifiedChain{}, ErrNoCertificate
	}

	if roots == nil {
		return VerifiedChain{}, fmt.Errorf("%w: no trust bundle", ErrMissingDependency)
	}

	if boundErr := checkBounds(rawChain); boundErr != nil {
		return VerifiedChain{}, boundErr
	}

	certs, parseErr := parseChain(rawChain)
	if parseErr != nil {
		return VerifiedChain{}, parseErr
	}

	leaf := certs[0]

	if leafErr := checkLeafTime(leaf, now); leafErr != nil {
		return VerifiedChain{}, leafErr
	}

	if verifyErr := verifyAgainstRoots(leaf, certs[1:], roots, now); verifyErr != nil {
		return VerifiedChain{}, verifyErr
	}

	if revokeErr := checkRevocation(leaf, check); revokeErr != nil {
		return VerifiedChain{}, revokeErr
	}

	spiffeID, spiffeErr := singleSPIFFEID(leaf)
	if spiffeErr != nil {
		return VerifiedChain{}, spiffeErr
	}

	return VerifiedChain{leaf: leaf, spiffeID: spiffeID, verifiedAt: now}, nil
}

// checkBounds returns ErrInvalid when the chain is oversize or too deep.
func checkBounds(rawChain [][]byte) error {
	total := 0

	for _, b := range rawChain {
		total += len(b)
	}

	if total > MaxChainBytes {
		return fmt.Errorf("%w: the chain holds more than %d bytes", ErrInvalid, MaxChainBytes)
	}

	if len(rawChain) > MaxChainDepth {
		return fmt.Errorf("%w: the chain holds more than %d certificates", ErrInvalid, MaxChainDepth)
	}

	return nil
}

// parseChain parses each DER block of rawChain.
func parseChain(rawChain [][]byte) ([]*x509.Certificate, error) {
	out := make([]*x509.Certificate, 0, len(rawChain))

	for i, b := range rawChain {
		cert, parseErr := x509.ParseCertificate(b)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: certificate %d does not parse: %w", ErrInvalid, i, parseErr)
		}

		out = append(out, cert)
	}

	return out, nil
}

// checkLeafTime returns ErrExpired or ErrNotYetValid with no skew.
func checkLeafTime(leaf *x509.Certificate, now time.Time) error {
	switch {
	case now.After(leaf.NotAfter):
		return fmt.Errorf("%w at %s", ErrExpired, leaf.NotAfter.UTC().Format(time.RFC3339))
	case now.Before(leaf.NotBefore):
		return fmt.Errorf("%w before %s", ErrNotYetValid, leaf.NotBefore.UTC().Format(time.RFC3339))
	default:
		return nil
	}
}

// verifyAgainstRoots verifies leaf with intermediates against roots at now.
func verifyAgainstRoots(
	leaf *x509.Certificate, rest []*x509.Certificate, roots *x509.CertPool, now time.Time,
) error {
	intermediates := x509.NewCertPool()
	for _, c := range rest {
		intermediates.AddCert(c)
	}

	_, verifyErr := leaf.Verify(x509.VerifyOptions{
		DNSName: "", Intermediates: intermediates, Roots: roots, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, MaxConstraintComparisions: 0,
		CertificatePolicies: nil,
	})
	if verifyErr == nil {
		return nil
	}

	if _, ok := errors.AsType[x509.UnknownAuthorityError](verifyErr); ok {
		return fmt.Errorf("%w: %w", ErrUnknownIssuer, verifyErr)
	}

	if invalid, ok := errors.AsType[x509.CertificateInvalidError](verifyErr); ok &&
		invalid.Reason == x509.Expired {
		return fmt.Errorf("%w: %w", ErrExpired, verifyErr)
	}

	return fmt.Errorf("%w: %w", ErrUnknownIssuer, verifyErr)
}

// checkRevocation runs check on leaf. A nil check gives ErrRevocationUnknown.
func checkRevocation(leaf *x509.Certificate, check func(*x509.Certificate) error) error {
	if check == nil {
		return ErrRevocationUnknown
	}

	if checkErr := check(leaf); checkErr != nil {
		if errors.Is(checkErr, ErrRevoked) {
			return fmt.Errorf("%w: %w", ErrRevoked, checkErr)
		}

		return fmt.Errorf("%w: %w", ErrRevocationUnknown, checkErr)
	}

	return nil
}

// singleSPIFFEID returns the single SPIFFE URI of leaf.
func singleSPIFFEID(leaf *x509.Certificate) (string, error) {
	found := make([]string, 0, 1)

	for _, u := range leaf.URIs {
		if u == nil || u.Scheme != "spiffe" {
			continue
		}

		found = append(found, u.String())
	}

	if len(found) != 1 {
		return "", fmt.Errorf("%w: the leaf holds %d SPIFFE IDs, want one", ErrInvalid, len(found))
	}

	id := found[0]
	if strings.Contains(id, "*") {
		return "", fmt.Errorf("%w: the SPIFFE ID holds *", ErrInvalid)
	}

	return id, nil
}

// MatchRule returns the one rule of rules in ns whose SPIFFE ID pattern is
// exactly spiffeID and whose bundle is bundleID. A pattern with a "*" is not
// a wildcard: it matches no SPIFFE ID, because a verified SPIFFE ID never
// holds "*". Rules without a bundle or a SPIFFE pattern never match.
func MatchRule(rules []trustrule.Rule, ns namespace.Name, spiffeID, bundleID string) (trustrule.Rule, error) {
	if spiffeID == "" || strings.Contains(spiffeID, "*") {
		return trustrule.Rule{}, fmt.Errorf("%w: missing or bad SPIFFE ID", ErrInvalid)
	}

	if bundleID == "" {
		return trustrule.Rule{}, fmt.Errorf("%w: no trust bundle", ErrNoMatchingRule)
	}

	matched := make([]trustrule.Rule, 0, 1)

	for i := range rules {
		if names(&rules[i], ns, spiffeID, bundleID) {
			matched = append(matched, rules[i])
		}
	}

	switch len(matched) {
	case 0:
		return trustrule.Rule{}, ErrNoMatchingRule
	case 1:
		return matched[0], nil
	default:
		return trustrule.Rule{}, ErrAmbiguousRule
	}
}

// names reports whether r is a rule of ns in bundleID whose SPIFFE ID
// pattern is exactly spiffeID.
func names(r *trustrule.Rule, ns namespace.Name, spiffeID, bundleID string) bool {
	// Defense in depth: the caller reads the rules of ns only, and the store
	// checks the rule against the namespace of the fence. This check stays so
	// that a rule of another namespace in the list never matches, whatever
	// the caller read.
	if r.Namespace != ns {
		return false
	}

	return r.Bundle != "" && r.Bundle == bundleID && r.SpiffeIDPattern != "" && r.SpiffeIDPattern == spiffeID
}

// PrincipalReader reads the principal of a matched rule.
type PrincipalReader interface {
	// GetPrincipal returns the principal id, or principal.ErrNotFound.
	GetPrincipal(ctx context.Context, id string) (principal.Principal, error)
}

// TokenIssuer makes the stored token of an mTLS exchange.
type TokenIssuer interface {
	// NewMtlsTrustToken returns a new token of the kind mtls-exchange.
	NewMtlsTrustToken(ctx context.Context, in accesstoken.MtlsInput) (accesstoken.Issued, error)
}

// BundleReader reads the trust bundle of a namespace.
type BundleReader interface {
	// GetBundle returns the bundle of ns, or trustrule.ErrNotFound.
	GetBundle(ctx context.Context, ns namespace.Name) (trustrule.Bundle, error)
}

// DenialRecorder appends the audit entry of a refused exchange.
type DenialRecorder interface {
	// Append stores e in the audit log of the namespace of fence.
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// Config holds the dependencies of an Exchanger. Every field except Now is
// required. A nil Now means time.Now.
type Config struct {
	Principals PrincipalReader
	Tokens     TokenIssuer
	Exchanges  trustrule.ExchangeStore
	Bundles    BundleReader
	Audit      DenialRecorder
	Now        func() time.Time
}

// Exchanger matches a verified chain to one rule and issues a stored token
// with its audit entry in one transaction.
type Exchanger struct {
	principals PrincipalReader
	tokens     TokenIssuer
	exchanges  trustrule.ExchangeStore
	bundles    BundleReader
	audit      DenialRecorder
	now        func() time.Time
}

// NewExchanger returns an Exchanger, or ErrMissingDependency.
func NewExchanger(cfg Config) (*Exchanger, error) {
	if cfg.Principals == nil || cfg.Tokens == nil || cfg.Exchanges == nil || cfg.Bundles == nil ||
		cfg.Audit == nil {
		return nil, fmt.Errorf("%w: the mTLS exchange needs principals, tokens, exchanges, bundles and an audit log",
			ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Exchanger{
		principals: cfg.Principals, tokens: cfg.Tokens, exchanges: cfg.Exchanges, bundles: cfg.Bundles,
		audit: cfg.Audit, now: now,
	}, nil
}

// Call names one exchange call for the audit entry of its denial: the
// namespace of the route, the fence of that namespace, and the entry with the
// request fields.
type Call struct {
	Namespace namespace.Name
	Fence     namespace.Fence
	Entry     audit.Entry
}

// VerifyInput is the client chain of one call as the TLS session gives it.
// Check is the revocation check of VerifyChain.
type VerifyInput struct {
	Call     Call
	RawChain [][]byte
	Roots    *x509.CertPool
	Check    func(*x509.Certificate) error
}

// Verify is VerifyChain at the time of the clock of e. Each refusal writes a
// denial entry to the audit log of the namespace of the call. A failed write
// of that entry joins the error of the refusal.
func (e *Exchanger) Verify(ctx context.Context, in *VerifyInput) (VerifiedChain, error) {
	chain, err := VerifyChain(in.RawChain, in.Roots, e.now(), in.Check)
	if err == nil {
		return chain, nil
	}

	return VerifiedChain{}, e.deny(ctx, &in.Call, "", err)
}

// Admit runs g.Check on in. Each refusal writes a denial entry to the audit
// log of the namespace of the call. A failed write of that entry joins the
// error of the refusal.
func (e *Exchanger) Admit(ctx context.Context, g *Guard, call *Call, in CheckInput) error {
	checkErr := g.Check(in)
	if checkErr == nil {
		return nil
	}

	return e.deny(ctx, call, "", checkErr)
}

// ExchangeInput is one mTLS exchange.
type ExchangeInput struct {
	Namespace namespace.Name
	Chain     VerifiedChain
	Rules     []trustrule.Rule
	Bundle    string
	Fence     namespace.Fence
	Entry     audit.Entry
}

// ExchangeResult is the stored token and the matched rule.
type ExchangeResult struct {
	Token  accesstoken.Token
	Secret accesstoken.Secret
	Rule   trustrule.Rule
}

// Exchange issues a stored token of the kind mtls-exchange for the principal
// of the one matched rule. The store matches the rule again in the
// transaction of RecordExchange. Any refusal gives no token. A store failure
// gives ErrIssueFailed with no token and no entry.
//
// Every refusal and every failure writes a denial entry to the audit log of
// the namespace. A failed write of that entry joins the error of the refusal.
func (e *Exchanger) Exchange(ctx context.Context, in *ExchangeInput) (ExchangeResult, error) {
	res, err := e.issueFor(ctx, in)
	if err == nil {
		return res, nil
	}

	call := Call{Namespace: in.Namespace, Fence: in.Fence, Entry: in.Entry}

	return ExchangeResult{}, e.deny(ctx, &call, in.Chain.spiffeID, err)
}

// deny appends the denial entry of cause and returns cause, joined with the
// error of the append when the append fails.
func (e *Exchanger) deny(ctx context.Context, call *Call, spiffeID string, cause error) error {
	if appendErr := e.recordDenial(ctx, call, spiffeID, cause); appendErr != nil {
		return fmt.Errorf("%w: the denial entry is not stored: %w", cause, appendErr)
	}

	return cause
}

// recordDenial appends the denial entry of cause to the audit log. An empty
// spiffeID gives the subject RefusedSubject.
func (e *Exchanger) recordDenial(ctx context.Context, call *Call, spiffeID string, cause error) error {
	subject := spiffeID
	if subject == "" {
		subject = RefusedSubject
	}

	entry := call.Entry
	entry.At = e.now()
	entry.Namespace = call.Namespace
	entry.Subject = subject
	entry.Action = ActionExchange
	entry.Object = identity.NamespaceObject(call.Namespace).String()
	entry.Kind = audit.KindSecurity
	entry.Outcome = audit.OutcomeDenied
	entry.Changes = []audit.Change{{Key: "reason", Old: nil, New: denialReason(cause), Layer: "", TakesEffect: ""}}

	if spiffeID != "" {
		entry.Changes = append(entry.Changes, audit.Change{
			Key: "spiffeID", Old: nil, New: spiffeID, Layer: "", TakesEffect: "",
		})
	}

	if _, appendErr := e.audit.Append(ctx, call.Fence, entry); appendErr != nil {
		return fmt.Errorf("append the denial entry: %w", appendErr)
	}

	return nil
}

// denialReason returns the fixed text of the sentinel that err wraps.
func denialReason(err error) string {
	for _, s := range []error{
		ErrNoCertificate, ErrUnknownIssuer, ErrExpired, ErrNotYetValid, ErrRevoked, ErrRevocationUnknown,
		ErrNoMatchingRule, ErrAmbiguousRule, ErrBundleUnknown, trustrule.ErrPrincipal, ErrRateLimited,
		ErrInvalid, ErrIssueFailed, ErrMissingDependency,
	} {
		if errors.Is(err, s) {
			return s.Error()
		}
	}

	return "the exchange failed"
}

// issueFor is Exchange without the denial entry.
func (e *Exchanger) issueFor(ctx context.Context, in *ExchangeInput) (ExchangeResult, error) {
	rule, owner, authErr := e.authorize(ctx, in)
	if authErr != nil {
		return ExchangeResult{}, authErr
	}

	issued, issueErr := e.tokens.NewMtlsTrustToken(ctx, accesstoken.MtlsInput{
		At: in.Chain.verifiedAt, LeafNotAfter: in.Chain.leaf.NotAfter, PrincipalID: rule.PrincipalID,
		Name: "trust rule " + string(rule.ID), CreatedBy: trustrule.Ref(rule.ID),
	})
	if issueErr != nil {
		return ExchangeResult{}, fmt.Errorf("%w: %w", ErrIssueFailed, issueErr)
	}

	entry := buildEntry(&entryParams{
		Template: in.Entry, At: in.Chain.verifiedAt, NS: in.Namespace, Rule: &rule, Tok: &issued.Token,
		Owner: owner, SPIFFEID: in.Chain.spiffeID,
	})

	xch := trustrule.Exchange{
		Rule: rule.ID, Token: issued.Token, Digest: issued.Digest, SPIFFEID: in.Chain.spiffeID,
	}
	if recordErr := e.exchanges.RecordExchange(ctx, xch, in.Fence, entry); recordErr != nil {
		return ExchangeResult{}, fmt.Errorf("%w: %w", ErrIssueFailed, recordErr)
	}

	return ExchangeResult{Token: issued.Token, Secret: issued.Secret, Rule: rule}, nil
}

// authorize checks the chain, the bundle, the one rule and its principal.
func (e *Exchanger) authorize(ctx context.Context, in *ExchangeInput) (trustrule.Rule, principal.Principal, error) {
	// Defense in depth: only VerifyChain makes a chain with a leaf, and the
	// route calls Verify before Exchange. This check stays so that a zero
	// VerifiedChain from any caller gives no token. It does not rely on the
	// TLS layer or on the route.
	if in.Chain.leaf == nil || in.Chain.spiffeID == "" {
		return trustrule.Rule{}, principal.Principal{}, ErrNoCertificate
	}

	// Defense in depth: the store compares the fence with the namespace of
	// the entry. This check stays so that the match never runs for one
	// namespace under the fence of another.
	if in.Fence.Namespace != in.Namespace {
		return trustrule.Rule{}, principal.Principal{}, fmt.Errorf("%w: the fence names %q, the call names %q",
			ErrInvalid, in.Fence.Namespace, in.Namespace)
	}

	if bundleErr := e.checkBundle(ctx, in); bundleErr != nil {
		return trustrule.Rule{}, principal.Principal{}, bundleErr
	}

	rule, matchErr := MatchRule(in.Rules, in.Namespace, in.Chain.spiffeID, in.Bundle)
	if matchErr != nil {
		return trustrule.Rule{}, principal.Principal{}, matchErr
	}

	owner, ownerErr := e.checkPrincipal(ctx, &rule, in.Namespace)
	if ownerErr != nil {
		return trustrule.Rule{}, principal.Principal{}, ownerErr
	}

	return rule, owner, nil
}

// checkBundle returns ErrBundleUnknown unless in.Bundle is the bundle of the
// namespace and it has an active version. A trust bundle has no separate
// disabled state: a bundle with no active version is the disabled state.
func (e *Exchanger) checkBundle(ctx context.Context, in *ExchangeInput) error {
	b, readErr := e.bundles.GetBundle(ctx, in.Namespace)
	if readErr != nil {
		return fmt.Errorf("%w: %w", ErrBundleUnknown, readErr)
	}

	// Defense in depth: the bundle reader reads by namespace, and the route
	// takes the bundle ID from the bundle that it read. This check stays so
	// that an empty bundle ID, or a bundle of another namespace from a bad
	// reader, gives no token. It does not rely on the reader or the route.
	if in.Bundle == "" || string(b.ID) != in.Bundle || b.Namespace != in.Namespace {
		return fmt.Errorf("%w: %q is not the bundle of the namespace", ErrBundleUnknown, in.Bundle)
	}

	for i := range b.Versions {
		if b.Versions[i].Number == b.Active {
			return nil
		}
	}

	return fmt.Errorf("%w: %q has no active version", ErrBundleUnknown, in.Bundle)
}

// checkPrincipal returns the service principal of rule in ns.
func (e *Exchanger) checkPrincipal(
	ctx context.Context, rule *trustrule.Rule, ns namespace.Name,
) (principal.Principal, error) {
	p, readErr := e.principals.GetPrincipal(ctx, rule.PrincipalID)
	if readErr != nil {
		return principal.Principal{}, fmt.Errorf("%w: %w", trustrule.ErrPrincipal, readErr)
	}

	switch {
	case p.Kind != principal.KindService:
		return principal.Principal{}, fmt.Errorf("%w: principal %q is a %s, not a service principal",
			trustrule.ErrPrincipal, p.ID, p.Kind)
	case p.Namespace != ns:
		// Defense in depth: RecordExchange checks the namespace of the
		// principal again in its transaction. This check stays so that the
		// service refuses before it makes a token, without the store.
		return principal.Principal{}, fmt.Errorf("%w: principal %q is not a principal of the namespace",
			trustrule.ErrPrincipal, p.ID)
	case !p.DisabledAt.IsZero():
		return principal.Principal{}, fmt.Errorf("%w: principal %q is disabled", trustrule.ErrPrincipal, p.ID)
	default:
		return p, nil
	}
}

// entryParams names the fields of an exchange audit entry.
type entryParams struct {
	Template audit.Entry
	At       time.Time
	NS       namespace.Name
	Rule     *trustrule.Rule
	Tok      *accesstoken.Token
	Owner    principal.Principal
	SPIFFEID string
}

// buildEntry returns the audit entry of the exchange of rule for tok.
func buildEntry(p *entryParams) audit.Entry {
	e := p.Template
	e.At = p.At
	e.Namespace = p.NS
	e.Subject = p.Owner.Subject
	e.TokenID = p.Tok.ID
	e.Action = ActionExchange
	e.Object = identity.NamespaceObject(p.NS).String()
	e.ObjectID = string(p.Rule.ID)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.PrincipalKind = string(p.Owner.Kind)
	e.Changes = []audit.Change{
		{Key: "principal", Old: nil, New: p.Rule.PrincipalID, Layer: "", TakesEffect: ""},
		{Key: "kind", Old: nil, New: string(p.Tok.Kind), Layer: "", TakesEffect: ""},
		{Key: "spiffeID", Old: nil, New: p.SPIFFEID, Layer: "", TakesEffect: ""},
		{Key: "expiresAt", Old: nil, New: p.Tok.ExpiresAt.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	return e
}
