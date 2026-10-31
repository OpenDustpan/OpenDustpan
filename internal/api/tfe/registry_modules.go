package tfe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// The module registry protocol of OpenTofu lives under this path. The service
// discovery document names it as modules.v1.
const modulesPrefix = "/v1/modules/"

// moduleURLLabel separates the signature of a module file URL from the
// signature of a provider file URL and from every other use of the key.
const moduleURLLabel = "opendustpan registry module file url v1"

// moduleAddress is the module part of a path: the name, the system and the
// version of a module in a namespace.
type moduleAddress struct {
	ns      namespace.Name
	name    string
	system  string
	version string
}

// moduleOf reads the module part of the path of r.
func moduleOf(r *http.Request, ns namespace.Name) moduleAddress {
	return moduleAddress{
		ns: ns, name: r.PathValue("name"), system: r.PathValue("system"), version: r.PathValue("version"),
	}
}

// address returns the registry address of the module.
func (a moduleAddress) address() registry.Address {
	return registry.Address{Namespace: a.ns, Kind: registry.KindModule, Name: a.name, System: a.system}
}

// moduleRoutes returns the routes of the module registry protocol.
func (h *Handler) moduleRoutes() []Route {
	if h.registry == nil {
		return nil
	}

	base := modulesPrefix + "{namespace}/{name}/{system}"

	return []Route{
		{
			Method: http.MethodGet,
			Path:   base + "/versions",
			// The list of versions needs registry:read on the namespace. A host
			// other than the configured host answers 404.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.moduleVersions,
		},
		{
			Method: http.MethodGet,
			Path:   base + "/{version}/download",
			// The download answer needs registry:read on the namespace. It holds
			// the signed URL of the archive in X-Terraform-Get.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.moduleDownload,
		},
		{
			Method: http.MethodGet,
			Path:   base + "/{version}/files/{sha256}",
			// OpenTofu fetches the archive without credentials, so the signature
			// in the query is the credential: the download answer minted it for a
			// caller who held registry:read. It expires, and it names one archive
			// of one release.
			Scope:   ScopeSigned,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: nil,
			Serve:   h.moduleFile,
		},
	}
}

// moduleVersion is one version in the version list of a module.
type moduleVersion struct {
	Version string `json:"version"`
}

// moduleVersionList is the answer to the version list of a module. The
// protocol wraps the list in a list of modules that has one element.
type moduleVersionList struct {
	Modules []moduleVersions `json:"modules"`
}

type moduleVersions struct {
	Versions []moduleVersion `json:"versions"`
}

// moduleVersions serves GET /v1/modules/{namespace}/{name}/{system}/versions.
func (h *Handler) moduleVersions(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()
	addr := moduleOf(r, obj.Namespace())

	releases, err := h.registry.service.ModuleReleases(ctx, addr.address())
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	list := moduleVersions{Versions: make([]moduleVersion, 0, len(releases))}
	for i := range releases {
		list.Versions = append(list.Versions, moduleVersion{Version: releases[i].Version})
	}

	h.writeRegistryJSON(ctx, w, moduleVersionList{Modules: []moduleVersions{list}})
}

// moduleDownload serves GET /v1/modules/{namespace}/{name}/{system}/{version}/download.
// It answers 204 with the signed URL of the archive in X-Terraform-Get, as the
// protocol asks.
func (h *Handler) moduleDownload(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()
	addr := moduleOf(r, obj.Namespace())

	rel, err := h.registry.service.ModuleRelease(ctx, addr.ns, addr.name, addr.system, addr.version)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	// Defense in depth: Validate keeps a module release to one archive. This
	// check stays so that a store that returns another shape serves nothing.
	if len(rel.Archives) != 1 {
		h.registryNotFound(ctx, w)

		return
	}

	archive := rel.Archives[0]
	expires := h.registry.now().Add(h.registry.ttl)

	w.Header().Set("X-Terraform-Get", h.registry.moduleFileURL(h.baseURL.BaseURL(r), addr, archive, expires))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// archiveHint returns the value of the archive query member that tells the
// client how to unpack the file. The file name has the extension of the
// format. The signed URL has no extension of its own.
func archiveHint(fileName string) string {
	switch {
	case strings.HasSuffix(fileName, ".tar.gz"):
		return "tar.gz"
	case strings.HasSuffix(fileName, ".zip"):
		return "zip"
	default:
		return ""
	}
}

// moduleMAC returns the signature of one module file URL: the HMAC-SHA-256,
// keyed with the key of the handler, of the label, the address, the version,
// the file and the expiry. The fields are joined by a newline, which no field
// can hold. The label is not the label of a provider file, so a signature of
// one kind never opens a file of the other.
func (s *registryState) moduleMAC(addr moduleAddress, sum string, expires int64) []byte {
	mac := hmac.New(sha256.New, s.key)

	parts := []string{
		moduleURLLabel, addr.ns.String(), addr.name, addr.system, addr.version, sum, strconv.FormatInt(expires, 10),
	}

	for _, part := range parts {
		mac.Write([]byte(part))
		mac.Write([]byte{'\n'})
	}

	return mac.Sum(nil)
}

// moduleFileURL returns the signed URL of the archive of a module release. The
// archive member tells the client the format. The signature does not cover it:
// it changes how the client unpacks the file, and nothing else.
func (s *registryState) moduleFileURL(base string, addr moduleAddress, a registry.Archive, expires time.Time) string {
	expSec := expires.Unix()
	sig := base64.RawURLEncoding.EncodeToString(s.moduleMAC(addr, a.SHA256, expSec))

	return base + modulesPrefix + url.PathEscape(addr.ns.String()) + "/" + url.PathEscape(addr.name) + "/" +
		url.PathEscape(addr.system) + "/" + url.PathEscape(addr.version) + "/files/" + url.PathEscape(a.SHA256) +
		"?archive=" + archiveHint(a.FileName) + "&expires=" + strconv.FormatInt(expSec, 10) + "&sig=" + sig
}

// validModuleSignature reports whether the query of r holds a signature that
// the handler made for this file, and that has not expired, with the rules of
// validSignature.
func (s *registryState) validModuleSignature(r *http.Request, addr moduleAddress, sum string) bool {
	q := r.URL.Query()

	expires, err := strconv.ParseInt(q.Get("expires"), 10, 64)
	if err != nil {
		return false
	}

	now := s.now()
	if expires <= now.Unix() || expires > now.Add(s.ttl).Unix() {
		return false
	}

	sig, err := base64.RawURLEncoding.DecodeString(q.Get("sig"))
	if err != nil {
		return false
	}

	return hmac.Equal(sig, s.moduleMAC(addr, sum, expires))
}

// moduleFile serves GET /v1/modules/{namespace}/{name}/{system}/{version}/files/{sha256}.
func (h *Handler) moduleFile(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		h.registryNotFound(ctx, w)

		return
	}

	addr, sum := moduleOf(r, ns), r.PathValue("sha256")

	// The signature is checked before the store is read, so a request without a
	// valid signature learns nothing about what exists.
	if !h.registry.validModuleSignature(r, addr, sum) {
		h.registryNotFound(ctx, w)

		return
	}

	size, body, err := h.registry.service.OpenModuleFile(ctx, addr.address(), addr.version, sum)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	defer func() {
		if closeErr := body.Close(); closeErr != nil {
			h.logger.WarnContext(ctx, "close registry file failed")
		}
	}()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")

	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}

	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, body); err != nil {
		h.logger.WarnContext(ctx, "write registry file failed")
	}
}
