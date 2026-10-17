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

// DefaultMaxPlanAge is the oldest plan that an apply phase applies. An
// installation setting changes it. An older plan is stale: the job plans
// again and asks for approval again.
const DefaultMaxPlanAge = 24 * time.Hour

var (
	// ErrShredded reports a read of a plan or a log version whose data key
	// the server destroyed. The metadata and the hash stay.
	ErrShredded = errors.New("content was shredded")
	// ErrCurrentVersion reports a redaction shred of the current version of
	// a log. A redaction writes a new version first, and then shreds only
	// the older one. The retention sweep shreds every version through
	// ArtifactStore.ShredExpired, and never gets this error.
	ErrCurrentVersion = errors.New("the current log version cannot be shredded")
	// ErrLogOpen reports a redaction of the log of a step that still runs.
	ErrLogOpen = errors.New("the log is still open")
)

// PlanRef names a stored plan and what a reader can know without its
// content. ID has the prefix pln-, and is also the ID of the data key of
// the plan. SHA256 is the hash of the plaintext. An approval binds to it,
// and the apply phase refuses a plan with another hash.
type PlanRef struct {
	ID     string
	Kind   OutputKind
	SHA256 string
	// StateVersion is the state version of the unit that the plan read. An
	// apply phase dispatches only while it is still the current version.
	StateVersion string
	Size         int64
	CreatedAt    time.Time
	// ShreddedAt is the time that the server destroyed the data key, or the
	// zero time.
	ShreddedAt time.Time
}

// PlanSet is the two stored outputs of one plan phase: the readable plan
// that people approve, and the plan that the apply phase applies. An
// approval binds to both.
type PlanSet struct {
	Display *PlanRef
	Apply   *PlanRef
}

// Binding returns what an approval of s binds to, or the zero binding for
// a set with no plan.
func (s PlanSet) Binding() PlanBinding {
	var b PlanBinding

	if s.Display != nil {
		b.DisplaySHA256 = s.Display.SHA256
		b.StateVersion = s.Display.StateVersion
	}

	if s.Apply != nil {
		b.ApplySHA256 = s.Apply.SHA256
		b.StateVersion = s.Apply.StateVersion
	}

	return b
}

// PlanBinding is what an approval binds to: the hashes of both plans and
// the state version that they read.
type PlanBinding struct {
	DisplaySHA256 string
	ApplySHA256   string
	StateVersion  string
}

// CheckPlan returns the error of the dispatch of an apply phase with the
// plans s, approved with bound, while current is the current state version
// of the unit. It returns ErrPlanChanged when bound is not the binding of
// s, and ErrPlanStale when the plans read another state version than
// current or were made before notBefore (now minus the maximum plan age).
// A set with no plan passes: an apply phase with no plan phase before it,
// such as a refresh, has nothing to bind.
func CheckPlan(s PlanSet, bound *PlanBinding, current string, notBefore time.Time) error {
	if s.Display == nil && s.Apply == nil {
		return nil
	}

	b := s.Binding()
	if bound != nil && *bound != b {
		return fmt.Errorf("%w: the approval binds to other plans", ErrPlanChanged)
	}

	if b.StateVersion != current {
		return fmt.Errorf("%w: the plan read state version %q, the current one is %q", ErrPlanStale,
			b.StateVersion, current)
	}

	for _, p := range []*PlanRef{s.Display, s.Apply} {
		if p != nil && p.CreatedAt.Before(notBefore) {
			return fmt.Errorf("%w: plan %s is older than the maximum plan age", ErrPlanStale, p.ID)
		}
	}

	return nil
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
	// Shred destroys the data key of the plan or the older log version with
	// the ID id, for a redaction, and sets its ShreddedAt, with e in the
	// same transaction. A second shred changes nothing. It returns
	// ErrNotFound for an unknown ID, and ErrCurrentVersion for the current
	// version of a log.
	Shred(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// ShredExpired destroys the data key of a plan or of any log version,
	// the current one too, for the retention sweep. It has the rules of
	// Shred, except that it never returns ErrCurrentVersion.
	ShredExpired(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error
	// Expired returns at most limit plans and log versions whose job ended
	// before cutoff and whose key is not shredded yet, oldest first. The
	// retention schedule shreds them, then deletes their blobs.
	Expired(ctx context.Context, cutoff time.Time, limit int) ([]Expired, error)
}

// Expired is one plan or log version that the retention sweep shreds with
// ArtifactStore.ShredExpired. Keys
// are its blobs. The sweep deletes them after the shred commits.
type Expired struct {
	Namespace namespace.Name
	Job       ID
	ID        string
	Keys      []blob.Key
}
