package issuersvc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"time"
	"unicode"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

const (
	jtiPrefix = "oit-"
	// notBeforeSkew is how long before iat a token is valid, so that a token
	// from a server whose clock runs ahead still verifies at a cloud.
	notBeforeSkew = 30 * time.Second
	// maxPayloadBytes bounds the claims of one token.
	maxPayloadBytes = 8 << 10
	// purposeLogin and purposeAdapter are the values of the claim purpose.
	purposeLogin   = "login"
	purposeAdapter = "adapter"
	// openAction is the action that the audit entry of a token records.
	openAction = "profile:open"
)

var b64 = base64.RawURLEncoding

// Login implements profile.Issuer.
func (s *Service) Login(
	ctx context.Context, o profile.OpenContext, block, aud string, lifetime time.Duration,
) (profile.Token, error) {
	return s.sign(ctx, o, signRequest{purpose: purposeLogin, block: block, aud: aud, lifetime: lifetime, body: nil})
}

// Adapter implements profile.Issuer.
func (s *Service) Adapter(
	ctx context.Context, o profile.OpenContext, block, aud string, body []byte,
) (profile.Token, error) {
	return s.sign(ctx, o, signRequest{
		purpose: purposeAdapter, block: block, aud: aud, lifetime: profile.DefaultTokenLifetime, body: body,
	})
}

type signRequest struct {
	purpose  string
	block    string
	aud      string
	body     []byte
	lifetime time.Duration
}

// sign is the one place that signs. Each check below stands on its own: the
// caller has checked the audience and the lifetime too, and this function does
// not rely on it.
func (s *Service) sign(ctx context.Context, o profile.OpenContext, r signRequest) (profile.Token, error) {
	if err := checkRequest(o, r, s.publicURL); err != nil {
		return profile.Token{}, err
	}

	name := o.Namespace.Name

	// Fail closed on a namespace that the open did not read as it is now. A
	// stale failover version means that another instance may hold the
	// namespace, and an instance of the old home signs nothing.
	current, err := s.namespaces.GetNamespace(ctx, name)
	if err != nil {
		return profile.Token{}, fmt.Errorf("read namespace %s: %w", name, err)
	}

	if current.ID != o.Namespace.ID || current.FailoverVersion != o.Namespace.FailoverVersion {
		return profile.Token{}, fmt.Errorf("sign for %s: %w", name, namespace.ErrFenced)
	}

	set, err := s.store.GetIssuerSettings(ctx, name)
	if err != nil {
		return profile.Token{}, fmt.Errorf("read issuer settings: %w", err)
	}

	iss, err := s.issuerURL(current, set)
	if err != nil {
		return profile.Token{}, err
	}

	active, err := s.signingKey(ctx, current, set, o.By)
	if err != nil {
		return profile.Token{}, err
	}

	priv, err := s.open(ctx, active)
	if err != nil {
		return profile.Token{}, err
	}

	now := s.now().UTC().Truncate(time.Second)
	jti := ident.NewID(jtiPrefix)

	payload, err := payload(o, r, iss, jti, now)
	if err != nil {
		return profile.Token{}, err
	}

	value, err := compact(priv, active.ID, payload)
	if err != nil {
		return profile.Token{}, err
	}

	exp := now.Add(r.lifetime)

	return profile.Token{
		IssuedAt: now, ExpiresAt: exp, Entry: tokenEntry(o, r, jti, active.ID, exp, now), Value: value, ID: jti,
		Issuer: iss, Subject: o.Subject, Audience: r.aud, KeyID: active.ID, Warnings: s.warnings(set, active, now),
	}, nil
}

// checkRequest checks the request of a token against the rules of the issuer.
func checkRequest(o profile.OpenContext, r signRequest, publicURL string) error {
	if r.lifetime < profile.MinTokenLifetime || r.lifetime > profile.MaxTokenLifetime {
		return fmt.Errorf("%w: a token lives from %s to %s, not %s", profile.ErrInvalid, profile.MinTokenLifetime,
			profile.MaxTokenLifetime, r.lifetime)
	}

	if err := profile.CheckAudience(r.aud, publicURL); err != nil {
		return err
	}

	if o.Namespace.Name == "" || o.Namespace.ID == "" {
		return fmt.Errorf("%w: the open names no namespace", profile.ErrInvalid)
	}

	if err := checkSubject(o.Subject); err != nil {
		return err
	}

	if r.block == "" || len(r.block) > 255 {
		return fmt.Errorf("%w: the block name has 1 to 255 bytes", profile.ErrInvalid)
	}

	for name := range o.Claims {
		if err := profile.CheckClaimName(name); err != nil {
			return err
		}
	}

	return nil
}

func checkSubject(sub string) error {
	if sub == "" {
		return fmt.Errorf("%w: a token needs a subject", profile.ErrInvalid)
	}

	if len(sub) > profile.MaxSubjectBytes {
		return profile.ErrSubjectTooLong
	}

	for _, r := range sub {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: the subject holds a control character", profile.ErrInvalid)
		}
	}

	return nil
}

