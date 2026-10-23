package registry

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagRegistry is the tag of every operation of this adapter.
const tagRegistry = "registry"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it. No route takes If-Match, and no answer holds an ETag.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	// The body of an upload is the raw bytes of one file, with the media type
	// application/octet-stream. The description holds JSON bodies only, so it
	// does not show this body.
	apidesc.Pattern(http.MethodPost, uploadsPath): apidesc.Name("stageRegistryUpload", tagRegistry).
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[stagedDocument]("StagedUpload")),
	apidesc.Pattern(http.MethodPut, versionPath): apidesc.Name("publishProviderRelease", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[publishBody]("PublishProviderReleaseRequest", apidesc.Named[publishFile]("PublishFile")),
			releaseBody),
	apidesc.Pattern(http.MethodGet, providersPath): apidesc.Name("listRegistryProviders", tagRegistry).
		On(apidesc.KindNamespace).
		WithQuery(
			apidesc.IntegerParam("pageSize", "The largest number of providers on the page."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
		).
		WithBodies(apidesc.NoBody, apidesc.Typed[listProvidersResponse](
			"ListRegistryProvidersResponse", apidesc.Named[providerDocument]("RegistryProvider"),
		)),
	apidesc.Pattern(http.MethodGet, versionsPath): apidesc.Name("listProviderReleases", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listReleasesResponse](
			"ListProviderReleasesResponse", releaseNames...,
		)),
	apidesc.Pattern(http.MethodGet, versionPath): apidesc.Name("getProviderRelease", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, releaseBody),
	apidesc.Pattern(http.MethodPost, keysPath): apidesc.Name("addRegistryKey", tagRegistry).
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[keyBody]("AddRegistryKeyRequest"), keyResponse),
	apidesc.Pattern(http.MethodGet, keysPath): apidesc.Name("listRegistryKeys", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listKeysResponse]("ListRegistryKeysResponse",
			apidesc.Named[keyDocument]("RegistryKey"))),
	apidesc.Pattern(http.MethodDelete, keyPath): apidesc.Name("removeRegistryKey", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
}

// The bodies that several routes share.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	releaseNames = []apidesc.Declaration{apidesc.Named[releaseFile]("ProviderReleaseFile")}
	releaseBody  = apidesc.Typed[releaseDocument]("ProviderRelease", releaseNames...)
	keyResponse  = apidesc.Typed[keyDocument]("RegistryKey")
)

// queryOf returns the query of a request, as the label of its route allows
// the handler to read it.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, operationLabels)
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Actions))
		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, NamespaceActions: nil,
			Public: route.Scope == ScopePublic, IdempotencyKey: route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}
