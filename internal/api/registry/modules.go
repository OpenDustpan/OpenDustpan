package registry

import (
	"encoding/base64"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/internal/registry/service"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/registry"
)

// Wire shapes of the module routes.
//
// Contract: never remove or rename these fields.
type publishModuleBody struct {
	Upload string `json:"upload"`
}

type moduleArchiveDocument struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type moduleReleaseDocument struct {
	Namespace   string                `json:"namespace"`
	Name        string                `json:"name"`
	System      string                `json:"system"`
	Version     string                `json:"version"`
	PublishedAt string                `json:"publishedAt"`
	PublishedBy string                `json:"publishedBy"`
	Archive     moduleArchiveDocument `json:"archive"`
}

type listModuleReleasesResponse struct {
	Releases []moduleReleaseDocument `json:"releases"`
}

// moduleDocument is one module of a namespace in a list.
type moduleDocument struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	System    string `json:"system"`
}

// listModulesResponse is one page of GET .../registry/modules. Modules is never
// null. NextPageToken is empty on the last page.
type listModulesResponse struct {
	Modules       []moduleDocument `json:"modules"`
	NextPageToken string           `json:"nextPageToken"`
}

func toModuleRelease(r registry.Release) moduleReleaseDocument {
	// A module release has one archive, and Validate checks it. A release without
	// one gives an empty archive, never a panic.
	var archive moduleArchiveDocument
	if len(r.Archives) > 0 {
		archive = moduleArchiveDocument{
			Name:   r.Archives[0].FileName,
			SHA256: r.Archives[0].SHA256,
			Size:   r.Archives[0].Size,
		}
	}

	return moduleReleaseDocument{
		Namespace: r.Address.Namespace.String(), Name: r.Address.Name, System: r.Address.System, Version: r.Version,
		PublishedAt: formatTime(r.PublishedAt), PublishedBy: r.PublishedBy, Archive: archive,
	}
}

// publishModule serves PUT .../registry/modules/{name}/{system}/versions/{version}.
func (h *Handler) publishModule(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	var body publishModuleBody
	if err := readBody(w, r, &body); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rel, err := h.service.PublishModule(ctx, callerOf(r, p), service.ModulePublishRequest{
		Namespace: obj.Namespace(), Name: r.PathValue("name"), System: r.PathValue("system"),
		Version: r.PathValue("version"), Upload: body.Upload,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.send(ctx, w, moduleReleaseAnswer(obj), rel)
}

// moduleReleaseAnswer answers a module publish: 200 with the release. A repeat
// of a published version answers 200 too.
func moduleReleaseAnswer(identity.Object) answerFunc {
	return func(result any) (idempotency.Answer, error) {
		rel, ok := result.(registry.Release)
		if !ok {
			return idempotency.Answer{}, errResult
		}

		return jsonAnswer(http.StatusOK, toModuleRelease(rel))
	}
}

// moduleQuery is one validated list request: the page size and the module
// after which the page starts. After is the zero address for the first page.
type moduleQuery struct {
	pageSize int
	after    registry.Address
	hasAfter bool
}

// moduleTokenSeparator joins the name and the system in a page token.
const moduleTokenSeparator = "/"

// parseModuleQuery reads the pageSize and the pageToken. The token is the
// base64url form of "<name>/<system>". It is untrusted input: it is checked
// against the same rules as the address of a module, and a value that fails is
// refused, never repaired.
func parseModuleQuery(r *http.Request, ns namespace.Name) (moduleQuery, error) {
	q := queryOf(r)

	var out moduleQuery

	out.pageSize = defaultProviderPage

	if raw := q.Get("pageSize"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxProviderPage {
			return moduleQuery{}, errProviderPageSize
		}

		out.pageSize = n
	}

	if raw := q.Get("pageToken"); raw != "" {
		// Defense in depth: the address check below refuses a long name too; this
		// cap stays so that the decode never reads more than a token can hold.
		if len(raw) > pagetoken.MaxLen {
			return moduleQuery{}, errProviderPageToken
		}

		decoded, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil {
			return moduleQuery{}, errProviderPageToken
		}

		name, system, ok := strings.Cut(string(decoded), moduleTokenSeparator)
		addr := registry.Address{Namespace: ns, Kind: registry.KindModule, Name: name, System: system}

		if !ok || addr.Validate() != nil {
			return moduleQuery{}, errProviderPageToken
		}

		out.after, out.hasAfter = addr, true
	}

	return out, nil
}

// moduleNotAfter reports whether a sorts before or equal to b by name and system.
func moduleNotAfter(a, b registry.Address) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}

	return a.System <= b.System
}

// listModules serves GET .../registry/modules: one page of the modules of the
// namespace that have at least one release, ordered by name and system.
func (h *Handler) listModules(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	query, err := parseModuleQuery(r, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	addrs, err := h.service.Modules(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The addresses are in order, so the page starts at the first address after
	// the token. A token that names no module still pages from where it would be.
	start := 0
	if query.hasAfter {
		start = sort.Search(len(addrs), func(i int) bool { return !moduleNotAfter(addrs[i], query.after) })
	}

	end := min(start+query.pageSize, len(addrs))
	page := listModulesResponse{Modules: make([]moduleDocument, 0, end-start), NextPageToken: ""}

	for _, a := range addrs[start:end] {
		page.Modules = append(
			page.Modules,
			moduleDocument{Namespace: a.Namespace.String(), Name: a.Name, System: a.System},
		)
	}

	if end < len(addrs) {
		last := addrs[end-1]
		page.NextPageToken = base64.RawURLEncoding.EncodeToString(
			[]byte(last.Name + moduleTokenSeparator + last.System),
		)
	}

	h.writeJSON(ctx, w, http.StatusOK, page)
}

// listModuleReleases serves GET .../registry/modules/{name}/{system}/versions.
func (h *Handler) listModuleReleases(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	found, err := h.service.ModuleReleases(ctx, registry.Address{
		Namespace: obj.Namespace(), Kind: registry.KindModule, Name: r.PathValue("name"), System: r.PathValue("system"),
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := make([]moduleReleaseDocument, 0, len(found))
	for i := range found {
		out = append(out, toModuleRelease(found[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, listModuleReleasesResponse{Releases: out})
}

// getModuleRelease serves GET .../registry/modules/{name}/{system}/versions/{version}.
func (h *Handler) getModuleRelease(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	rel, err := h.service.ModuleRelease(
		ctx, obj.Namespace(), r.PathValue("name"), r.PathValue("system"), r.PathValue("version"),
	)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, toModuleRelease(rel))
}
