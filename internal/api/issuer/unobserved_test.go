package issuer_test

// unobservedOperations lists the described operations whose success path no
// test of this package observes. The list can only get shorter: the package
// fails when a test observes an operation on the list, and when an operation
// that is not on the list has no test of its success path.
func unobservedOperations() []string { return nil }
