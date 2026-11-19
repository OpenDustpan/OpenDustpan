package trustrulesvc

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// Bounds of the chain that a workload sends.
const (
	// changeKeyPrincipal is the key of the principal in the audit entry. It is the
	// key of the JWT exchange entry too.
	changeKeyPrincipal = "principal"
	// serialBase is the base of the serial number in a revocation error.
	serialBase = 16
	// MaxChainBytes is the largest size of a chain, in bytes of DER, that
	// VerifyChain parses.
	MaxChainBytes = 32768
	// MaxChainDepth is the largest number of certificates in a chain that
	// VerifyChain parses: the leaf, the intermediates and the root.
	MaxChainDepth = 8
)

// Errors of the mTLS exchange. Each one is a refusal: no token exists after
// it.
var (
	// ErrNoCertificate reports a call with no verified client certificate.
	ErrNoCertificate = errors.New("the call has no client certificate")
	// ErrUnknownIssuer reports a chain that does not lead to the active
	// version of the trust bundle of the namespace.
	ErrUnknownIssuer = errors.New("the client certificate does not chain to the trust bundle of the namespace")
	// ErrExpired reports a certificate that is past its not-after time.
	ErrExpired = errors.New("the client certificate is expired")
	// ErrNotYetValid reports a certificate that is before its not-before
	// time.
	ErrNotYetValid = errors.New("the client certificate is not valid yet")
	// ErrRevoked reports a certificate that the revocation source lists.
	ErrRevoked = errors.New("the client certificate is revoked")
	// ErrRevocationUnknown reports that the server could not learn whether
	// a certificate is revoked. The server fails closed.
	ErrRevocationUnknown = errors.New("the revocation status of the client certificate is not known")
	// ErrNoMatchingRule reports that no enabled trust rule names the SPIFFE
	// ID in the bundle of the chain.
	ErrNoMatchingRule = errors.New("no trust rule names the SPIFFE ID of the client certificate")
	// ErrAmbiguousRule reports that more than one trust rule names the
	// SPIFFE ID in the bundle of the chain.
	ErrAmbiguousRule = errors.New("more than one trust rule names the SPIFFE ID of the client certificate")
	// ErrBundleUnknown reports that the namespace has no trust bundle, or
	// that its bundle has no active version.
	ErrBundleUnknown = errors.New("the namespace has no active trust bundle")
	// ErrInvalid reports a chain that is too large, too deep, not parsable,
	// or that has no usable SPIFFE ID.
	ErrInvalid = errors.New("the client certificate chain is not valid")
	// ErrIssueFailed reports that the server could not store the token. No
	// token and no entry exist.
	ErrIssueFailed = errors.New("the server could not issue the token")
	// ErrRateLimited reports a call that the rate limit of the guard
	// refused.
	ErrRateLimited = errors.New("too many exchange calls")
	// ErrBodyTooLarge reports a call whose body is over the cap of the
	// guard.
	ErrBodyTooLarge = errors.New("the request body is too large")
)

// VerifiedChain is a client chain that VerifyChain checked: it leads to a CA
// of a bundle, its leaf is in its validity time, the revocation check passed
// for it, and the leaf names exactly one SPIFFE ID. Its fields are not
// exported, so only VerifyChain and Verify make a usable value. The zero
// value holds no chain, and Exchange refuses it.
type VerifiedChain struct {
	leaf          *x509.Certificate
	spiffeID      string
	leafNotAfter  time.Time
	bundleID      trustrule.BundleID
	bundleVersion int64
}

// SPIFFEID returns the SPIFFE ID of the leaf, or the empty string for the
// zero value.
func (c VerifiedChain) SPIFFEID() string { return c.spiffeID }

// LeafNotAfter returns the not-after time of the leaf.
func (c VerifiedChain) LeafNotAfter() time.Time { return c.leafNotAfter }

// BundleID returns the ID of the bundle that verified the chain. It is empty
// for a value that VerifyChain made and Verify did not complete.
func (c VerifiedChain) BundleID() trustrule.BundleID { return c.bundleID }

// BundleVersion returns the number of the bundle version that verified the
// chain, or 0.
func (c VerifiedChain) BundleVersion() int64 { return c.bundleVersion }

