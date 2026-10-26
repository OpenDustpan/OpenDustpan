package gate

import (
	"fmt"
	"slices"
	"strings"
)

// NoApprover is the value that nobody can meet. A rule or a default that
// lost its last approver holds it as its only user, so the requirement stays
// open and the gate refuses the change that the rule covers. It starts with
// "!", and a subject made through the API never does, so no principal made
// through the API has it. The approval code never counts a signature whose
// subject equals NoApprover, also not for a principal that an identity
// provider made.
const NoApprover = "!no-approver"

// DevSubjectMatcher reports whether a subject is a dev subject. The caller
// passes principal.IsDevSubject, so one rule matches a dev subject
// everywhere. Package gate cannot import the package that holds the rule.
type DevSubjectMatcher func(subject string) bool

// ApproverPolicy holds what the checks of approvers need to know about the
// installation. The zero value is a production installation that matches no
// dev subject, and then each check refuses: fail closed.
type ApproverPolicy struct {
	// Production is true for an installation of the kind production.
	Production bool `json:"-"`
	// IsDev is the matcher of dev subjects.
	IsDev DevSubjectMatcher `json:"-"`
}

// Validate returns an error that wraps ErrInvalid when an entry of a.Users
// cannot be a user that a person names. It refuses NoApprover, each subject
// that starts with "!", and, on a production installation, each dev subject.
// Each write path calls it for every approval rule, approval default and
// ownership rule.
//
// Defense in depth: the approval code checks a signature again with
// CountsAsSignature, and a restore strips dev subjects from stored data. This
// check does not rely on either.
func (p ApproverPolicy) Validate(a Approvers) error {
	production, isDev := p.Production, p.IsDev

	for _, user := range a.Users {
		switch {
		case user == NoApprover:
			return fmt.Errorf("%w: %q is a value for internal use, not an approver; replace it with an approver",
				ErrInvalid, user)
		case strings.HasPrefix(user, "!"):
			return fmt.Errorf("%w: the approver %q starts with an exclamation mark", ErrInvalid, user)
		case production && isDev != nil && isDev(user):
			return fmt.Errorf("%w: the approver %q is a dev user, and this installation is a production installation",
				ErrInvalid, user)
		default:
		}
	}

	return nil
}

// RuleApprovers returns a with the read rule of a stored approval rule or
// ownership rule: a list that names no approver is read as Users with
// NoApprover only. A default does not use it, because an approval default
// with no approvers means any holder of the approve action.
func RuleApprovers(a Approvers) Approvers {
	if !a.Empty() {
		return a
	}

	return Approvers{Groups: a.Groups, GroupIDs: nil, Users: []string{NoApprover}, UnitOwners: false}
}

// NamesNobody reports whether nobody can meet a: it holds no group, does not
// read the unit owners, and each user is NoApprover or there is no user.
func (a Approvers) NamesNobody() bool {
	if len(a.Groups) > 0 || a.UnitOwners {
		return false
	}

	return !slices.ContainsFunc(a.Users, func(u string) bool { return u != NoApprover })
}

// Unmeetable reports whether a requirement of r cannot be met. The gate then
// refuses the change that r covers and does not skip the requirement.
func (r ApprovalRule) Unmeetable() bool {
	return RuleApprovers(r.Approvers).NamesNobody()
}

// Unmeetable reports whether a requirement of r cannot be met. A rule in
// evaluate mode asks for nothing, so it is never unmeetable.
func (r OwnershipRule) Unmeetable() bool {
	return r.Mode == ModeEnforce && RuleApprovers(r.Approvers).NamesNobody()
}

// Unmeetable reports whether a requirement of d cannot be met. A default with
// no approver from the start means any holder of the approve action, so it
// can be met.
func (d ApprovalDefault) Unmeetable() bool {
	return !d.Approvers.Empty() && d.Approvers.NamesNobody()
}

// CountsAsSignature reports whether a signature of subject may fill a slot of
// a requirement. It is false for NoApprover, also on a dev installation, and
// on a production installation for each dev subject, also when the data
// already holds the signature.
func (p ApproverPolicy) CountsAsSignature(subject string) bool {
	if subject == NoApprover {
		return false
	}

	if p.Production && (p.IsDev == nil || p.IsDev(subject)) {
		return false
	}

	return true
}
