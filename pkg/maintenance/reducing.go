package maintenance

// ReducingRoute is one route that only reduces access and that therefore
// works in maintenance mode: it ends, revokes, retires or disables, and the
// store journals the write (see Revocation). The patterns are plain strings
// in the form of the route tables, "METHOD /path", so that this package does
// not import net/http.
type ReducingRoute struct {
	// Method is the HTTP method, for example DELETE.
	Method string
	// Pattern is the path pattern, for example /api/server/grants/{id}.
	Pattern string
	// Actions are the authorization actions that the route declares. A route
	// that serves more than one kind of object declares more than one.
	Actions []string
	// Kind is the journal kind of the write: one of the Revoked constants.
	Kind string
}

// The HTTP methods of the list. They are the values of net/http, written out
// because this package does not import net/http.
const (
	methodPost   = "POST"
	methodDelete = "DELETE"
)

// ReducingRoutes returns the list of the routes that only reduce access, a
// new slice each call. It is the only list: the maintenance gate, the
// journal kinds and the route tests read it. The gate lets these routes pass
// in maintenance mode. The kinds of the list are a subset of Kinds: a
// temporary administrator ends by expiry, so it has no route of its own. The
// change of a sign-in provider that only disables it depends on the body, so
// only the delete of a provider is on the list.
func ReducingRoutes() []ReducingRoute {
	return append(runnerReducingRoutes(), serverReducingRoutes()...)
}

// runnerReducingRoutes returns the entries of the runner management API.
func runnerReducingRoutes() []ReducingRoute {
	return []ReducingRoute{
		{
			Method:  methodPost,
			Pattern: "/api/runners/{namespace}/runners/{runner}/retire",
			Actions: []string{"namespace:manage-runners"},
			Kind:    RevokedRunner,
		},
		{
			Method:  methodPost,
			Pattern: "/api/runners/{namespace}/pools/{pool}/join-tokens/{id}/revoke",
			Actions: []string{"namespace:manage-runners"},
			Kind:    RevokedJoinToken,
		},
	}
}

// serverReducingRoutes returns the entries of the installation API.
func serverReducingRoutes() []ReducingRoute {
	return []ReducingRoute{
		{
			Method:  methodDelete,
			Pattern: "/api/server/tokens/{id}",
			Actions: []string{"server:manage-tokens", "principal:manage-tokens"},
			Kind:    RevokedToken,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/principals/{principal}/tokens/{id}",
			Actions: []string{"principal:manage-tokens"},
			Kind:    RevokedToken,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/caller/tokens/{id}",
			Actions: []string{"token:manage-own"},
			Kind:    RevokedToken,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/grants/{id}",
			Actions: []string{
				"server:manage-access", "namespace:manage-access", "collection:manage-access", "unit:manage-access",
			},
			Kind: RevokedGrant,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/namespaces/{namespace}/grants/{id}",
			Actions: []string{"namespace:manage-access"},
			Kind:    RevokedGrant,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/namespaces/{namespace}/collections/{collection}/grants/{id}",
			Actions: []string{"collection:manage-access"},
			Kind:    RevokedGrant,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/namespaces/{namespace}/unit-grants/{id}",
			Actions: []string{"unit:manage-access"},
			Kind:    RevokedGrant,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/principals/{principal}/grants/{id}",
			Actions: []string{"server:manage-access"},
			Kind:    RevokedGrant,
		},
		{
			Method:  methodDelete,
			Pattern: "/api/server/sign-in-providers/{id}",
			Actions: []string{"server:manage-sign-in"},
			Kind:    RevokedProvider,
		},
		{
			Method:  methodPost,
			Pattern: "/api/server/principals/{id}/disable",
			Actions: []string{"server:manage-principals"},
			Kind:    RevokedPrincipal,
		},
		{
			Method:  methodPost,
			Pattern: "/api/server/sign-out",
			Actions: []string{"caller:read"},
			Kind:    RevokedSession,
		},
	}
}

// Kinds returns the journal kinds that a replay accepts, a copy each time.
func Kinds() []string {
	return []string{
		RevokedSession, RevokedToken, RevokedPrincipal, RevokedTempAdmin, RevokedGrant, RevokedJoinToken,
		RevokedProvider, RevokedRunner,
	}
}