// VerifyChain checks the DER certificates rawChain, the leaf first, against
// roots at the time now, with no clock skew. It refuses a chain over
// MaxChainBytes or MaxChainDepth, a certificate that does not parse, a
// chain that does not lead to roots, a leaf outside its validity time, a
// leaf with no SPIFFE ID or more than one URI, and a certificate for which
// check reports a revocation. check runs for the leaf and each
// intermediate. A nil check gives ErrRevocationUnknown: the function fails
// closed. The value that it returns has no bundle yet.
func VerifyChain(
	rawChain [][]byte, roots *x509.CertPool, now time.Time, check func(*x509.Certificate) error,
) (VerifiedChain, error) {
	certs, err := parseChain(rawChain)
	if err != nil {
		return VerifiedChain{}, err
	}

	leaf := certs[0]

	// Defense in depth: verifyPaths asks x509 for a chain that is valid at
	// now, and x509 refuses an expired leaf too. This check stays so that
	// the time of the leaf does not depend on x509, and so that the answer
	// names expired or not yet valid.
	if timeErr := checkLeafTime(leaf, now); timeErr != nil {
		return VerifiedChain{}, timeErr
	}

	spiffeID, err := leafSPIFFEID(leaf)
	if err != nil {
		return VerifiedChain{}, err
	}

	// Defense in depth: Verify builds the roots from the stored bundle. A
	// nil pool would make x509 use the roots of the operating system, so
	// this check stays so that no caller ever verifies against them.
	if roots == nil {
		return VerifiedChain{}, fmt.Errorf("%w: no roots", ErrBundleUnknown)
	}

	chains, err := verifyPaths(certs, roots, now)
	if err != nil {
		return VerifiedChain{}, err
	}

	if revErr := checkRevocation(chains, check); revErr != nil {
		return VerifiedChain{}, revErr
	}

	return VerifiedChain{
		leaf: leaf, spiffeID: spiffeID, leafNotAfter: leaf.NotAfter.UTC(), bundleID: "", bundleVersion: 0,
	}, nil
}

// parseChain returns the certificates of rawChain, after the checks of size
// and depth that come before any parse.
func parseChain(rawChain [][]byte) ([]*x509.Certificate, error) {
	if len(rawChain) == 0 {
		return nil, fmt.Errorf("%w: the chain is empty", ErrNoCertificate)
	}

	if len(rawChain) > MaxChainDepth {
		return nil, fmt.Errorf("%w: the chain holds %d certificates, at most %d", ErrInvalid, len(rawChain),
			MaxChainDepth)
	}

	total := 0

	for _, der := range rawChain {
		total += len(der)
		if total > MaxChainBytes {
			return nil, fmt.Errorf("%w: the chain is over %d bytes", ErrInvalid, MaxChainBytes)
		}
	}

	certs := make([]*x509.Certificate, 0, len(rawChain))

	for i, der := range rawChain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate %d does not parse", ErrInvalid, i)
		}

		certs = append(certs, cert)
	}

	return certs, nil
}

// checkLeafTime refuses a leaf outside its validity time. It allows no
// clock skew.
func checkLeafTime(leaf *x509.Certificate, now time.Time) error {
	switch {
	case now.Before(leaf.NotBefore):
		return fmt.Errorf("%w: valid from %s", ErrNotYetValid, leaf.NotBefore.UTC().Format(time.RFC3339))
	case now.After(leaf.NotAfter):
		return fmt.Errorf("%w: valid until %s", ErrExpired, leaf.NotAfter.UTC().Format(time.RFC3339))
	default:
		return nil
	}
}

// leafSPIFFEID returns the one SPIFFE ID of the leaf. The leaf must not be a
// CA and must hold exactly one URI SAN, which must be an exact SPIFFE ID.
func leafSPIFFEID(leaf *x509.Certificate) (string, error) {
	if leaf.IsCA {
		return "", fmt.Errorf("%w: the leaf is a CA certificate", ErrInvalid)
	}

	if len(leaf.URIs) != 1 {
		return "", fmt.Errorf("%w: the leaf holds %d URI SANs, need exactly 1", ErrInvalid, len(leaf.URIs))
	}

	id := leaf.URIs[0].String()

	// Defense in depth: ValidateSpiffePattern below refuses a * too. This
	// check stays so that a change to that function cannot let a wildcard
	// SPIFFE ID of a leaf match a rule.
	if strings.Contains(id, "*") {
		return "", fmt.Errorf("%w: the SPIFFE ID holds a *", ErrInvalid)
	}

	if err := trustrule.ValidateSpiffePattern(id); err != nil {
		return "", fmt.Errorf("%w: the URI SAN is not a SPIFFE ID: %w", ErrInvalid, err)
	}

	return id, nil
}

