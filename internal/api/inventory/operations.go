package inventory

import (
	"net/http"
	"slices"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/route"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Tags of the operations of this adapter.
const (
	tagUnits       = "units"
	tagCollections = "collections"
	tagLocks       = "locks"
	tagViews       = "views"
	tagSharing     = "output-sharing"
)

const (
	namespacePath  = v1Prefix + "/namespaces/{namespace}"
	collectionPath = namespacePath + "/collections/{collection}"
	viewPath       = namespacePath + "/views/{view_id}"
)

// operationLabels names the routes of this adapter that sit under
// apidesc.Prefix. The key is the pattern of the route.
//
//nolint:gochecknoglobals // A fixed table; Go has no constant maps.
var operationLabels = map[string]apidesc.Label{
	apidesc.Pattern(http.MethodGet, namespacePath+"/units"): apidesc.Name("listUnits", tagUnits).
		On(apidesc.KindNamespace).
		WithQuery(append(slices.Clone(unitPageQuery),
			apidesc.StringParam("label", "Keeps the units with this label, as key=value. All label values must match.").
				Repeat(),
			apidesc.StringParam("reference", "Keeps the unit with this reference in the namespace.").Repeat(),
			apidesc.StringParam("state", "Keeps the units in this state. One of the states must match.").Repeat(),
			apidesc.IntegerParam("at", "Lists the units as they were at this time, in Unix milliseconds. "+
				"It takes no filter and no page token."),
		)...).
		WithBodies(apidesc.NoBody, unitList),
	apidesc.Pattern(http.MethodGet, namespacePath+"/deleted-units"): apidesc.Name("listDeletedUnits", tagUnits).
		On(apidesc.KindNamespace).
		WithQuery(
			apidesc.IntegerParam("pageSize", "The largest number of deleted units on the page."),
			apidesc.StringParam("pageToken", "The nextPageToken of the page before."),
			apidesc.StringParam("tool", "Keeps the units of this tool."),
			apidesc.StringParam("namePrefix", "Keeps the units whose name starts with this text."),
			apidesc.IntegerParam("purgeWithinDays", "Keeps the units that the server purges within this number of days."),
		).
		WithBodies(apidesc.NoBody, apidesc.Typed[listDeletedUnitsResponse](
			"ListDeletedUnitsResponse", apidesc.Named[deletedUnitEntry]("DeletedUnit"),
		)),

	apidesc.Pattern(http.MethodGet, namespacePath+"/collections"): apidesc.Name("listCollections", tagCollections).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[collectionsResponse](
			"ListCollectionsResponse", apidesc.Named[collectionEntry]("Collection"),
		)),
	apidesc.Pattern(http.MethodPost, namespacePath+"/collections"): apidesc.Name("createCollection", tagCollections).
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[createCollectionRequest]("CreateCollectionRequest"), collectionBody),
	apidesc.Pattern(http.MethodGet, collectionPath): apidesc.Name("getCollection", tagCollections).
		On(apidesc.KindCollection).
		WithBodies(apidesc.NoBody, collectionBody),
	apidesc.Pattern(http.MethodDelete, collectionPath): apidesc.Name("deleteCollection", tagCollections).
		On(apidesc.KindCollection).
		WithBodies(apidesc.NoBody, apidesc.NoBody),

	apidesc.Pattern(http.MethodPut, collectionPath+"/lock"): apidesc.Name("lockCollection", tagLocks).
		On(apidesc.KindCollection).
		WithBodies(lockRequestBody, manualLockBody),
	apidesc.Pattern(http.MethodGet, collectionPath+"/lock"): apidesc.Name("getCollectionLock", tagLocks).
		On(apidesc.KindCollection).
		WithBodies(apidesc.NoBody, manualLockBody),
	apidesc.Pattern(http.MethodDelete, collectionPath+"/lock"): apidesc.Name("unlockCollection", tagLocks).
		On(apidesc.KindCollection).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodPost, collectionPath+"/lock/force-unlock"): apidesc.Name(
		"forceUnlockCollection",
		tagLocks,
	).On(apidesc.KindCollection).WithBodies(apidesc.NoBody, apidesc.NoBody),

	apidesc.Pattern(http.MethodPut, namespacePath+"/lock"): apidesc.Name("lockNamespace", tagLocks).
		On(apidesc.KindNamespace).
		WithBodies(lockRequestBody, manualLockBody),
	apidesc.Pattern(http.MethodGet, namespacePath+"/lock"): apidesc.Name("getNamespaceLock", tagLocks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, manualLockBody),
	apidesc.Pattern(http.MethodDelete, namespacePath+"/lock"): apidesc.Name("unlockNamespace", tagLocks).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodPost, namespacePath+"/lock/force-unlock"): apidesc.Name(
		"forceUnlockNamespace",
		tagLocks,
	).On(apidesc.KindNamespace).WithBodies(apidesc.NoBody, apidesc.NoBody),

	apidesc.Pattern(http.MethodGet, outputSharingPath): apidesc.Name("getOutputSharing", tagSharing).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, outputSharingBody),
	apidesc.Pattern(http.MethodPut, outputSharingPath): apidesc.Name("setOutputSharing", tagSharing).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[outputSharingRequest]("OutputSharingRequest"), outputSharingBody),

	apidesc.Pattern(http.MethodPost, namespacePath+"/views"): apidesc.Name("createView", tagViews).WithKey().
		WithStatus(http.StatusCreated).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.Typed[createViewBody]("CreateViewRequest", viewNames...), viewBody),
	apidesc.Pattern(http.MethodGet, namespacePath+"/views"): apidesc.Name("listViews", tagViews).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.Typed[listViewsResponse]("ListViewsResponse", viewNames...)),
	apidesc.Pattern(http.MethodGet, viewPath): apidesc.Name("getView", tagViews).
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, viewBody),
	apidesc.Pattern(http.MethodDelete, viewPath): apidesc.Name("deleteView", tagViews).WithKey().
		On(apidesc.KindNamespace).
		WithBodies(apidesc.NoBody, apidesc.NoBody),
	apidesc.Pattern(http.MethodGet, viewPath+"/units"): apidesc.Name("listViewUnits", tagViews).
		On(apidesc.KindNamespace).
		WithQuery(unitPageQuery...).
		WithBodies(apidesc.NoBody, unitList),
}

