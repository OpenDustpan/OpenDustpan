package registry

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
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
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[stagedDocument]("StagedUpload")),
	apidesc.Pattern(http.MethodPut, versionPath): apidesc.Name("publishProviderRelease", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[publishBody]("PublishProviderReleaseRequest", apidesc.Named[publishFile]("PublishFile")),
			releaseBody),
	apidesc.Pattern(http.MethodGet, versionsPath): apidesc.Name("listProviderReleases", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listReleasesResponse](
			"ListProviderReleasesResponse", releaseNames...,
		)),
	apidesc.Pattern(http.MethodGet, versionPath): apidesc.Name("getProviderRelease", tagRegistry).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, releaseBody),
	apidesc.Pattern(http.MethodPost, keysPath): apidesc.Name("addRegistryKey", tagRegistry).
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

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, len(rt.Actions))
		for _, action := range rt.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: nil,
			Public: rt.Scope == route.ScopePublic, IdempotencyKey: rt.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
