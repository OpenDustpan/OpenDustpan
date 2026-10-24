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
		WithQuery(
			apidesc.StringParam("folder", "Keeps the profiles in this folder."),
			apidesc.IntegerParam("pageSize", "The largest number of profiles on the page."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before, for the same folder."),
		).
		WithBodies(apidesc.NoBody, apidesc.Typed[listResponse]("ListProfilesResponse", profileNames...)),
	apidesc.Pattern(http.MethodPost, profilesPath): apidesc.Name("createProfile", tagProfiles).WithETag().
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[createBody]("CreateProfileRequest"), profileBody),
	apidesc.Pattern(http.MethodGet, profilePath): apidesc.Name("getProfile", tagProfiles).WithETag().
		On(apidesc.KindNamespace).
		WithQuery(showSecretsQuery).
		WithBodies(apidesc.NoBody, profileBody),
	apidesc.Pattern(http.MethodDelete, profilePath): apidesc.Name("deleteProfile", tagProfiles).WithIfMatch().
		On(apidesc.KindNamespace).
		WithQuery(apidesc.StringParam("reason", "Why the profile is deleted. The audit record holds it.")).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, revisionsPath): apidesc.Name("listProfileRevisions", tagProfiles).
		On(apidesc.KindNamespace).
		WithQuery(
			apidesc.IntegerParam("pageSize", "The largest number of revisions on the page."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
		).
		WithBodies(apidesc.NoBody, apidesc.Typed[listRevisionsResponse]("ListProfileRevisionsResponse", profileNames...)),
	apidesc.Pattern(http.MethodPost, revisionsPath): apidesc.Name("createProfileRevision", tagProfiles).
		WithStatus(http.StatusCreated).
		WithIfMatchRequired().
		WithETag().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[saveBody]("CreateProfileRevisionRequest"), profileBody),
	apidesc.Pattern(http.MethodGet, revisionsPath+"/{revision}"): apidesc.Name("getProfileRevision", tagProfiles).
		WithETag().
		On(apidesc.KindNamespace).
		WithQuery(showSecretsQuery).
		WithBodies(apidesc.NoBody, apidesc.Typed[revisionEntry]("ProfileRevision", profileNames...)),
}

// showSecretsQuery is the query parameter of a read of a profile that shows
// the values of its secrets.
//
//nolint:gochecknoglobals // A fixed value; Go has no constant structs.
var showSecretsQuery = apidesc.BooleanParam(
	"showSecrets", "Shows the values of the secrets. The caller needs the permission to reveal them.",
)

// queryOf returns the query of a request, as the label of its route allows
// the handler to read it.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, operationLabels)
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

	for _, route := range h.routes {
		actions := make([]string, 0, len(route.Actions))
		for _, action := range route.Actions {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, NamespaceActions: nil,
			Public: false, IdempotencyKey: route.IdempotencyKey,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}