// verifyPaths returns the chains from the leaf certs[0] to a root of roots at
// the time now, for the use client authentication.
func verifyPaths(certs []*x509.Certificate, roots *x509.CertPool, now time.Time) ([][]*x509.Certificate, error) {
	inter := x509.NewCertPool()
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}

	chains, err := certs[0].Verify(x509.VerifyOptions{
		DNSName: "", Roots: roots, Intermediates: inter, CurrentTime: now,
		KeyUsages:                 []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		MaxConstraintComparisions: 0, CertificatePolicies: nil,
	})
	if err != nil {
		return nil, mapVerifyError(err, now)
	}

	if len(chains) == 0 {
		return nil, fmt.Errorf("%w: no chain", ErrUnknownIssuer)
	}

	return chains, nil
}

// mapVerifyError turns an error of x509 Verify into a sentinel. A certificate
// of the chain outside its time gives ErrExpired or ErrNotYetValid, and any
// other error gives ErrUnknownIssuer.
func mapVerifyError(err error, now time.Time) error {
	var invalid x509.CertificateInvalidError

	if errors.As(err, &invalid) && invalid.Reason == x509.Expired && invalid.Cert != nil {
		if now.Before(invalid.Cert.NotBefore) {
			return fmt.Errorf("%w: a certificate of the chain is valid from %s", ErrNotYetValid,
				invalid.Cert.NotBefore.UTC().Format(time.RFC3339))
		}

		return fmt.Errorf("%w: a certificate of the chain is valid until %s", ErrExpired,
			invalid.Cert.NotAfter.UTC().Format(time.RFC3339))
	}

	return fmt.Errorf("%w: %w", ErrUnknownIssuer, err)
}

// checkRevocation runs check for each certificate but the root of the first
// chain of chains in which every check passes. When no chain passes it
// answers the first error. A nil check gives ErrRevocationUnknown.
func checkRevocation(chains [][]*x509.Certificate, check func(*x509.Certificate) error) error {
	if check == nil {
		return fmt.Errorf("%w: the server has no revocation source", ErrRevocationUnknown)
	}

	var first error

	for _, chain := range chains {
		err := checkOneChain(chain, check)
		if err == nil {
			return nil
		}

		if first == nil {
			first = err
		}
	}

	return first
}

// checkOneChain runs check for each certificate of chain but the root.
func checkOneChain(chain []*x509.Certificate, check func(*x509.Certificate) error) error {
	for _, c := range chain[:len(chain)-1] {
		err := check(c)

		switch {
		case err == nil:
			continue
		case errors.Is(err, ErrRevoked):
			return fmt.Errorf("%w: serial %s", ErrRevoked, c.SerialNumber.Text(serialBase))
		default:
			// Any error that is not a clear "revoked" means that the status
			// is not known. The server fails closed.
			return fmt.Errorf("%w: %w", ErrRevocationUnknown, err)
		}
	}

	return nil
}

// MatchRule returns the one rule of ns that names the exact SPIFFE ID
// spiffeID in the bundle bundleID. It returns ErrNoMatchingRule for no
// rule, and ErrAmbiguousRule for two or more. A rule has no flag of its
// own to disable it, so each rule of the namespace is enabled.
func MatchRule(rules []trustrule.Rule, ns namespace.Name, spiffeID, bundleID string) (trustrule.Rule, error) {
	// Defense in depth: VerifyChain refuses an empty or wildcard SPIFFE ID
	// and the bundle always has an ID. This check stays so that a caller
	// never matches the rules that have no SPIFFE ID or no bundle.
	if spiffeID == "" || bundleID == "" || strings.Contains(spiffeID, "*") {
		return trustrule.Rule{}, fmt.Errorf("%w: no exact SPIFFE ID or no bundle", ErrNoMatchingRule)
	}

	var (
		found trustrule.Rule
		count int
	)

	for i := range rules {
		r := &rules[i]
		if r.Namespace != ns || r.Bundle != bundleID || r.SpiffeIDPattern != spiffeID {
			continue
		}

		found = *r
		count++
	}

	switch count {
	case 0:
		return trustrule.Rule{}, ErrNoMatchingRule
	case 1:
		return found, nil
	default:
		return trustrule.Rule{}, fmt.Errorf("%w: %d rules", ErrAmbiguousRule, count)
	}
}

