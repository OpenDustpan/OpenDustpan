package installation

import "context"

// KindReader reads the installation kind without a claim. The backup export
// reads the kind of its own database through it, so a backup never records a
// kind that a caller passed in. A store adapter implements it after the
// conformance suite in pkg/store/storetest checks it.
type KindReader interface {
	// ReadKind returns the kind that the database holds. It returns
	// ErrUnavailable when no kind is claimed yet, when the row is missing,
	// when the kind cannot be read, and when the stored value is not a known
	// kind. It changes nothing.
	ReadKind(ctx context.Context) (Kind, error)
}
