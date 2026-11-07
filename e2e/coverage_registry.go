//go:build e2e

package e2e

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	registryapi "opendustpan.org/opendustpan/internal/api/registry"
	registryservice "opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// coverageRegistryService answers nothing. It exists only to build a Handler
// for its route table.
type coverageRegistryService struct{}

func (coverageRegistryService) ServableReleases(context.Context, namespace.Name, string) ([]registry.Release, error) {
	return nil, registry.ErrNotFound
}

func (coverageRegistryService) Release(context.Context, namespace.Name, string, string) (registry.Release, error) {
	return registry.Release{}, registry.ErrNotFound
}

func (coverageRegistryService) SigningKey(context.Context, registry.Release) (registry.TrustedKey, error) {
	return registry.TrustedKey{}, registry.ErrKeyNotFound
}

func (coverageRegistryService) OpenFile(
	context.Context, namespace.Name, string, string, string,
) (int64, io.ReadCloser, error) {
	return 0, nil, registry.ErrNotFound
}

func (coverageRegistryService) ModuleReleases(
	context.Context, registry.Address,
) ([]registry.Release, error) {
	return nil, registry.ErrNotFound
}

func (coverageRegistryService) ModuleRelease(
	context.Context, namespace.Name, string, string, string,
) (registry.Release, error) {
	return registry.Release{}, registry.ErrNotFound
}

func (coverageRegistryService) OpenModuleFile(
	context.Context, registry.Address, string, string,
) (int64, io.ReadCloser, error) {
	return 0, nil, registry.ErrNotFound
}

// coverageRegistryRoutes returns the route table of the registry API. The
// service is an empty shell: it exists only to build a Handler.
func coverageRegistryRoutes(tb testing.TB) []RouteRef {
	tb.Helper()

	store := struct{ registry.Store }{nil}
	keys := struct{ registry.KeyStore }{nil}
	verifier := struct {
		registry.SignatureVerifier
		registry.KeyInspector
	}{nil, nil}

	svc, err := registryservice.New(registryservice.Config{
		Store: store, Keys: keys, Verifier: verifier, Inspector: verifier, Blobs: struct{ blob.Store }{nil},
		Namespaces: coverageRegistryNamespaces{}, Audit: coverageAuditStore{}, Logger: slog.New(slog.DiscardHandler),
		OnAuditFailure: func() {}, Now: nil, MaxArchiveBytes: 0, MaxReleaseBytes: 0, MaxModuleArchiveBytes: 0,
	})
	if err != nil {
		tb.Fatalf("registry service: %v", err)
	}

	handler, err := registryapi.NewHandler(registryapi.Config{
		Service:        svc,
		Authenticator:  coverageAuthenticator{},
		Authorizer:     coverageAuthorizer{},
		MaxUploadBytes: registryservice.DefaultMaxArchiveBytes,
		Audit:          coverageAuditStore{},
		Namespaces:     coverageRegistryNamespaces{},
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Idempotency:    nil,
		Logger:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		tb.Fatalf("registry handler: %v", err)
	}

	routes := handler.Routes()
	out := make([]RouteRef, 0, len(routes))

	for _, route := range routes {
		out = append(out, RouteRef{Method: route.Method, Path: route.Path})
	}

	return out
}

// coverageRegistryNamespaces holds no namespace.
type coverageRegistryNamespaces struct{}

func (coverageRegistryNamespaces) GetNamespace(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

// Get answers the namespace read of the refusal recorder. It finds no namespace.
func (coverageRegistryNamespaces) Get(context.Context, namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{}, namespace.ErrNotFound
}

// registryAllowEntries covers the routes of the registry API. No CLI calls
// them: a CI job or a client of the API does. The steps of TestRegistryTofuInit
// call each of them, and OpenTofu then installs what they published.
func registryAllowEntries() []AllowEntry {
	const (
		base = "/api/dustpan/v1/namespaces/{namespace}/registry"
		test = "TestRegistryTofuInit"
		why  = "Client route: no CLI calls it. The steps of TestRegistryTofuInit call it, and OpenTofu installs what it published."

		moduleTest = "TestRegistryModuleTofuInit"
		moduleWhy  = "Client route: no CLI calls it. The steps of TestRegistryModuleTofuInit call it, and OpenTofu downloads what it published."

		moduleProtocol    = "/v1/modules/{namespace}/{name}/{system}"
		moduleProtocolWhy = "OpenTofu calls it on init, but only on the registry host. The steps of TestRegistryModuleTofuInit run the real CLI against it."

		protocol    = "/v1/providers/{namespace}/{type}"
		protocolWhy = "OpenTofu calls it on init, but only on the registry host. The steps of TestRegistryTofuInit run the real CLI against it."
	)

	return []AllowEntry{
		{Method: http.MethodPost, Path: base + "/uploads", CoveringTest: test, Reason: why},
		{Method: http.MethodPut, Path: base + "/providers/{name}/versions/{version}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers/{name}/versions", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/providers/{name}/versions/{version}", CoveringTest: test, Reason: why},
		{
			Method:       http.MethodPut,
			Path:         base + "/modules/{name}/{system}/versions/{version}",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{Method: http.MethodGet, Path: base + "/modules", CoveringTest: moduleTest, Reason: moduleWhy},
		{
			Method:       http.MethodGet,
			Path:         base + "/modules/{name}/{system}/versions",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         base + "/modules/{name}/{system}/versions/{version}",
			CoveringTest: moduleTest,
			Reason:       moduleWhy,
		},
		{Method: http.MethodPost, Path: base + "/keys", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: base + "/keys", CoveringTest: test, Reason: why},
		{Method: http.MethodDelete, Path: base + "/keys/{fingerprint}", CoveringTest: test, Reason: why},
		{Method: http.MethodGet, Path: protocol + "/versions", CoveringTest: test, Reason: protocolWhy},
		{
			Method:       http.MethodGet,
			Path:         protocol + "/{version}/download/{os}/{arch}",
			CoveringTest: test,
			Reason:       protocolWhy,
		},
		{Method: http.MethodGet, Path: protocol + "/{version}/files/{sha256}", CoveringTest: test, Reason: protocolWhy},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/versions",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/{version}/download",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
		{
			Method:       http.MethodGet,
			Path:         moduleProtocol + "/{version}/files/{sha256}",
			CoveringTest: moduleTest,
			Reason:       moduleProtocolWhy,
		},
	}
}