// unitPageQuery is the query of a page of units: the unit list and the units
// of a view.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant slices.
var unitPageQuery = []apidesc.QueryParam{
	apidesc.IntegerParam("pageSize", "The largest number of units on the page."),
	apidesc.StringParam("pageToken", "The nextPageToken of the page before, for the same query."),
}

// queryOf returns the query of a request, as the label of its route allows
// the handler to read it.
func queryOf(r *http.Request) apidesc.Query {
	return apidesc.QueryOf(r, operationLabels)
}

// The bodies that several routes share. The types of the bodies are the types
// that the handlers write and read, so the description cannot drift from
// them.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	// unitList is the page of units, for the unit list and for a view.
	unitList = apidesc.Typed[listUnitsResponse](
		"ListUnitsResponse",
		apidesc.Named[unitEntry]("Unit"),
		apidesc.Named[stateEntry]("UnitState"),
		apidesc.Named[lockEntry]("UnitLock"),
	)
	collectionBody  = apidesc.Typed[collectionEntry]("Collection")
	lockRequestBody = apidesc.Typed[lockRequest]("LockRequest")
	manualLockBody  = apidesc.Typed[lockEntryDocument]("ManualLock")
	viewBody        = apidesc.Typed[viewDocument]("View", viewNames...)
	// outputSharingBody is the output sharing setting of a namespace.
	outputSharingBody = apidesc.Typed[outputSharingDocument]("OutputSharing")
	viewNames         = []apidesc.Declaration{
		apidesc.Named[viewQuery]("ViewQuery"),
		apidesc.Named[viewLabel]("ViewLabel"),
	}
)

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
//
// The route that reads one unit across namespaces is not in the list. It is
// on apidesc.Undescribed with the reason, and apidesc.Collect leaves it out by
// its exact pattern.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, rt := range h.routes {
		actions := make([]string, 0, len(rt.NamespaceActions)+len(rt.Actions))
		for _, action := range append(append([]identity.Action(nil), rt.NamespaceActions...), rt.Actions...) {
			actions = append(actions, string(action))
		}

		namespaceActions := make([]string, len(rt.NamespaceActions))
		for i, action := range rt.NamespaceActions {
			namespaceActions[i] = string(action)
		}

		rows = append(rows, apidesc.Row{
			Method: rt.Method, Path: rt.Path, Actions: actions, NamespaceActions: namespaceActions,
			Public: rt.Scope == route.ScopePublic, IdempotencyKey: false,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}

// userAgent returns the User-Agent header of a request, for the audit record.
func userAgent(r *http.Request) string {
	return apidesc.Header(r, "User-Agent")
}
