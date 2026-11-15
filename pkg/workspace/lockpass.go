package workspace

import (
	"context"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
)

type (
	lockPassKey struct{}
	jobKey      struct{}
)

// WithJob returns a context that names the job whose job token made the
// request. Create reads it to decide whether a manual lock of the namespace
// applies to the request. The authenticator sets the job only for a verified
// job token.
func WithJob(ctx context.Context, jobID string) context.Context {
	return context.WithValue(ctx, jobKey{}, jobID)
}

// JobFrom returns the job that WithJob set, or the empty string.
func JobFrom(ctx context.Context) string {
	if id, ok := ctx.Value(jobKey{}).(string); ok {
		return id
	}

	return ""
}

// originFrom returns the origin of the state write that the request in ctx
// makes: the job whose job token made it. The commit stays empty, because
// OpenTofu reports none: a job write takes it from the spec of the job when
// the store publishes the version. A request with another credential gives an
// empty origin.
func originFrom(ctx context.Context) identity.StateOrigin {
	return identity.StateOrigin{Job: JobFrom(ctx), Commit: ""}
}

// WithLockPass returns a context for Store.CreateWorkspace. startedAt is when
// the phase of the job of the request started, or the zero time for a request
// with no job. The store refuses the create while a manual lock covers the
// namespace, unless startedAt is not zero and is before the start of the lock:
// the phase of a job runs to its end, as it does for the lock request of the
// job. A store that keeps no manual locks ignores it.
func WithLockPass(ctx context.Context, startedAt time.Time) context.Context {
	return context.WithValue(ctx, lockPassKey{}, startedAt)
}

// LockPassFrom returns the time that WithLockPass set, or the zero time, which
// passes no lock. A store calls it in the transaction of CreateWorkspace.
func LockPassFrom(ctx context.Context) time.Time {
	if at, ok := ctx.Value(lockPassKey{}).(time.Time); ok {
		return at
	}

	return time.Time{}
}
