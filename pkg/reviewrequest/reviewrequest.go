// Package reviewrequest is the domain of the review requests of a Git host
// (a pull request of GitHub, a merge request of GitLab) and of their link to
// change orders. It holds the review mode of a unit, the binding level of an
// approval, the set of operations that an approval binds to, the authors of a
// review request, and the ports that the service of the review requests
// uses.
//
// The package is tool-neutral and forge-neutral. It imports no tool package,
// no store adapter, no forge adapter and no transport. The wiring implements
// its narrow GitHost port over the Forge port.
//
// This package holds types, validation and ports only. The service, the
// adapters and the routes come in later changes, each with the conformance
// suite of package reviewrequesttest.
package reviewrequest

import (
	"errors"
	"slices"
	"time"
)

// Errors of the ports. Each port documents which of them it returns.
var (
	// ErrInvalid reports a value that fails validation.
	ErrInvalid = errors.New("reviewrequest: invalid value")
	// ErrNotFound reports an unknown review request, or one of another
	// namespace.
	ErrNotFound = errors.New("reviewrequest: not found")
	// ErrConflict reports a write on a stale read: the version did not match.
	ErrConflict = errors.New("reviewrequest: version conflict")
	// ErrUnavailable reports a Git host that did not answer in time, or that
	// answered with an error that is not a refusal. The caller fails closed.
	ErrUnavailable = errors.New("reviewrequest: Git host unavailable")
	// ErrNotMergeable reports a Git host that refused a merge. The error that
	// wraps it carries the answer of the Git host.
	ErrNotMergeable = errors.New("reviewrequest: Git host refused the merge")
	// ErrHeadMoved reports a head commit that is not the commit that the
	// caller named.
	ErrHeadMoved = errors.New("reviewrequest: the head commit moved")
	// ErrUnknown reports a value that the Git host has not computed yet, for
	// example a merge state that is still being checked. A gate treats it as
	// a denial.
	ErrUnknown = errors.New("reviewrequest: value not known yet")
	// ErrBypass reports a merge that the server refused because the app of
	// the connection can bypass the branch rules, or because the branch rules
	// no longer require the review request and its status.
	ErrBypass = errors.New("reviewrequest: the app can bypass the branch rules")
)

// Mode is the review mode of a unit. The mode is a unit setting with the
// cascade installation, namespace, collection, unit. It has no default.
type Mode string

// The four review modes.
const (
	// ModeMergedApprovals: approvals on the Git host and in OpenDustpan both
	// count, and the required status of OpenDustpan enforces the result.
	ModeMergedApprovals Mode = "merged-approvals"
	// ModeOpenDustpanMerges: the change order is the place of the plan
	// review, and OpenDustpan or a person merges after the approval.
	ModeOpenDustpanMerges Mode = "opendustpan-merges"
	// ModeNoReviewRequests: change orders start from commits, and the
	// review is in OpenDustpan only.
	ModeNoReviewRequests Mode = "no-review-requests"
	// ModeNoChangeOrders: no change order and no approval. Deny rules and
	// policies apply, and runs are recorded.
	ModeNoChangeOrders Mode = "no-change-orders"
)

// Valid reports whether m is one of the four modes.
func (m Mode) Valid() bool {
	switch m {
	case ModeMergedApprovals, ModeOpenDustpanMerges, ModeNoReviewRequests, ModeNoChangeOrders:
		return true
	default:
		return false
	}
}

// Strictest returns the strictest mode of modes. The order from the
// strictest is ModeNoReviewRequests, ModeOpenDustpanMerges,
// ModeMergedApprovals, ModeNoChangeOrders: where the modes of the units of
// one review request differ, the most restrictive rule wins. It returns
// ErrInvalid when modes is empty or holds a mode that is not valid.
func Strictest(modes []Mode) (Mode, error) {
	if len(modes) == 0 {
		return "", ErrInvalid
	}

	// From the least strict to the strictest.
	order := []Mode{ModeNoChangeOrders, ModeMergedApprovals, ModeOpenDustpanMerges, ModeNoReviewRequests}
	best, bestRank := Mode(""), -1

	for _, m := range modes {
		rank := slices.Index(order, m)
		if rank < 0 {
			return "", ErrInvalid
		}

		if rank > bestRank {
			best, bestRank = m, rank
		}
	}

	return best, nil
}

// BindingLevel says what an approval binds to.
type BindingLevel string

// The three binding levels. Level1 is the default.
const (
	// Level0 binds an approval to the commit. A new commit resets it.
	Level0 BindingLevel = "L0"
	// Level1 binds an approval to the set of operations. A change of the set
	// resets it.
	Level1 BindingLevel = "L1"
	// Level2 binds an approval to the exact plan. A change of the plan
	// digest resets it.
	Level2 BindingLevel = "L2"
)

// DefaultBindingLevel is the binding level when no scope sets one.
const DefaultBindingLevel = Level1

// Valid reports whether l is one of the three binding levels.
func (l BindingLevel) Valid() bool {
	switch l {
	case Level0, Level1, Level2:
		return true
	default:
		return false
	}
}

// OperationKind is the kind of change of one resource in a plan.
type OperationKind string

