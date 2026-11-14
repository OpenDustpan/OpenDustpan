package dustpan

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	registryapi "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/registry/pgpverify"
)

// WithRegistry sets the stores of the private registry: the releases and the
// trusted signing keys. With them, and with a blob store, the server serves the
// registry API under /api/dustpan/v1/namespaces/{namespace}/registry. With a
// public URL too, it serves the provider registry protocol of OpenTofu under
// /v1/providers/ and the module registry protocol under /v1/modules/, and
// advertises them as providers.v1 and modules.v1 in service discovery.
// Without them the server serves no registry route.
func WithRegistry(releases registry.Store, keys registry.KeyStore) Option {
	return func(o *settings) {
		if o.registry == nil {
			o.registry = &registrySettings{releases: nil, keys: nil, urlKey: nil}
		}

		o.registry.releases, o.registry.keys = releases, keys
	}
}

// registrySettings holds the options of the private registry.
type registrySettings struct {
	releases registry.Store
	keys     registry.KeyStore
	urlKey   []byte
}

// WithRegistryURLKey sets the key that signs the file URLs of the provider
// registry protocol. It has at least tfe.MinRegistryURLKeyBytes bytes. Without
// it the server makes a random key at start, so a signed URL is valid only on
// the instance that made it: instances behind one address need one shared key.
func WithRegistryURLKey(key []byte) Option {
	return func(o *settings) {
		if o.registry == nil {
			o.registry = &registrySettings{releases: nil, keys: nil, urlKey: nil}
		}

		o.registry.urlKey = append([]byte(nil), key...)
	}
}

// newRegistryService builds the registry service, or returns nil when no
// registry store is set. onFailure counts a refused publish whose audit entry
// could not be written.
func newRegistryService(cfg *settings, onFailure func()) (*service.Service, error) {
	if cfg.registry == nil || cfg.registry.releases == nil || cfg.registry.keys == nil {
		return nil, nil //nolint:nilnil // No registry: no registry store is set.
	}

	if cfg.blobStore == nil {
		return nil, fmt.Errorf("the registry needs a blob store: %w", ErrMissingOption)
	}

	verifier := pgpverify.Verifier{Now: nil}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	svc, err := service.New(service.Config{
		Store:                 cfg.registry.releases,
		Keys:                  cfg.registry.keys,
		Verifier:              verifier,
		Inspector:             verifier,
		Blobs:                 cfg.blobStore,
		Namespaces:            cfg.namespaceStore,
		Audit:                 entries,
		Logger:                cfg.logger,
		OnAuditFailure:        onFailure,
		Now:                   cfg.now,
		MaxArchiveBytes:       0,
		MaxReleaseBytes:       0,
		MaxModuleArchiveBytes: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("registry service: %w", err)
	}

	return svc, nil
}

// newRegistryHandler builds the registry API, or returns nil when no registry
// store is set.
func newRegistryHandler(svc services, cfg *settings, onFailure func()) (http.Handler, error) {
	regSvc, err := newRegistryService(cfg, onFailure)
	if err != nil || regSvc == nil {
		return nil, err
	}

	entries, ok := cfg.namespaceStore.(audit.Store)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit entries: %w", ErrMissingOption)
	}

	idem, err := idempotencyMiddleware(cfg)
	if err != nil {
		return nil, err
	}

	handler, err := registryapi.NewHandler(registryapi.Config{
		Service: regSvc, Authenticator: cfg.authenticator, Authorizer: cfg.authorizer,
		MaxUploadBytes: service.DefaultMaxArchiveBytes, Audit: entries, Namespaces: svc.namespaces,
		OnAuditFailure: onFailure, DenialLimiter: cfg.denialLimiter, Sessions: cfg.sessionCookies,
		Idempotency: idem, Logger: cfg.logger,
	})
	if err != nil {
		return nil, fmt.Errorf("registry api: %w", err)
	}

	return handler, nil
}

// errRegistryURL reports a public URL that the registry cannot take a host from.
var errRegistryURL = errors.New("the public URL has no host")

// newRegistryProtocol returns the settings of the provider registry protocol of
// OpenTofu for the TFE-compatible adapter. It returns the zero value, which
// serves no registry route, when no registry store is set or no public URL is
// set: the host check compares each request with the host of the public URL, so
// without one every request would fail.
func newRegistryProtocol(cfg *settings, publicURL string, onFailure func()) (*tfe.RegistryConfig, error) {
	regSvc, err := newRegistryService(cfg, onFailure)
	if err != nil || regSvc == nil || publicURL == "" {
		return nil, err
	}

	u, err := url.Parse(publicURL)
	if err != nil || u.Host == "" {
		return nil, errRegistryURL
	}

	key := cfg.registry.urlKey
	if len(key) == 0 {
		key = make([]byte, tfe.MinRegistryURLKeyBytes)
		if _, err = rand.Read(key); err != nil {
			return nil, fmt.Errorf("make the registry URL key: %w", err)
		}
	}

	port := "443"
	if u.Scheme == "http" {
		port = "80"
	}

	return &tfe.RegistryConfig{
		Service: regSvc, Host: strings.ToLower(u.Host), DefaultPort: port, URLKey: key, URLTTL: 0, Now: cfg.now,
	}, nil
}
