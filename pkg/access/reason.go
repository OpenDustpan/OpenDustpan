package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Reason names what gives one action of an "explain why" answer: the ID of
// a stored grant, or the ID of a rule of the code-rule catalog. Exactly one
// of GrantID and Rule is set. An allowed action with no reason is a defect:
// the answer then denies the action and names no reason.
type Reason struct {
	Action  identity.Action
	GrantID string
	Rule    identity.CodeRuleID
}

// GrantRevoked is told when a revoke ends a grant of a server-made
// principal, after the revoke commits. The trigger service meets it: it
// pauses the triggers whose purpose needs the grant, with the reason
// visible, and deletes none.
//
// The pause makes the cause visible. It does not stop a start: the
// Authorizer refuses a start with no grant, also when the pause did not
// happen. So an error of GrantRevoked does not undo the revoke. The access
// service logs the error and answers the revoke as done.
type GrantRevoked interface {
	// GrantRevoked records that g ended at e.At. e is the audit entry of
	// the revoke, and e.Subject names the principal that revoked it. It
	// returns the errors of the store of the pauses.
	GrantRevoked(ctx context.Context, g Grant, e audit.Entry) error
}