// The kinds of change. Each order of a replace is its own kind.
const (
	OperationCreate             OperationKind = "create"
	OperationUpdate             OperationKind = "update"
	OperationDelete             OperationKind = "delete"
	OperationReplaceCreateFirst OperationKind = "replace-create-first"
	OperationReplaceDeleteFirst OperationKind = "replace-delete-first"
)

// Valid reports whether k is one of the kinds of change.
func (k OperationKind) Valid() bool {
	switch k {
	case OperationCreate, OperationUpdate, OperationDelete,
		OperationReplaceCreateFirst, OperationReplaceDeleteFirst:
		return true
	default:
		return false
	}
}

// Floor reports whether a new operation of kind k always needs a new
// approval with a fresh authentication (the approval floor): a delete or a
// replace.
func (k OperationKind) Floor() bool {
	return k == OperationDelete || k == OperationReplaceCreateFirst || k == OperationReplaceDeleteFirst
}

// Operation is one change of one resource: its address in the tool's own
// words (an OpenTofu address or a Pulumi URN) and its kind. The core never
// parses the address.
type Operation struct {
	Address string
	Kind    OperationKind
}

// Ref names one review request: the Git host connection, the numeric ID of
// the repository that the Git host answers, and the number of the review
// request in that repository.
type Ref struct {
	Connection   string
	RepositoryID string
	Number       int64
}

// ApprovalSource says where an approval came from.
type ApprovalSource string

// The sources of an approval.
const (
	// ApprovalConsole is an approval of a person in a console session.
	ApprovalConsole ApprovalSource = "console"
	// ApprovalGitHostReview is an approving review on the Git host by a
	// linked person account. It counts in ModeMergedApprovals only, binds at
	// Level0 at most, and never fills a requirement that asks for a fresh
	// authentication.
	ApprovalGitHostReview ApprovalSource = "git-host-review"
)

// AuthorSource says why a principal is an author of a review request.
type AuthorSource string

// The sources of an author. A Co-authored-by trailer is never a source.
const (
	AuthorOpener         AuthorSource = "opener"
	AuthorPusher         AuthorSource = "pusher"
	AuthorJobRequester   AuthorSource = "job-requester"
	AuthorVerifiedCommit AuthorSource = "verified-commit-author"
	AuthorPlanAsMe       AuthorSource = "plan-with-credentials-as-me"
)

// Author is one author of a review request, with the source that made it an
// author, as the Git host gives it. AccountID is the immutable user ID on the
// Git host. BotAppID is the app ID of a bot account, and is empty for a
// person. PersonUnknown is true when the Git host cannot tell which person is
// behind a push of a bot. The service, never the adapter, looks up the link
// of an account to a principal.
type Author struct {
	AccountID     string
	BotAppID      string
	PersonUnknown bool
	Source        AuthorSource
}

// Review is a review on the Git host, as a fresh read through the API gives
// it. CommitID is empty on a Git host whose reviews name no commit.
type Review struct {
	ID          string
	AccountID   string
	Approved    bool
	Changes     bool
	Dismissed   bool
	CommitID    string
	SubmittedAt time.Time
}

// StatusRead is one status of a commit as the Git host gives it, with its
// creator. CreatorID is the account ID of a person or the app ID of an app,
// and CreatorIsApp tells which. A status matches an OpenDustpan status only
// when its creator is the app of the connection, never by its name alone.
type StatusRead struct {
	Name         string
	State        string
	Commit       string
	CreatorID    string
	CreatorIsApp bool
}

// Snapshot is what the server read of one review request at one time. The
// console shows ReadAt. MergedBy and QueuedBy are immutable account IDs, and
// are empty when no person merged or queued the review request.
type Snapshot struct {
	Ref          Ref
	Open         bool
	Merged       bool
	Head         string
	Base         string
	HeadRepoID   string
	MergeCommit  string
	MergedBy     string
	QueuedBy     string
	Authors      []Author
	Reviews      []Review
	Statuses     []StatusRead
	Requirements []Requirement
	ReadAt       time.Time
}

// Requirement is one merge requirement of the Git host, in its own terms.
// It leaves out each requirement that the app of the connection fills with
// its own status, matched by creator.
type Requirement struct {
	Name string
	Met  bool
}

// RequiredStatus is a status that the branch rules require, with the app
// that must post it when the rules name one.
type RequiredStatus struct {
	Name         string
	CreatorAppID string
}

// BranchRules are the rules of the target branch of a review request, as the
// Git host gives them to the app of the connection.
type BranchRules struct {
	RequiredStatuses  []RequiredStatus
	RequiredApprovals int
	CodeOwnerReview   bool
	RequireReview     bool
	// AppCanBypass is true when the app of the connection is on a bypass
	// list, has an admin override, or can force a merge.
	AppCanBypass bool
	// BypassActors names each actor of a bypass list, as the Git host gives
	// it.
	BypassActors []string
	// Mergers names the accounts or apps that may merge when the branch
	// restricts who merges, and is empty when it does not.
	Mergers []string
}

// Permission is the access of an account on a repository.
type Permission string

// The permissions that the service tells apart.
const (
	PermissionNone  Permission = "none"
	PermissionRead  Permission = "read"
	PermissionWrite Permission = "write"
	PermissionAdmin Permission = "admin"
)
