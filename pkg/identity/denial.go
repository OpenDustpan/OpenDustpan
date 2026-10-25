package identity

import (
	"fmt"
)

// DenyCause names why an Authorizer denied an action.
type DenyCause string

// The causes of a denial.
const (
	// DenyNoGrant is a denial because no stored grant of the principal
	// gives the action on the object.
	DenyNoGrant DenyCause = "no-grant"
	// DenyCodeRule is a denial because a rule of the code-rule catalog with
	// the effect CodeRuleLimits took the action away. DeniedError.Rule names
	// the rule.
	DenyCodeRule DenyCause = "code-rule"
	// DenyFailClosed is a denial because an error, for example of a store,
	// kept the Authorizer from a decision.
	DenyFailClosed DenyCause = "fail-closed"
)

// DeniedError is a denial of an Authorizer with its cause. It wraps
// ErrDenied, and Err when it is set. Rule is set for DenyCodeRule only. A
// denial without a DeniedError has a cause that the caller does not know,
// and a caller treats it so.
type DeniedError struct {
	Err    error
	Cause  DenyCause
	Rule   CodeRuleID
	Action Action
}

// Error names the action and the cause, and the rule or the error when one
// is set.
func (e *DeniedError) Error() string {
	msg := fmt.Sprintf("%s: %s (%s)", ErrDenied.Error(), e.Action, e.Cause)
	if e.Rule != "" {
		msg += ": rule " + string(e.Rule)
	}

	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}

	return msg
}

// Unwrap gives ErrDenied, and Err when it is set.
func (e *DeniedError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrDenied}
	}

	return []error{ErrDenied, e.Err}
}
