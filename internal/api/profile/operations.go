package profile

import (
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
)

// tagProfiles is the tag of every operation of this adapter.
const tagProfiles = "profiles"

// operationLabels names the routes of this adapter. The key is the pattern of
// the route. The Idempotency-Key flag comes from Route.IdempotencyKey, so no
// label sets it.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, profilesPath): apidesc.Name("listProfiles", tagProfiles).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listResponse]("ListProfilesResponse", profileNames...)),
	apidesc.Pattern(http.MethodPost, profilesPath): apidesc.Name("createProfile", tagProfiles).WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[createBody]("CreateProfileRequest"), profileBody),
	apidesc.Pattern(http.MethodGet, profilePath): apidesc.Name("getProfile", tagProfiles).WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, profileBody),
	apidesc.Pattern(http.MethodDelete, profilePath): apidesc.Name("deleteProfile", tagProfiles).WithIfMatch().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, revisionsPath): apidesc.Name("listProfileRevisions", tagProfiles).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listRevisionsResponse]("ListProfileRevisionsResponse", profileNames...)),
	apidesc.Pattern(http.MethodPost, revisionsPath): apidesc.Name("createProfileRevision", tagProfiles).
		WithIfMatchRequired().
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[saveBody]("CreateProfileRevisionRequest"), profileBody),
	apidesc.Pattern(http.MethodGet, revisionsPath+"/{revision}"): apidesc.Name("getProfileRevision", tagProfiles).
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[revisionEntry]("ProfileRevision", profileNames...)),
}

// The bodies that several routes share.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	profileBody  = apidesc.Typed[profileEntry]("Profile", profileNames...)
	profileNames = []apidesc.Declaration{
		apidesc.Named[revisionEntry]("ProfileRevision"),
		apidesc.Named[entryValue]("ProfileEntry"),
		apidesc.Declare[jsonValue](map[string]any{"description": "Any JSON value."}),
	}
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
			Public: false, IdempotencyKey: rt.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
