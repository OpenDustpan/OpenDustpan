//go:build !linux && !darwin

package conformance

// endGroupAfterExit does nothing on this platform. Only linux and darwin wait
// for the exit of the adapter before the group kill; the wait that follows
// reaps the adapter here.
func endGroupAfterExit(*ProcessAdapter) {}
