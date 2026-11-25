// Package freshstep decides whether a fresh authentication is required.
package freshstep

// Guard asks for a fresh step only when the policy requires one. It has no
// exported field, so code outside this package cannot set its state. Its
// zero value requires a fresh step.
type Guard struct {
	_ [0]func()
}
