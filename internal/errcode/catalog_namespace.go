package errcode

// The codes of the area. A code is never removed and never changes its
// meaning.
const (
	// NamespaceAlreadyExists means that a namespace with the same name exists.
	NamespaceAlreadyExists = "namespace.already-exists"
	// NamespaceFenced means that a newer failover version fences the write in the namespace.
	NamespaceFenced = "namespace.fenced"
	// NamespaceInvalidName means that the name of the namespace is not valid.
	NamespaceInvalidName = "namespace.invalid-name"
	// NamespaceNotFound means that the namespace does not exist.
	NamespaceNotFound = "namespace.not-found"
	// NamespaceUnavailable means that the database does not answer.
	NamespaceUnavailable = "namespace.unavailable"
	// NamespaceNotHome means that the namespace is not homed in the region of the server.
	NamespaceNotHome = "namespace.not-home"
)

// namespaceSpecs are the codes of the namespace of a request.
func namespaceSpecs() []Spec {
	return []Spec{
		{Code: NamespaceAlreadyExists, Meaning: "a namespace with the same name exists", Args: nil},
		{Code: NamespaceFenced, Meaning: "a newer failover version fences the write in the namespace", Args: nil},
		{Code: NamespaceInvalidName, Meaning: "the name of the namespace is not valid", Args: nil},
		{Code: NamespaceNotFound, Meaning: "the namespace does not exist", Args: nil},
		{Code: NamespaceNotHome, Meaning: "the namespace is not homed in the region of the server", Args: nil},
		{Code: NamespaceUnavailable, Meaning: "the database does not answer", Args: nil},
	}
}