// MtlsTokenIssuer makes the access token of an mTLS exchange.
// *accesstoken.Service meets it.
type MtlsTokenIssuer interface {
	// NewMtlsTrustToken returns a new token of the kind mtls-exchange. It
	// does not store the token. The token never lives past in.LeafNotAfter.
	NewMtlsTrustToken(ctx context.Context, in accesstoken.MtlsInput) (accesstoken.Issued, error)
}

// MtlsConfig holds the dependencies of an MtlsExchanger. Every field except
// Refusals, Revocation, Now and Log is required.
type MtlsConfig struct {
	Rules      trustrule.Store
	Exchanges  trustrule.ExchangeStore
	Bundles    trustrule.BundleStore
	Namespaces Namespaces
	Principals Principals
	Tokens     MtlsTokenIssuer
	// Audit keeps the entries of the refused exchanges.
	Audit audit.Store
	// ServerAudit keeps the entries of the installation.
	ServerAudit audit.ServerStore
	// Refusals bounds the audit entries of refused exchanges, as in
	// ExchangeConfig. Nil means a limiter of the exchanger only.
	Refusals *denylimit.Limiter
	// Revocation reports a revoked certificate with ErrRevoked, and any
	// other error when it cannot tell. Nil means that the server has no
	// revocation source: each verify refuses with ErrRevocationUnknown.
	Revocation func(*x509.Certificate) error
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Log is the logger. Nil means a logger that discards.
	Log *slog.Logger
}

// MtlsExchanger exchanges the client certificate of a workload for an access
// token, through the trust rules of a namespace. It reads no header and no
// request: it takes the chain from the TLS layer.
type MtlsExchanger struct {
	cfg MtlsConfig
	// refuser writes the audit entries of the refusals. It shares the
	// code of the JWT exchange and holds none of its dependencies that the
	// refusals do not use.
	refuser *Exchanger
	now     func() time.Time
}

