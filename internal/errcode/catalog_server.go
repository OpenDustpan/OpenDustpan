package errcode

// ServerInternal is a 500 that no classify function of the adapter maps.
const ServerInternal = "server.internal"

// serverSpecs are the codes of a failure of the server.
func serverSpecs() []Spec {
	return []Spec{
		{Code: ServerInternal, Meaning: "the server failed with an error that it does not classify", Args: nil},
	}
}
