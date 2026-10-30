// Package issuersvc is the OIDC issuer of the namespaces: the signing keys and
// their rotation, the discovery document and the JWKS, the settings, the copy
// check for a namespace with an issuerURL, and the one signer of the server
// (profile.Issuer).
//
// It is security code. A private key is sealed under the data key of the
// namespace scope "issuer", and only the signer opens it, in the memory of
// the instance, for at most one minute. Every sign checks the fence of the
// namespace, the state of the key, the audience and the lifetime itself.
package issuersvc

import (
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
)

// ErrMissingDependency reports a Config without a required port.
var ErrMissingDependency = errors.New("missing dependency")

// scopeID is the ID of the secret scope of the signing keys of a namespace.
const scopeID = "issuer"

// keyCacheTime is how long the signer keeps an opened private key in memory.
const keyCacheTime = time.Minute

// Notifier appends the notices of the issuer. notify.Outbox meets it.
type Notifier interface {
	Append(ctx context.Context, fence namespace.Fence, ns []notify.Notification) error
}

// Config holds the dependencies of a Service. Notifier and HTTP are optional.
type Config struct {
	Store      profile.IssuerStore
	Namespaces namespace.Store
	Sealer     profile.Sealer
	Authorizer identity.Authorizer
	// Notifier receives issuer.key-next, issuer.key-revoked and
	// issuer.key-copy-missing. Nil sends no notice.
	Notifier Notifier
	// HTTP makes the requests of the copy check. It must apply the egress
	// rules (egresshttp.Client). Nil makes each copy check fail closed.
	HTTP *http.Client
	// IssuerBaseURL and PublicURL give the issuer host. The issuer base URL
	// wins; when it is empty the public URL is used. Both empty: the server
	// issues no token.
	IssuerBaseURL string
	PublicURL     string
	Now           func() time.Time
	Log           *slog.Logger
}

// Service implements the use cases of the issuer, and profile.Issuer.
type Service struct {
	store      profile.IssuerStore
	namespaces namespace.Store
	sealer     profile.Sealer
	authz      identity.Authorizer
	notifier   Notifier
	http       *http.Client
	issuerBase string
	publicURL  string
	now        func() time.Time
	log        *slog.Logger

	mu    sync.Mutex
	cache map[cacheKey]cachedKey
}

var _ profile.Issuer = (*Service)(nil)

type cacheKey struct {
	ns namespace.Name
	id profile.KeyID
}

// cachedKey is a private key that the signer opened. sealed is the hash of
// the sealed bytes that it came from, so a changed row never uses a stale key.
type cachedKey struct {
	at     time.Time
	key    *rsa.PrivateKey
	sealed [sha256.Size]byte
}

// NewService returns a Service. It returns ErrMissingDependency when a
// required port is missing.
func NewService(cfg Config) (*Service, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Sealer == nil || cfg.Authorizer == nil {
		return nil, fmt.Errorf("%w: store, namespaces, sealer and authorizer", ErrMissingDependency)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Service{
		store: cfg.Store, namespaces: cfg.Namespaces, sealer: cfg.Sealer, authz: cfg.Authorizer,
		notifier: cfg.Notifier, http: cfg.HTTP, issuerBase: trimSlash(cfg.IssuerBaseURL),
		publicURL: trimSlash(cfg.PublicURL), now: now, log: log, mu: sync.Mutex{}, cache: map[cacheKey]cachedKey{},
	}, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}

	return s
}

// host returns the issuer host: the issuer base URL, else the public URL.
func (s *Service) host() (string, error) {
	switch {
	case s.issuerBase != "":
		return s.issuerBase, nil
	case s.publicURL != "":
		return s.publicURL, nil
	default:
		return "", fmt.Errorf("%w", profile.ErrIssuerNotConfigured)
	}
}

// issuerURL returns the issuer URL of namespace ns with settings set: its
// issuerURL, else the host and the path with the stable ID.
func (s *Service) issuerURL(ns namespace.Namespace, set profile.IssuerSettings) (string, error) {
	if set.IssuerURL != "" {
		return set.IssuerURL, nil
	}

	host, err := s.host()
	if err != nil {
		return "", err
	}

	return host + "/oidc/" + ns.ID.String(), nil
}

// Actor is the caller of a use case.
type Actor = profile.Actor

func (s *Service) require(ctx context.Context, a *Actor, ns namespace.Name, action identity.Action) error {
	if err := s.authz.Authorize(ctx, a.Principal, action, identity.NamespaceObject(ns)); err != nil {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}

// closeBody closes a response body and logs the failure.
func (s *Service) closeBody(c io.Closer) {
	if err := c.Close(); err != nil {
		s.log.Debug("issuer: close response body", slog.String("error", err.Error()))
	}
}
