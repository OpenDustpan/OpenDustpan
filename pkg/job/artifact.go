package job

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DefaultRetention is the time that the server keeps the content of a plan
// and of a log version after the end of its job. An installation setting
// changes it, separately for plans and for logs.
const DefaultRetention = 400 * 24 * time.Hour

// MinRetention bounds the retention setting from below, so that a typing
// error cannot shred every plan and log of the installation at the next
// sweep.
const MinRetention = 24 * time.Hour

var (
	// ErrShredded reports a read of a plan or a log version whose data key
	// the server destroyed. The metadata and the hash stay.
	ErrShredded = errors.New("content was shredded")
	// ErrCurrentVersion reports a shred of the current version of a log.
	// Only an older version can be shredded: a redaction writes a new
	// version first.
	ErrCurrentVersion = errors.New("the current log version cannot be shredded")
	// ErrLogOpen reports a redaction of the log of a step that still runs.
	ErrLogOpen = errors.New("the log is still open")
)

// PlanRef names a stored plan and what a reader can know without its
// content. ID has the prefix pln-, and is also the ID of the data key of
// the plan. SHA256 is the hash of the plaintext. An approval binds to it,
// and the apply phase refuses a plan with another hash.
type PlanRef struct {
	ID        string
	Kind      OutputKind
	SHA256    string
	Size      int64
	CreatedAt time.Time
	// ShreddedAt is the time that the server destroyed the data key, or the
	// zero time.
	ShreddedAt time.Time
}

// LogRef names one version of the log of one step. ID has the prefix jlv-,
// and is also the ID of the data key of the version. Version starts at 1.
// A redaction writes the next version and shreds the older one.
type LogRef struct {
	ID      string
	Version int
	// Chunks is the number of chunks. SHA256 is the hash of the plaintext of
	// all chunks in order. Both are set when the log closes.
	Chunks     int
	SHA256     string
	Size       int64
	ClosedAt   time.Time
	ShreddedAt time.Time
	// RedactedBy is the subject that wrote this version by a redaction, or
	// empty for the first version.
	RedactedBy string
}

// PlanKey returns the blob key of the plan with the ID plan of the job id.
func PlanKey(id ID, plan string) (blob.Key, error) {
	return blob.ParseKey("job/" + string(id) + "/plan/" + plan)
}

// LogChunkKey returns the blob key of chunk seq of the log version with the
// ID version of the job id.
func LogChunkKey(id ID, version string, seq int) (blob.Key, error) {
	return blob.ParseKey(fmt.Sprintf("job/%s/log/%s/%08d", id, version, seq))
}

// Redaction is one change to the text of a log. The server replaces each
// match of Match with the text [redacted]. The server keeps the number of
// matches, never Match.
type Redaction struct {
	Match []byte
}

// ArtifactStore is the persistence port for the metadata and the data keys
// of plans and logs. The content is in the blob store, sealed with one data
// key for each plan and for each log version. A shred destroys that key, so
// the content cannot be read again, also from a copy of the blob store.
// The metadata and the hash stay.
//
// Every write in a namespace checks fence in its transaction, and returns
// namespace.ErrFenced when it is stale.
type ArtifactStore interface {
	// Shred destroys the data key of the plan or the log version with the ID
	// id and sets its ShreddedAt, with e in the same transaction. A second
	// shred changes nothing. It returns ErrNotFound for an unknown ID, and
	// ErrCurrentVersion for the current version of a log.
	Shred(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Expired returns at most limit plans and log versions whose job ended
	// before cutoff and whose key is not shredded yet, oldest first. The
	// retention schedule shreds them, then deletes their blobs.
	Expired(ctx context.Context, cutoff time.Time, limit int) ([]Expired, error)
}

// Expired is one plan or log version that the retention sweep shreds. Keys
// are its blobs. The sweep deletes them after the shred commits.
type Expired struct {
	Namespace namespace.Name
	Job       ID
	ID        string
	Keys      []blob.Key
}
