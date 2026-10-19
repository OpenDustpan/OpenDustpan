package profile

import (
	"context"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store persists profiles and their revisions.
//
// Each method that writes takes the fence of the namespace and one audit
// entry. The store checks the fence and appends the entry in the transaction
// of the write: both commit, or neither. A write returns namespace.ErrFenced
// for a stale fence, namespace.ErrNotFound for an unknown namespace,
// audit.ErrInvalidEntry for an entry that fails validation or names another
// namespace, and changes nothing on an error.
//
// A profile is in one namespace. A name is unique among the live profiles of
// a namespace. A deleted profile keeps its ID and its revisions, and frees
// its name.
type Store interface {
	// Create stores p with revision r, which has number 1, and returns the
	// stored profile. ErrConflict for a name in use, ErrLimit above
	// MaxProfiles, ErrInvalid for a profile or revision that fails the
	// rules of this package.
	Create(ctx context.Context, p Profile, r Revision, fence namespace.Fence, e audit.Entry) (Profile, error)
	// Get reads one live profile by ID or by name. ErrNotFound.
	Get(ctx context.Context, ns namespace.Name, ref Ref) (Profile, error)
	// List reads one page of live profiles ordered by name. ErrInvalid for
	// a Limit below 1. An unknown namespace reads as an empty page.
	List(ctx context.Context, q Filter) (Page, error)
	// Revision reads revision number of a live profile. Number 0 reads the
	// newest revision. ErrNotFound.
	Revision(ctx context.Context, ns namespace.Name, id ID, number int) (Revision, error)
	// Revisions reads the revisions of a live profile, newest first, with
	// a number below before (0 means from the newest), at most limit.
	// ErrNotFound for an unknown profile.
	Revisions(ctx context.Context, ns namespace.Name, id ID, before, limit int) ([]Revision, error)
	// Save adds revision r on top of the revision that at names and moves
	// the newest revision to it. r.Number must be at.Latest+1. ErrConflict
	// when at.Latest is not the newest revision, ErrNotFound, ErrInvalid. It
	// returns the updated profile.
	Save(ctx context.Context, at Anchor, r Revision, fence namespace.Fence, e audit.Entry) (Profile, error)
	// Delete removes a live profile: its name is free at once, and its
	// revisions stay. With at.Latest above 0 it returns ErrConflict when
	// the newest revision is another. ErrNotFound.
	Delete(ctx context.Context, at Anchor, fence namespace.Fence, e audit.Entry) error
}

// Anchor names a live profile and the newest revision that the caller read.
// A write on a stale anchor returns ErrConflict. A Latest of 0 names no
// revision, which only Delete accepts.
type Anchor struct {
	Namespace namespace.Name
	ID        ID
	Latest    int
}
