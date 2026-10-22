//go:build !unix

package conformance_test

// startGrandchild does nothing: only the unix build has process groups.
func startGrandchild() {}
