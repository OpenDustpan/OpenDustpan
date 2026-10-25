package access

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Reason names what gives one allowed action of an "explain why" answer,
// and the code rules that took part in the same decision. Exactly one of
// GrantID and Rule is set: GrantID is the ID of a stored grant, and Rule is
// the ID of a rule of the code-rule catalog with the effect allows. Rules
// lists the rules with the effect limits or exempts that applied to the
// decision, for example the owner cap of a server-made principal, in
// catalog order.
//
// An Authorizer that cannot name the grant or the rule of an action denies
// the action, so the answer and the decision never disagree.
type Reason struct {
	Action  identity.Action
	GrantID string
	Rule    identity.CodeRuleID
	Rules   []identity.CodeRuleID
}

// GrantRevoked is told when a revoke ends a grant of a server-made
// principal, after the revoke commits. The trigger service meets it: it
// pauses the one trigger of that principal, with the reason visible, and
// deletes nothing.
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
