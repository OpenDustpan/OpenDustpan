package tfe

import (
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/registry"
)

// The provider network mirror of OpenTofu lives under this path. It is not
// named in service discovery: a person writes it in the network_mirror block
// of the CLI configuration. It lives outside /api/ and outside the API
// description, next to the provider registry protocol.
const mirrorPrefix = "/v1/mirror/"

// mirrorRoutes returns the routes of the provider network mirror.
func (h *Handler) mirrorRoutes() []Route {
	if h.registry == nil {
		return nil
	}

	return []Route{
		{
			Method: http.MethodGet,
			Path:   mirrorPrefix + "{hostname}/{namespace}/{type}/index.json",
			// The index needs registry:read on the namespace. It lists the
			// active, servable versions of the provider: a version whose key
			// the namespace removed is left out. A hostname other than the
			// configured host answers 404.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.mirrorIndex,
		},
		{
			Method: http.MethodGet,
			Path:   mirrorPrefix + "{hostname}/{namespace}/{type}/{file}",
			// The version document needs registry:read on the namespace. The
			// file segment holds "<version>.json": every other value answers
			// 404. Each archive URL is the signed file URL of the provider
			// registry protocol, minted for a caller who held registry:read.
			// A hostname other than the configured host answers 404.
			Scope:   ScopeRegistry,
			Actions: []identity.Action{identity.ActionRegistryRead},
			Resolve: resolveRegistryNamespace,
			Serve:   h.mirrorVersion,
		},
	}
}

// mirrorHostAllowed reports whether the hostname of a mirror path is the
// configured host of the server, exactly, as hostAllowed does for the host of
// the request. The CLI appends the hostname of the provider address to the
// mirror URL, so the path names the same host that the request reached.
func (s *registryState) mirrorHostAllowed(hostname string) bool {
	return s.host != "" && s.canonicalHost(hostname) == s.host
}

// mirrorIndex serves GET /v1/mirror/{hostname}/{namespace}/{type}/index.json.
func (h *Handler) mirrorIndex(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	// Defense in depth: enforceRegistry checks the host of the request, and
	// this check stays so that a renamed or remounted route still refuses a
	// hostname outside the configuration; it does not rely on the request
	// host.
	if !h.registry.mirrorHostAllowed(r.PathValue("hostname")) {
		h.denyHost(w, r)

		return
	}

	// The mirror index lists the servable releases: the same set that the
	// version list of the team protocol serves through ServableReleases.
	// service.MirrorIndex pins the same read contract with its own tests.
	releases, err := h.registry.service.ServableReleases(ctx, obj.Namespace(), r.PathValue("type"))
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	versions := make(map[string]struct{}, len(releases))
	for i := range releases {
		versions[releases[i].Version] = struct{}{}
	}

	h.writeRegistryJSON(ctx, w, mirrorIndex{Versions: versions})
}

// mirrorIndex is the index document of the provider network mirror. Each
// version maps to an empty object.
type mirrorIndex struct {
	Versions map[string]struct{} `json:"versions"`
}

// mirrorVersion serves GET /v1/mirror/{hostname}/{namespace}/{type}/{file}
// for {file} = "<version>.json".
func (h *Handler) mirrorVersion(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	// Defense in depth: enforceRegistry checks the host of the request, and
	// this check stays so that a renamed or remounted route still refuses a
	// hostname outside the configuration; it does not rely on the request
	// host.
	if !h.registry.mirrorHostAllowed(r.PathValue("hostname")) {
		h.denyHost(w, r)

		return
	}

	// ServeMux takes no wildcard inside a segment, so the route holds the
	// whole last segment. Only "<version>.json" on a valid version is served:
	// every other value is refused (fail closed) with no store read.
	file := r.PathValue("file")

	version, ok := strings.CutSuffix(file, ".json")
	if !ok || registry.ValidateVersion(version) != nil {
		h.registryNotFound(ctx, w)

		return
	}

	rel, err := h.registry.service.Release(ctx, obj.Namespace(), r.PathValue("type"), version)
	if err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	// Fail closed: the version document is served only while the namespace
	// trusts the key that signed the release. A removed key answers 404, as
	// for the download document of the team protocol.
	if _, err := h.registry.service.SigningKey(ctx, rel); err != nil {
		h.registryServiceError(ctx, w, err)

		return
	}

	base := h.baseURL.BaseURL(r)
	expires := h.registry.now().Add(h.registry.ttl)

	archives := make(map[string]mirrorArchive, len(rel.Archives))
	for _, a := range rel.Archives {
		sum := registryFile{obj.Namespace(), r.PathValue("type"), version, a.SHA256}
		archives[a.Platform] = mirrorArchive{
			URL: h.registry.fileURL(base, sum, expires),
			// The SHA-256 of the stored archive bytes, which the server
			// checked at publish. The server does not unpack an archive, so
			// it serves no h1 hash.
			Hashes: []string{"zh:" + a.SHA256},
		}
	}

	h.writeRegistryJSON(ctx, w, mirrorVersion{Archives: archives})
}

// mirrorVersion is the version document of the provider network mirror.
type mirrorVersion struct {
	// Archives holds one entry per platform, for example "linux_amd64".
	Archives map[string]mirrorArchive `json:"archives"`
}

// mirrorArchive is one platform in a version document: the signed URL of the
// archive and its hashes.
type mirrorArchive struct {
	// URL is the signed file URL of the provider registry protocol.
	URL string `json:"url"`
	// Hashes holds the "zh:" SHA-256 of the archive.
	Hashes []string `json:"hashes"`
}