// NewMtlsExchanger returns an MtlsExchanger, or
// trustrule.ErrMissingDependency.
func NewMtlsExchanger(cfg MtlsConfig) (*MtlsExchanger, error) {
	switch {
	case cfg.Rules == nil, cfg.Exchanges == nil, cfg.Bundles == nil, cfg.Namespaces == nil, cfg.Principals == nil,
		cfg.Tokens == nil, cfg.Audit == nil, cfg.ServerAudit == nil:
		return nil, fmt.Errorf("%w: the mTLS exchange needs rules, exchanges, bundles, namespaces, principals, "+
			"tokens, audit and server audit", trustrule.ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	refusals := cfg.Refusals
	if refusals == nil {
		refusals = denylimit.New(denylimit.Config{
			Window: 0, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0, Now: now,
		})
	}

	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	cfg.Now, cfg.Log, cfg.Refusals = now, log, refusals

	return &MtlsExchanger{cfg: cfg, refuser: newRefuser(&cfg), now: now}, nil
}

// newRefuser returns the part of an Exchanger that writes the refusal entries.
func newRefuser(cfg *MtlsConfig) *Exchanger {
	return &Exchanger{
		cfg: ExchangeConfig{
			Rules: cfg.Rules, Exchanges: cfg.Exchanges, Namespaces: cfg.Namespaces, Principals: cfg.Principals,
			Tokens: nil, Audit: cfg.Audit, ServerAudit: cfg.ServerAudit, Refusals: cfg.Refusals, Keys: nil,
			InstallationURLs: nil, OwnIssuer: nil, Now: cfg.Now, Log: cfg.Log,
		},
		own: nil, refusals: cfg.Refusals, warnings: nil, log: cfg.Log, now: cfg.Now,
	}
}

// MtlsVerifyInput is the chain of one call, as the TLS layer read it from the
// handshake. Request describes where the call came from, for the audit
// entries.
type MtlsVerifyInput struct {
	Namespace namespace.Name
	// RawChain holds the DER certificates of the handshake, the leaf first.
	RawChain [][]byte
	Request  audit.Entry
}

// Verify checks the chain of in against the active version of the trust
// bundle of the namespace and returns the VerifiedChain that Exchange
// takes. Each refusal writes an audit entry with the outcome denied.
func (e *MtlsExchanger) Verify(ctx context.Context, in *MtlsVerifyInput) (VerifiedChain, error) {
	ns, err := e.cfg.Namespaces.WritableHere(ctx, in.Namespace)
	if err != nil {
		// An unknown namespace has no audit log and no bundle.
		return VerifiedChain{}, namespaceError(err)
	}

	bundle, err := e.cfg.Bundles.GetBundle(ctx, in.Namespace)
	if err != nil {
		if errors.Is(err, trustrule.ErrNotFound) {
			return VerifiedChain{}, e.refuse(ctx, ns, in.Request, nil, fmt.Errorf("%w: %w", ErrBundleUnknown, err))
		}

		return VerifiedChain{}, fmt.Errorf("%w: read the trust bundle: %w", ErrIssueFailed, err)
	}

	roots, version, err := activeRoots(&bundle)
	if err != nil {
		return VerifiedChain{}, e.refuse(ctx, ns, in.Request, nil, err)
	}

	chain, err := VerifyChain(in.RawChain, roots, e.now(), e.cfg.Revocation)
	if err != nil {
		return VerifiedChain{}, e.refuse(ctx, ns, in.Request, nil, err)
	}

	chain.bundleID, chain.bundleVersion = bundle.ID, version

	return chain, nil
}

// RefuseCall writes the denial entry of a call that the guard refused before
// Verify ran, for example a call over the rate limit. cause is the refusal of
// the guard. The entry goes through the same limiter as the other refusals of
// the namespace. RefuseCall returns only the failures of the write, never
// cause: the caller answers with cause whatever happens here. An unknown
// namespace has no audit log, so the result is the error of the namespace and
// no entry is written. The caller calls RefuseCall for the first refusal of
// each window only, because each call reads the namespace.
func (e *MtlsExchanger) RefuseCall(
	ctx context.Context, name namespace.Name, request audit.Entry, cause error,
) error {
	ns, err := e.cfg.Namespaces.WritableHere(ctx, name)
	if err != nil {
		return namespaceError(err)
	}

	in := ExchangeInput{Namespace: ns.Name, Token: "", Rule: "", Request: request}

	return errors.Join(writeFailures(e.refuser.refuse(ctx, ns, in, nil, cause))...)
}

// namespaceError turns the error of a namespace read into the answer of the
// mTLS exchange.
func namespaceError(err error) error {
	if errors.Is(err, namespace.ErrNotFound) {
		return fmt.Errorf("%w: namespace: %w", ErrBundleUnknown, err)
	}

	return fmt.Errorf("%w: read the namespace: %w", ErrIssueFailed, err)
}

// activeRoots returns the CA pool of the active version of b and its number.
// A bundle with no active version, or with no CA in it, gives
// ErrBundleUnknown.
func activeRoots(b *trustrule.Bundle) (*x509.CertPool, int64, error) {
	if b.Active < 1 || b.ID.Validate() != nil {
		return nil, 0, fmt.Errorf("%w: no active version", ErrBundleUnknown)
	}

	for i := range b.Versions {
		v := &b.Versions[i]
		if v.Number != b.Active {
			continue
		}

		if len(v.Certificates) == 0 {
			break
		}

		pool := x509.NewCertPool()
		for _, c := range v.Certificates {
			pool.AddCert(c)
		}

		return pool, v.Number, nil
	}

	return nil, 0, fmt.Errorf("%w: the active version %d holds no CA", ErrBundleUnknown, b.Active)
}

// MtlsExchangeInput is one mTLS exchange. Chain is the value of Verify.
type MtlsExchangeInput struct {
	Namespace namespace.Name
	Chain     VerifiedChain
	Request   audit.Entry
}

// MtlsExchangeResult is the access token of an mTLS exchange and the rule
// that matched. The secret shows once.
type MtlsExchangeResult struct {
	Token  accesstoken.Token
	Secret accesstoken.Secret
	Rule   trustrule.Rule
}

// Exchange issues an access token for the principal of the one rule that
// names the SPIFFE ID of in.Chain in its bundle. It checks, in order: the
// chain value, the namespace, the leaf time, the one matching rule, the
// principal, and then it stores the token with its audit entry in one
// transaction in which the store checks the fence, the rule, the SPIFFE ID
// and the active bundle version again. A refusal writes an audit entry with
// the outcome denied and gives no token. A failed store gives ErrIssueFailed,
// no token and no entry.
func (e *MtlsExchanger) Exchange(ctx context.Context, in *MtlsExchangeInput) (MtlsExchangeResult, error) {
	ns, err := e.cfg.Namespaces.WritableHere(ctx, in.Namespace)
	if err != nil {
		return MtlsExchangeResult{}, namespaceError(err)
	}

	// Defense in depth: the namespace service answers the namespace that was
	// asked for. This check stays so that a wrong answer never moves the
	// fence, the rule list or the audit log to another namespace.
	if ns.Name != in.Namespace {
		return MtlsExchangeResult{}, fmt.Errorf("%w: the namespace read does not match the call", ErrIssueFailed)
	}

	chain := &in.Chain

	signed := &signedBy{rule: "", subject: chain.spiffeID}

	if valueErr := e.checkChainValue(chain); valueErr != nil {
		return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signedOrNil(chain, signed), valueErr)
	}

	rules, err := e.cfg.Rules.List(ctx, in.Namespace)
	if err != nil {
		return MtlsExchangeResult{}, fmt.Errorf("%w: list trust rules: %w", ErrIssueFailed, err)
	}

	rule, err := MatchRule(rules, in.Namespace, chain.spiffeID, string(chain.bundleID))
	if err != nil {
		return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signed, err)
	}

	signed.rule = rule.ID

	owner, err := e.checkPrincipal(ctx, &rule)
	if err != nil {
		return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signed, err)
	}

	return e.issue(ctx, ns, in, &rule, &owner)
}

