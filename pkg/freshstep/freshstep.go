// Package freshstep decides whether a fresh authentication is required. The
// installation setting freshAuthEnabled turns the requirement on and off.
package freshstep

import (
	"context"
	"errors"
)

// ErrNilPolicy means that NewGuard got no policy.
var ErrNilPolicy = errors.New("freshstep: nil policy")

// ErrNilCheck means that NewGuard got no check.
var ErrNilCheck = errors.New("freshstep: nil check")

// Policy reports whether the installation requires a fresh step.
type Policy interface {
	// Required returns true when the setting freshAuthEnabled is on, or when
	// the setting cannot be read (fail closed). It never returns an error.
	Required(ctx context.Context) bool
}

// PolicyFunc is a Policy that a function answers. A site that tests the
// setting off or on uses it.
type PolicyFunc func(ctx context.Context) bool

// Required returns f(ctx). A nil f requires a step (fail closed).
func (f PolicyFunc) Required(ctx context.Context) bool {
	if f == nil {
		return true
	}

	return f(ctx)
}

// readFunc reads the setting freshAuthEnabled.
type readFunc func(ctx context.Context) (bool, error)

// SettingPolicy is the Policy of a setting reader. Only NewPolicy makes one
// that holds a reader. The zero value always requires a step.
type SettingPolicy struct {
	read readFunc
}

// NewPolicy returns the SettingPolicy of read, the reader of freshAuthEnabled.
// A nil reader gives a policy that always requires a step.
func NewPolicy(read func(ctx context.Context) (bool, error)) SettingPolicy {
	return SettingPolicy{read: read}
}

// Required returns the value of the setting. It returns true when the setting
// cannot be read, and when there is no reader.
func (p SettingPolicy) Required(ctx context.Context) bool {
	// Defense in depth: a nil reader fails closed. NewPolicy callers do not
	// rely on the reader being set.
	if p.read == nil {
		return true
	}

	on, err := p.read(ctx)
	if err != nil {
		return true
	}

	return on
}

// Guard asks for a fresh step only when the policy requires one. It has no
// exported field, so code outside this package cannot set its state. Its
// zero value requires a fresh step.
type Guard struct {
	policy Policy
	check  func(ctx context.Context, subject string) error
}

// NewGuard returns a Guard. A nil policy or a nil check is an error.
func NewGuard(p Policy, check func(ctx context.Context, subject string) error) (Guard, error) {
	if p == nil {
		return Guard{}, ErrNilPolicy
	}

	if check == nil {
		return Guard{}, ErrNilCheck
	}

	return Guard{policy: p, check: check}, nil
}

// IsZero reports whether g has no policy or no check: NewGuard did not make
// it. A service that requires a Guard refuses such a value.
func (g Guard) IsZero() bool {
	return g.policy == nil || g.check == nil
}

// Require returns nil when the policy does not require a step. Otherwise it
// returns the result of the check, which is the refusal of the domain package.
func (g Guard) Require(ctx context.Context, subject string) error {
	// Defense in depth: a Guard without a policy or a check, made by a zero
	// value, runs the check. The services check their own input too.
	if g.policy != nil && !g.policy.Required(ctx) {
		return nil
	}

	if g.check == nil {
		return ErrNilCheck
	}

	return g.check(ctx, subject)
}
