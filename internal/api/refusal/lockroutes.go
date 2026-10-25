package refusal

import (
	"net/url"
	"strings"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
)

// The routes of the manual lock API. The inventory adapter registers its lock
// routes with these paths, and a refusal of a manual lock names them, because
// the lock of a tool never removes a manual lock.
const (
	// UnitLockPath is the route of the manual lock of one unit. The unit query
	// value names the unit.
	UnitLockPath = "/api/inventory/unit-lock"
	// CollectionLockPath is the route of the manual lock of one collection.
	CollectionLockPath = "/api/dustpan/v1/namespaces/{namespace}/collections/{collection}/lock"
	// NamespaceLockPath is the route of the manual lock of a whole namespace.
	NamespaceLockPath = "/api/dustpan/v1/namespaces/{namespace}/lock"
	// ForceUnlockSuffix follows a lock route in the route of a force-unlock.
	ForceUnlockSuffix = "/force-unlock"
)

// LockRoutes holds the routes that remove a manual lock. Remove is the route
// of the holder. Force is the route of a person with unit:manage.
type LockRoutes struct {
	Remove string
	Force  string
}

// RemovalRoutes returns the routes that remove the manual lock of t: the
// collection lock routes, the namespace lock routes, or else the unit lock
// routes. A reserved character of a name stays escaped.
func RemovalRoutes(t manuallock.Target) LockRoutes {
	if t.Kind == manuallock.KindCollection {
		remove := strings.NewReplacer(
			"{namespace}", url.PathEscape(t.Collection.Namespace.String()),
			"{collection}", url.PathEscape(t.Collection.Name.String()),
		).Replace(CollectionLockPath)

		return LockRoutes{Remove: remove, Force: remove + ForceUnlockSuffix}
	}

	if t.Kind == manuallock.KindNamespace {
		remove := strings.Replace(NamespaceLockPath, "{namespace}", url.PathEscape(t.Space.String()), 1)

		return LockRoutes{Remove: remove, Force: remove + ForceUnlockSuffix}
	}

	return unitRoutes(t.Unit)
}

// unitRoutes returns the unit lock routes of the unit u.
func unitRoutes(u collection.UnitRef) LockRoutes {
	unit := unitQueryValue(u)

	return LockRoutes{
		Remove: UnitLockPath + "?unit=" + unit,
		Force:  UnitLockPath + ForceUnlockSuffix + "?unit=" + unit,
	}
}

// LockedText returns the plain text of a refusal of a manual lock for a
// person: the refusal of e, then the route of the holder that removes the
// lock.
func LockedText(e *manuallock.LockedError) string {
	return WithRemoval(e.Refusal(), e.Lock.Target)
}

// WithRemoval returns text, then the route of the holder that removes the
// manual lock of t: "<text>. To remove the lock, the holder sends DELETE
// <route>.".
func WithRemoval(text string, t manuallock.Target) string {
	return text + ". To remove the lock, the holder sends DELETE " + RemovalRoutes(t).Remove + "."
}

// unitQueryValue escapes the unit reference for a query. A colon and a slash
// are legal in a query, so they stay readable for the person who copies the
// route. Every other reserved character stays escaped.
func unitQueryValue(u collection.UnitRef) string {
	return strings.NewReplacer("%3A", ":", "%2F", "/").Replace(url.QueryEscape(u.String()))
}
