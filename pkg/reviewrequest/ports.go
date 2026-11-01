package reviewrequest

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Record is the stored state of one review request in one namespace: its
// reference, the change order that it carries, the last snapshot, the ID of
// the plan comment, and the version for writes on a stale read.
type Record struct {
	Namespace   namespace.Name
	Ref         Ref
	ChangeOrder string
	Snapshot    Snapshot
	CommentID   string
	Version     int64
}

// Store keeps the records of the review requests. Every write carries the
// fence of the namespace of the record and the audit entry of the change. The
// store checks the fence and appends the entry in the transaction of the
// write, and it changes nothing when either fails (fail closed). A store does
// not check access.
type Store interface {
	// Get returns the record of ref in the namespace ns. It returns
	// ErrNotFound for an unknown review request and for one of another
	// namespace, and ErrInvalid for a reference that fails validation.
	Get(ctx context.Context, ns namespace.Name, ref Ref) (Record, error)
	// Put writes r when r.Version is the stored version (0 for a new
	// record), and stores it with the next version. It returns ErrConflict
	// on another version, ErrInvalid for a record that fails validation, and
	// namespace.ErrFenced for a stale fence or a fence of another namespace.
	Put(ctx context.Context, r Record, fence namespace.Fence, e audit.Entry) (Record, error)
	// ByChangeOrder returns the record that carries the change order id in
	// the namespace ns. It returns ErrNotFound when no record carries it.
	ByChangeOrder(ctx context.Context, ns namespace.Name, id string) (Record, error)
}

// Status is one status that the server posts on a commit.
type Status struct {
	Name        string
	State       string
	Description string
	TargetURL   string
}

// GitHost is the narrow port of the review requests over the Forge port.
// Each method reads or writes through the API of the Git host as the
// OpenDustpan app. An action as a person uses the user token of that person
// and is not part of this port. Each method returns ErrUnavailable for a Git
// host that does not answer, and the caller fails closed.
type GitHost interface {
	// Read returns a fresh snapshot of ref. It returns ErrNotFound for an
	// unknown review request, and ErrUnknown when the Git host has not
	// computed the merge state yet. It never asks the Git host to compute
	// the merge state again.
	Read(ctx context.Context, ref Ref) (Snapshot, error)
	// PostStatus sets the status s on the commit of ref. A second call with
	// the same name replaces it. It returns ErrNotFound for an unknown
	// commit.
	PostStatus(ctx context.Context, ref Ref, commit string, s Status) error
	// PutComment writes the plan comment of ref and returns its ID. With an
	// empty id it posts a new comment, else it edits the comment id.
	PutComment(ctx context.Context, ref Ref, id, body string) (string, error)
	// DeleteComment deletes the comment id of ref.
	DeleteComment(ctx context.Context, ref Ref, id string) error
	// React adds the reaction emoji to the comment id of ref.
	React(ctx context.Context, ref Ref, id, emoji string) error
	// Merge merges ref when its head is head. It returns ErrHeadMoved when
	// the head is another commit, and an error that wraps ErrNotMergeable,
	// with the answer of the Git host, when the Git host refuses. It never
	// uses a bypass of the branch rules.
	Merge(ctx context.Context, ref Ref, head, method string) (string, error)
}

// Closure is the set of files that a unit reads, at one commit. Known is
// false when the server cannot compute it. Declared paths only add files to
// it.
type Closure struct {
	Files []string
	Known bool
}

// ClosureResolver computes the file closure of a unit.
type ClosureResolver interface {
	// Closure returns the closure of the unit at commit. It returns a
	// closure with Known false, and no error, when the closure cannot be
	// computed. It returns ErrInvalid for a unit or a commit that fails
	// validation.
	Closure(ctx context.Context, unit, commit string) (Closure, error)
}

// PlanClosure is the set of changed resources of one plan of one unit and
// their dependency closure, also across units through outputs.
type PlanClosure struct {
	Unit       string
	Operations []Operation
	Closure    []string
}

// ConflictResult is the result of one conflict check. Disjoint is true only
// when the check proves the two closures disjoint. Resources lists the
// resources that collide, for the console only.
type ConflictResult struct {
	Disjoint  bool
	Resources []string
	Reason    string
}

// ConflictChecker decides whether two plans conflict.
type ConflictChecker interface {
	// Check compares a and b. Each case that the checker cannot prove
	// disjoint is a conflict: provider changes, replacements, renames,
	// data sources, deletes and changes of a parent resource are never
	// proven disjoint. It returns ErrInvalid for a plan that fails
	// validation.
	Check(ctx context.Context, a, b PlanClosure) (ConflictResult, error)
}
