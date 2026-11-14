package registry

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

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

// moduleToken is the opaque page token of the module list: the key of the
// last module of the page before, as registry.AddressKey returns it.
type moduleToken struct {
	After string `json:"a"`
}

// The causes of the four refusals of a module page token. Each guard wraps
// its own cause, so the caller and a test can tell which guard refused.
var (
	errModuleTokenLength  = errors.New("module page token is over the length limit")
	errModuleTokenDecode  = errors.New("module page token does not decode")
	errModuleTokenEmpty   = errors.New("module page token holds no key")
	errModuleTokenAddress = errors.New("module page token holds no module address")
)

// moduleQuery is one validated list request: the page size and the cursor of
// the module after which the page starts. After is empty for the first page.
type moduleQuery struct {
	pageSize int
	after    string
}

// parseModuleQuery reads the pageSize and the pageToken. The token is opaque
// JSON. It is untrusted input: the key that it holds is checked against the
// same rules as the address of a module, and a value that fails is refused,
// never repaired.
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
		after, err := parseModuleToken(raw, ns)
		if err != nil {
			return moduleQuery{}, err
		}

		out.after = after
	}

	return out, nil
}

// parseModuleToken reads the cursor key of a module page token. The token is
// untrusted input: it is refused, never repaired.
func parseModuleToken(raw string, ns namespace.Name) (string, error) {
	// Defense in depth: pagetoken.Decode checks the length too; this cap
	// stays so that the handler refuses a long token before the decode.
	if len(raw) > pagetoken.MaxLen {
		return "", fmt.Errorf("%w: %w", errModuleTokenLength, errProviderPageToken)
	}

	var token moduleToken
	if err := pagetoken.Decode(raw, &token); err != nil {
		return "", fmt.Errorf("%w: %w", errModuleTokenDecode, errProviderPageToken)
	}

	// The first page carries no token: a token that holds no key is not a
	// page token of this list.
	if token.After == "" {
		return "", fmt.Errorf("%w: %w", errModuleTokenEmpty, errProviderPageToken)
	}

	// Defense in depth: the store checks the cursor too; this check stays so
	// that the handler refuses a bad token before the store call.
	if _, err := registry.ParseAddressKey(ns, registry.KindModule, token.After); err != nil {
		return "", fmt.Errorf("%w: %w", errModuleTokenAddress, errProviderPageToken)
	}

	return token.After, nil
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

	addrs, next, err := h.service.Modules(ctx, obj.Namespace(), query.after, query.pageSize)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	page := listModulesResponse{Modules: make([]moduleDocument, 0, len(addrs)), NextPageToken: ""}

	for _, a := range addrs {
		page.Modules = append(
			page.Modules,
			moduleDocument{Namespace: a.Namespace.String(), Name: a.Name, System: a.System},
		)
	}

	if next != "" {
		token, encodeErr := pagetoken.Encode(moduleToken{After: next})
		if encodeErr != nil {
			h.writeError(ctx, w, encodeErr)

			return
		}

		page.NextPageToken = token
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