// signedOrNil returns signed when the chain names a SPIFFE ID, and nil for a
// chain value with none, so that a zero value never takes the limiter key of
// a verified chain.
func signedOrNil(c *VerifiedChain, signed *signedBy) *signedBy {
	if c.spiffeID == "" {
		return nil
	}

	return signed
}

// checkChainValue refuses a VerifiedChain that VerifyChain and Verify did not
// make whole: the zero value, a value with no bundle, and a leaf that has
// expired since Verify. VerifyChain checked the time too; the two calls run
// at different times.
func (e *MtlsExchanger) checkChainValue(c *VerifiedChain) error {
	switch {
	case c.leaf == nil || c.spiffeID == "":
		return fmt.Errorf("%w: the chain value is empty", ErrNoCertificate)
	case c.bundleID.Validate() != nil || c.bundleVersion < 1:
		return fmt.Errorf("%w: the chain names no bundle version", ErrBundleUnknown)
	case strings.Contains(c.spiffeID, "*"):
		return fmt.Errorf("%w: the SPIFFE ID holds a *", ErrInvalid)
	}

	return checkLeafTime(c.leaf, e.now())
}

// checkPrincipal checks the principal of the matched rule r, at the time of
// the exchange, and returns it.
func (e *MtlsExchanger) checkPrincipal(ctx context.Context, r *trustrule.Rule) (principal.Principal, error) {
	var none principal.Principal

	// Defense in depth: the store checks the principal again in the
	// transaction of the exchange. This check stays so that a refused
	// principal costs no token and no write.
	p, err := e.cfg.Principals.GetPrincipal(ctx, r.PrincipalID)
	if errors.Is(err, principal.ErrNotFound) {
		return none, fmt.Errorf("%w: no principal %q", trustrule.ErrPrincipal, r.PrincipalID)
	}

	if err != nil {
		return none, fmt.Errorf("%w: read principal %q: %w", ErrIssueFailed, r.PrincipalID, err)
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

// issue makes the token and records it with its entry in one transaction.
func (e *MtlsExchanger) issue(
	ctx context.Context, ns namespace.Namespace, in *MtlsExchangeInput, rule *trustrule.Rule,
	owner *principal.Principal,
) (MtlsExchangeResult, error) {
	chain := &in.Chain
	at := e.now().UTC().Truncate(time.Millisecond)

	signed := &signedBy{rule: rule.ID, subject: chain.spiffeID}

	issued, err := e.cfg.Tokens.NewMtlsTrustToken(ctx, accesstoken.MtlsInput{
		At: at, LeafNotAfter: chain.leafNotAfter, PrincipalID: rule.PrincipalID,
		Name: "trust rule " + string(rule.ID), CreatedBy: trustrule.Ref(rule.ID),
	})
	if errors.Is(err, accesstoken.ErrInvalid) {
		// The token service refuses a leaf that is not valid after the issue
		// time, so the leaf expired between the checks and the issue.
		return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signed, fmt.Errorf("%w: %w", ErrExpired, err))
	}

	if err != nil {
		return MtlsExchangeResult{}, fmt.Errorf("%w: make the access token: %w", ErrIssueFailed, err)
	}

	entry := mtlsEntry(in.Request, at, rule, chain.spiffeID, issued.Token)
	entry.Subject = owner.Subject
	entry.PrincipalKind = string(owner.Kind)

	xch := trustrule.Exchange{
		Token: issued.Token, Digest: issued.Digest, Rule: rule.ID, SPIFFEID: chain.spiffeID,
		BundleID: chain.bundleID, BundleVersion: chain.bundleVersion,
	}

	if err := e.cfg.Exchanges.RecordExchange(ctx, xch, ns.Fence(), entry); err != nil {
		switch {
		case errors.Is(err, trustrule.ErrNotFound):
			return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signed,
				fmt.Errorf("%w: %w", ErrNoMatchingRule, err))
		case errors.Is(err, trustrule.ErrPrincipal):
			return MtlsExchangeResult{}, e.refuse(ctx, ns, in.Request, signed, err)
		default:
			return MtlsExchangeResult{}, fmt.Errorf("%w: record the exchange: %w", ErrIssueFailed, err)
		}
	}

	return MtlsExchangeResult{Token: issued.Token, Secret: issued.Secret, Rule: *rule}, nil
}