// signingKey returns the key that signs now for namespace ns, and makes the
// first keys of a namespace that has none and no issuerURL.
func (s *Service) signingKey(
	ctx context.Context, ns namespace.Namespace, set profile.IssuerSettings, by audit.Entry,
) (profile.Key, error) {
	keys, err := s.store.Live(ctx, ns.Name)
	if err != nil {
		return profile.Key{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		if set.IssuerURL != "" {
			return profile.Key{}, fmt.Errorf("%w: the namespace has no key: call %s to make the keys",
				profile.ErrIssuerKeyMissing, keysRoute(ns.Name))
		}

		if keys, err = s.firstKeys(ctx, ns, by, "the first open of a login block"); err != nil {
			return profile.Key{}, err
		}
	}

	active, ok := findState(keys, profile.KeyActive)
	if !ok {
		return profile.Key{}, fmt.Errorf("%w: the namespace has no active key", profile.ErrIssuerKeyMissing)
	}

	if set.IssuerURL != "" {
		if err = s.checkCopied(ns.Name, set, active); err != nil {
			return profile.Key{}, err
		}
	}

	return active, nil
}

// checkCopied checks the keys of a namespace with an issuerURL: the active key
// is in a copied JWKS, and it signed for less than profile.MaxActiveSigning.
func (s *Service) checkCopied(ns namespace.Name, set profile.IssuerSettings, active profile.Key) error {
	if s.now().Sub(active.ActivatedAt) >= profile.MaxActiveSigning {
		return fmt.Errorf("%w: export the files with GET %s and check the copy with POST %s",
			profile.ErrIssuerKeyExpired, exportRoute(ns), checkCopyRoute(ns))
	}

	if !set.CopyCheck.Holds(active.ID) {
		return fmt.Errorf("%w: key %s is in no copied JWKS: copy the files from GET %s, then call POST %s",
			profile.ErrIssuerKeyMissing, active.ID, exportRoute(ns), checkCopyRoute(ns))
	}

	return nil
}

// warnings returns the texts for a person about a namespace with an issuerURL
// whose active key signs after its 90 days.
func (s *Service) warnings(set profile.IssuerSettings, active profile.Key, now time.Time) []string {
	if set.IssuerURL == "" {
		return nil
	}

	age := now.Sub(active.ActivatedAt)
	if age < profile.ActiveKeyLifetime {
		return nil
	}

	left := int((profile.MaxActiveSigning - age + 24*time.Hour - 1) / (24 * time.Hour))

	return []string{fmt.Sprintf(
		"the signing key signed for %d days and the copy at %s has no newer key: %d days are left before it stops signing; "+
			"copy the files from GET %s and call POST %s",
		int(age/(24*time.Hour)), set.IssuerURL, max(left, 0), exportRoute(set.Namespace), checkCopyRoute(set.Namespace))}
}

func keysRoute(ns namespace.Name) string {
	return "/api/dustpan/v1/namespaces/" + ns.String() + "/issuer/keys"
}

func exportRoute(ns namespace.Name) string {
	return "/api/dustpan/v1/namespaces/" + ns.String() + "/issuer/export"
}

func checkCopyRoute(ns namespace.Name) string {
	return keysRoute(ns) + "/check-copy"
}

// payload builds the claims of a token. The claims of the open never replace a
// claim that the issuer sets.
func payload(o profile.OpenContext, r signRequest, iss, jti string, now time.Time) ([]byte, error) {
	claims := map[string]any{}
	maps.Copy(claims, o.Claims)

	// The issuer sets these after the copy, so a claim of the open with one of
	// these names never wins. CheckClaimName already refused such a name.
	claims["iss"] = iss
	claims["sub"] = o.Subject
	claims["aud"] = r.aud
	claims["iat"] = now.Unix()
	claims["nbf"] = now.Add(-notBeforeSkew).Unix()
	claims["exp"] = now.Add(r.lifetime).Unix()
	claims["jti"] = jti
	claims["namespace"] = o.Namespace.Name.String()
	claims["namespace_id"] = o.Namespace.ID.String()
	claims["purpose"] = r.purpose

	if r.purpose == purposeAdapter {
		sum := sha256.Sum256(r.body)
		claims["body_sha256"] = b64.EncodeToString(sum[:])
	}

	raw, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("%w: the claims do not encode: %w", profile.ErrInvalid, err)
	}

	if len(raw) > maxPayloadBytes {
		return nil, fmt.Errorf("%w: the claims are larger than %d bytes", profile.ErrInvalid, maxPayloadBytes)
	}

	return raw, nil
}

// compact signs the payload as a compact JWT with RS256.
func compact(priv *rsa.PrivateKey, kid profile.KeyID, payload []byte) (string, error) {
	header, err := json.Marshal(map[string]string{"alg": profile.KeyAlgorithm, "typ": "JWT", "kid": string(kid)})
	if err != nil {
		return "", fmt.Errorf("encode token header: %w", err)
	}

	signing := b64.EncodeToString(header) + "." + b64.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signing))

	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}

	return signing + "." + b64.EncodeToString(sig), nil
}

// tokenEntry makes the audit entry of one token. It holds no token.
func tokenEntry(o profile.OpenContext, r signRequest, jti string, kid profile.KeyID, exp, now time.Time) audit.Entry {
	e := o.By
	e.At = now
	e.Namespace = o.Namespace.Name
	e.Action = openAction
	e.Object = identity.NamespaceObject(o.Namespace.Name).String()
	e.ObjectID = string(o.ProfileID)
	e.Kind = audit.KindAccess
	e.Outcome = audit.OutcomeSuccess
	e.Changes = []audit.Change{
		change("profile", string(o.Profile)),
		change("block", r.block),
		change("purpose", r.purpose),
		change("audience", r.aud),
		change("subject", o.Subject),
		change("jti", jti),
		change("exp", strconv.FormatInt(exp.Unix(), 10)),
		change("kid", string(kid)),
	}

	return e
}

func change(key, value string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: value, Layer: "", TakesEffect: ""}
}
