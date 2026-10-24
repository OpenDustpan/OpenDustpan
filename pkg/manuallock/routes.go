package manuallock

import (
	"fmt"
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/pkg/collection"
)

// The routes of the lock API that remove a manual lock. A refusal names them,
// because the lock of a tool never removes a manual lock.
const (
	unitLockRoute       = "/api/inventory/unit-lock"
	collectionLockRoute = "/api/dustpan/v1/namespaces/%s/collections/%s/lock"
	namespaceLockRoute  = "/api/dustpan/v1/namespaces/%s/lock"
)

// RemovalRoutes holds the routes that remove a manual lock. Remove is the
// route of the holder. Force is the route of a person with unit:manage.
type RemovalRoutes struct {
	Remove string
	Force  string
}

// RemovalRoutes returns the routes that remove the manual lock of t: the
// collection lock routes, the namespace lock routes, or else the unit lock
// routes.
func (t Target) RemovalRoutes() RemovalRoutes {
	if t.Kind == KindCollection {
		remove := fmt.Sprintf(collectionLockRoute, url.PathEscape(t.Collection.Namespace.String()),
			url.PathEscape(t.Collection.Name.String()))

		return RemovalRoutes{Remove: remove, Force: remove + "/force-unlock"}
	}

	if t.Kind == KindNamespace {
		remove := fmt.Sprintf(namespaceLockRoute, url.PathEscape(t.Space.String()))

		return RemovalRoutes{Remove: remove, Force: remove + "/force-unlock"}
	}

	unit := unitQueryValue(t.Unit)

	return RemovalRoutes{
		Remove: unitLockRoute + "?unit=" + unit,
		Force:  unitLockRoute + "/force-unlock?unit=" + unit,
	}
}

// unitQueryValue escapes the unit reference for a query. A colon and a slash
// are legal in a query, so they stay readable for the person who copies the
// route. Every other reserved character stays escaped.
func unitQueryValue(u collection.UnitRef) string {
	return strings.NewReplacer("%3A", ":", "%2F", "/").Replace(url.QueryEscape(u.String()))
}
