package unitimport

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrUploadExists reports a second commit of one upload version: the import,
// the unit directory and the sequence already have a commit row. The first
// commit stays.
var ErrUploadExists = errors.New("upload version already committed")

// ErrAttemptExpired reports a commit of an attempt whose start time is outside
// the allowed window, by the clock of the database: the attempt started more
// than UploadAttemptDeadline before the commit, or more than UploadAttemptSkew
// after it. The attempt does not count, and its files are garbage.
var ErrAttemptExpired = errors.New("upload attempt is outside its time window")

// UploadAttemptDeadline is the time that one attempt to store a version has
// for the write of its files and the commit of its row. A reaper of the blob
// files of an attempt that no row names must wait longer than this deadline
// (and the skew below) before it deletes a file. It must read "now" from the
// database, not from the clock of an instance, as the commit does.
const UploadAttemptDeadline = 30 * time.Minute

// UploadAttemptSkew is how far the start time of an attempt may be ahead of
// the clock of the database. A writer with a clock that is further ahead
// could commit later than the deadline allows, so its commit is refused.
const UploadAttemptSkew = 2 * time.Minute

// MaxUploadSequence is the largest sequence of an upload version.
const MaxUploadSequence = 9_999_999_999

// uploadUnitDirPattern is the form of the unit directory of an upload: the
// hex of a digest.
var uploadUnitDirPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// uploadFactsPattern is the form of the file name of a facts file:
// <sequence>-<attempt>.json.
var uploadFactsPattern = regexp.MustCompile(`^\d{10}-[0-9a-z]{1,64}\.json$`)

// UploadCommit is the commit row of one upload version. The row is the commit
// point of the version: the version exists when the row exists. Facts names
// the facts file of the attempt that won, relative to the unit directory.
type UploadCommit struct {
	ImportID string
	UnitDir  string
	Sequence int
	Facts    string
	// CommittedAt is the time of the commit by the clock of the database. The
	// store sets it, ignores the value of a commit that it gets, and gives it
	// back with the row. A caller may set it to the time that it wants in the
	// audit entry.
	CommittedAt time.Time
	// AttemptStartedAt is the time at which the attempt started, by the clock
	// of the writer. The store checks it against its own clock. The store
	// does not keep it.
	AttemptStartedAt time.Time
}

// Validate returns ErrInvalid when c has a value that no commit row can hold.
// It is a defense-in-depth layer: the upload source builds these values from
// its own keys, and the store checks them again so that a new caller cannot
// store a name that points outside the unit directory.
func (c UploadCommit) Validate() error {
	switch {
	case c.ImportID == "":
		return fmt.Errorf("%w: the upload commit has no import ID", ErrInvalid)
	case !uploadUnitDirPattern.MatchString(c.UnitDir):
		return fmt.Errorf("%w: the upload unit directory %q is not a digest", ErrInvalid, c.UnitDir)
	case c.Sequence < 1 || c.Sequence > MaxUploadSequence:
		return fmt.Errorf("%w: the upload sequence %d is not from 1 to %d", ErrInvalid, c.Sequence, MaxUploadSequence)
	case !uploadFactsPattern.MatchString(c.Facts):
		return fmt.Errorf("%w: the upload facts file name %q is not <sequence>-<attempt>.json", ErrInvalid, c.Facts)
	case c.AttemptStartedAt.IsZero():
		return fmt.Errorf("%w: the upload commit has no attempt start time", ErrInvalid)
	}

	return nil
}

// UploadStore keeps the commit rows of the upload versions of an import.
// CommitUploadVersion takes the fence of the namespace and checks it in the
// same statement that inserts the row. It wraps ErrInvalid for a bad commit,
// ErrNotFound and ErrPurged for an import that does not exist, ErrTransition
// for an import that ended, ErrAttemptExpired for an attempt whose start time is
// more than UploadAttemptDeadline before the clock of the database or more
// than UploadAttemptSkew after it, and
// ErrUploadExists for a version that has its row already; the first row
// stays. The entry e is appended in the same transaction as the row, and it
// names the namespace of the fence; a refused commit appends nothing.
// UploadCommits returns the rows of one unit directory of an import, in the
// order of the sequences, and an empty list when the import holds none.
type UploadStore interface {
	// CommitUploadVersion inserts the commit row of one version.
	CommitUploadVersion(ctx context.Context, fence namespace.Fence, c UploadCommit, e audit.Entry) error
	// UploadCommits returns the commit rows of one unit directory.
	UploadCommits(ctx context.Context, ns namespace.Name, importID, unitDir string) ([]UploadCommit, error)
}