// refuse writes the denial entry of a refused call, as the JWT exchange does,
// and returns cause. When the entry fails, the failure joins the answer, so
// the caller still fails closed. signed is not nil for a chain that Verify
// accepted: its refusals have a limiter key of their own for each SPIFFE ID,
// so refusals of junk chains never hide them.
func (e *MtlsExchanger) refuse(
	ctx context.Context, ns namespace.Namespace, request audit.Entry, signed *signedBy, cause error,
) error {
	in := ExchangeInput{Namespace: ns.Name, Token: "", Rule: "", Request: request}

	answer := e.refuser.refuse(ctx, ns, in, signed, cause)

	return errors.Join(append([]error{cause}, writeFailures(answer)...)...)
}

// writeFailures returns the errors of answer that are not ErrNoMatch: the
// failures of the audit writes that refuse joined to its answer.
func writeFailures(answer error) []error {
	joined, ok := answer.(interface{ Unwrap() []error })
	if !ok {
		return nil
	}

	var out []error

	for _, err := range joined.Unwrap() {
		if !errors.Is(err, ErrNoMatch) && !errors.Is(err, ErrAmbiguous) {
			out = append(out, err)
		}
	}

	return out
}

// mtlsEntry returns the audit entry of the exchange of the SPIFFE ID
// spiffeID through r, which issued t.
func mtlsEntry(e audit.Entry, at time.Time, r *trustrule.Rule, spiffeID string, t accesstoken.Token) audit.Entry {
	e.At = at
	e.Namespace = r.Namespace
	e.TokenID = t.ID
	e.Action = ActionExchange
	e.Object = identity.NamespaceObject(r.Namespace).String()
	e.ObjectID = string(r.ID)
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = []audit.Change{
		{Key: changeKeyPrincipal, Old: nil, New: r.PrincipalID, Layer: "", TakesEffect: ""},
		{Key: "kind", Old: nil, New: string(t.Kind), Layer: "", TakesEffect: ""},
		{Key: "spiffeID", Old: nil, New: clean(spiffeID, maxClaimRunes), Layer: "", TakesEffect: ""},
		{Key: "expiresAt", Old: nil, New: t.ExpiresAt.UTC().Format(time.RFC3339), Layer: "", TakesEffect: ""},
	}

	return e
}
