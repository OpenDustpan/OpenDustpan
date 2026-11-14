package jobtoken

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for job tokens. A store keeps the digest of
// each secret, never the secret.
//
// Issue writes the record and its audit entry in one fenced transaction in
// the namespace of the unit: both commit, or neither. An error changes
// nothing.
type Store interface {
	// Issue writes t and its audit entry e in one fenced transaction in the
	// namespace of the unit. The time of the issue is e.At. The store refuses
	// a token that Validate refuses, so it refuses a lifetime above
	// MaxLifetime too. It returns:
	//
	//   - ErrInvalid when t fails Validate, when t.ExpiresAt is not after
	//     e.At, or when the starter of t is not the principal that started
	//     the job;
	//   - ErrNotFound when the job or the claim operation does not exist;
	//   - ErrNotLive when the claim or the job is not live: the operation is
	//     not running, the claim token differs, the lease ended at or before
	//     e.At, the job state is not dispatched, running or canceling, the
	//     current operation of the job differs, the unit ID differs, or the
	//     starter is deleted or disabled;
	//   - ErrAlreadyExists when a record has the digest of t;
	//   - namespace.ErrFenced when the fence is stale or names another
	//     namespace than the unit;
	//   - audit.ErrInvalidEntry when e fails validation or names another
	//     namespace than the fence.
	//
	// When several errors apply, the first rule wins, in this order: ErrInvalid
	// for a token that fails Validate, audit.ErrInvalidEntry, namespace.ErrFenced,
	// ErrInvalid for an expiry not after e.At, ErrAlreadyExists, ErrNotFound,
	// ErrNotLive for a starter that is deleted or disabled, ErrInvalid for a
	// starter that is not the job starter, then ErrNotLive for a claim or job
	// that is not live.
	Issue(ctx context.Context, t Token, fence namespace.Fence, e audit.Entry) error

	// GetByDigest returns the record whose secret has the digest d, with the
	// claim that the store reads at the same time. The row keeps the
	// namespace by its stable ID. The store reads the starter principal by
	// Token.StarterID and sets Claim.StarterRecordID to the ID of that record.
	// The store resolves the subject in job.StartedBy to the ID of its
	// principal record and sets Claim.JobStarterID to that ID; a subject with
	// no principal gives ErrNotFound. Claim.JobStarterID comes from the job,
	// never from the token row. It returns ErrNotFound when no record has the
	// digest, when the namespace of the row no longer exists, when the current
	// name of that namespace differs from the namespace of the unit of the
	// record (a rename, for example by a failover), when the starter principal
	// is deleted, or when the job or the claim operation of the record does
	// not exist. A disabled starter comes back with StarterEnabled false.
	// Every other failure comes back wrapped; the store never returns a driver
	// error.
	GetByDigest(ctx context.Context, d Digest) (Match, error)
}
