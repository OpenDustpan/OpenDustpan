package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// ServerComponentNotFound is a request for a route of a component that the process does not run.
	ServerComponentNotFound = "server.component-not-found"
	// ServerInternal is a 500 that no classify function of the adapter maps.
	ServerInternal = "server.internal"
	// ServerMethodNotAllowed is a request with a method that the route does not take.
	ServerMethodNotAllowed = "server.method-not-allowed"
	// ServerRouteNotFound is a request for a path that no route serves.
	ServerRouteNotFound = "server.route-not-found"
)

// serverSpecs are the codes of a failure of the server and of a request that
// no route takes.
func serverSpecs() []Spec {
	return []Spec{
		{
			Code:    ServerComponentNotFound,
			Meaning: "the process does not run the component that serves the path",
			Args:    nil,
		},
		{Code: ServerInternal, Meaning: "the server failed with an error that it does not classify", Args: nil},
		{Code: ServerMethodNotAllowed, Meaning: "the route does not take the method of the request", Args: nil},
		{Code: ServerRouteNotFound, Meaning: "no route serves the path of the request", Args: nil},
	}
}
