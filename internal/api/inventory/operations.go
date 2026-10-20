package inventory

import (
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Tags of the operations of this adapter.
const (
	tagUnits       = "units"
	tagCollections = "collections"
	tagLocks       = "locks"
	tagViews       = "views"
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
	apidesc.Pattern(http.MethodGet, namespacePath+"/units"):         apidesc.Name("listUnits", tagUnits),
	apidesc.Pattern(http.MethodGet, namespacePath+"/deleted-units"): apidesc.Name("listDeletedUnits", tagUnits),

	apidesc.Pattern(http.MethodGet, namespacePath+"/collections"):  apidesc.Name("listCollections", tagCollections),
	apidesc.Pattern(http.MethodPost, namespacePath+"/collections"): apidesc.Name("createCollection", tagCollections),
	apidesc.Pattern(http.MethodGet, collectionPath):                apidesc.Name("getCollection", tagCollections),
	apidesc.Pattern(http.MethodDelete, collectionPath):             apidesc.Name("deleteCollection", tagCollections),

	apidesc.Pattern(http.MethodPut, collectionPath+"/lock"):    apidesc.Name("lockCollection", tagLocks),
	apidesc.Pattern(http.MethodGet, collectionPath+"/lock"):    apidesc.Name("getCollectionLock", tagLocks),
	apidesc.Pattern(http.MethodDelete, collectionPath+"/lock"): apidesc.Name("unlockCollection", tagLocks),
	apidesc.Pattern(http.MethodPost, collectionPath+"/lock/force-unlock"): apidesc.Name(
		"forceUnlockCollection",
		tagLocks,
	),

	apidesc.Pattern(http.MethodPut, namespacePath+"/lock"):    apidesc.Name("lockNamespace", tagLocks),
	apidesc.Pattern(http.MethodGet, namespacePath+"/lock"):    apidesc.Name("getNamespaceLock", tagLocks),
	apidesc.Pattern(http.MethodDelete, namespacePath+"/lock"): apidesc.Name("unlockNamespace", tagLocks),
	apidesc.Pattern(http.MethodPost, namespacePath+"/lock/force-unlock"): apidesc.Name(
		"forceUnlockNamespace",
		tagLocks,
	),

	apidesc.Pattern(http.MethodPost, namespacePath+"/views"): apidesc.Name("createView", tagViews).WithKey(),
	apidesc.Pattern(http.MethodGet, namespacePath+"/views"):  apidesc.Name("listViews", tagViews),
	apidesc.Pattern(http.MethodGet, viewPath):                apidesc.Name("getView", tagViews),
	apidesc.Pattern(http.MethodDelete, viewPath):             apidesc.Name("deleteView", tagViews).WithKey(),
	apidesc.Pattern(http.MethodGet, viewPath+"/units"):       apidesc.Name("listViewUnits", tagViews),
}

// Operations returns the routes of this adapter that the description of the
// tool-neutral REST API holds: the routes under apidesc.Prefix.
//
// The route that reads one unit across namespaces is not in the list. Its
// path names the wildcard unitID, and the description allows lower case
// wildcard names with underscores only. The route joins the list when its
// wildcard is renamed.
func (h *Handler) Operations() []apidesc.Operation {
	rows := make([]apidesc.Row, 0, len(h.routes))

	for _, route := range h.routes {
		if strings.Contains(route.Path, "{unitID}") {
			continue
		}

		actions := make([]string, 0, len(route.NamespaceActions)+len(route.Actions))
		for _, action := range append(append([]identity.Action(nil), route.NamespaceActions...), route.Actions...) {
			actions = append(actions, string(action))
		}

		rows = append(rows, apidesc.Row{
			Method: route.Method, Path: route.Path, Actions: actions, Public: route.Scope == ScopePublic,
		})
	}

	return apidesc.Collect(rows, operationLabels)
}
