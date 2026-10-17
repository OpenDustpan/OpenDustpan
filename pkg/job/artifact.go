package job

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// DefaultMaxPlanAge is the oldest plan that an apply phase applies. An
// installation setting changes it. An older plan is stale: the job plans
// again and asks for approval again.
const DefaultMaxPlanAge = 24 * time.Hour

// Limits of plans and logs.
const (
	// MaxLogChunk is the largest chunk of a log that AppendLog accepts.
	MaxLogChunk = 1 << 20
	// MaxPlanPart is the largest part of a plan that PutPlan accepts.
	MaxPlanPart = 256 << 20
)

// ID prefixes of the artifacts of a job.
const (
	// PlanIDPrefix starts the ID of a stored plan.
	PlanIDPrefix = "pln-"
	// LogVersionIDPrefix starts the ID of one version of the log of a step.
	LogVersionIDPrefix = "jlv-"
)

var (
	// ErrRedacted reports a read of a plan or a log version whose content
	// the history model redacted: by a request, or at the end of its
	// retention. Its item key is shredded. The metadata and the hashes stay.
	ErrRedacted = errors.New("content was redacted")
	// ErrLogOpen reports a redaction of the log of a step that still runs.
	ErrLogOpen = errors.New("the log is still open")
)

// PlanPart is one part of a stored plan: one blob, sealed with the item
// key of its plan. Secret is true for a part that holds plain secret
// values, such as an OpenTofu plan file.
type PlanPart struct {
	Kind   OutputKind
	Size   int64
	SHA256 string
	Secret bool
}

// PlanRef names the stored plan of a plan phase and what a reader can know
// without its content. A plan is one history item, with two parts: the
// readable plan (OutputPlanDisplay) and the plan that the apply phase
// reads (OutputPlanApply). One item key seals both parts. An approval binds
// to the hashes of both parts and to the one state version that the plan
// read, so the two parts can never read different state versions.
type PlanRef struct {
	// ID has the prefix pln-.
	ID string
	// ItemKey is the ID of the item key of the plan, with the prefix ik-.
	ItemKey string
	// StateVersion is the state version of the unit that the plan read.
	// The server sets it from Phase.StateVersion, which the store records
	// at the claim of the plan phase. No executor gives it. An apply phase
	// dispatches only while it is still the current version.
	StateVersion string
	Parts        []PlanPart
	CreatedAt    time.Time
	// ExpiresAt is the end of the retention of the plan. The server sets it
	// at creation from the plan retention of the history model.
	ExpiresAt time.Time
	// RedactedAt is the time that the history model redacted the plan, or
	// the zero time.
	RedactedAt time.Time
}

// Part returns the part of kind k of p, or nil when p has none.
func (p *PlanRef) Part(k OutputKind) *PlanPart {
	if p == nil {
		return nil
	}

	for i := range p.Parts {
		if p.Parts[i].Kind == k {
			return &p.Parts[i]
		}
	}

	return nil
}

// hash returns the SHA-256 of p, or empty for no part.
func (p *PlanPart) hash() string {
	if p == nil {
		return ""
	}

	return p.SHA256
}

// Binding returns what an approval of p binds to, or the zero binding for
// no plan.
func (p *PlanRef) Binding() PlanBinding {
	if p == nil {
		return PlanBinding{Plan: "", DisplaySHA256: "", ApplySHA256: "", StateVersion: ""}
	}

	return PlanBinding{
		Plan: p.ID, DisplaySHA256: p.Part(OutputPlanDisplay).hash(), ApplySHA256: p.Part(OutputPlanApply).hash(),
		StateVersion: p.StateVersion,
	}
}

// PlanBinding is what an approval binds to: the plan, the hashes of both
// of its parts, and the state version that it read.
type PlanBinding struct {
	Plan          string
	DisplaySHA256 string
	ApplySHA256   string
	StateVersion  string
}

// CheckPlan returns the error of the dispatch of an apply phase that comes
// after a plan phase, with the stored plan p of that phase, approved with
// bound, while current is the current state version of the unit. The
// caller calls it only when a plan phase comes before the apply phase: an
// apply phase with no plan phase, such as a refresh, has nothing to check.
// It returns:
//   - ErrPlanStale when the plan phase stored no plan, so the job plans
//     again,
//   - ErrPlanChanged when bound is not nil and is not the binding of p,
//   - ErrPlanStale when p read another state version than current, was
//     made before notBefore (now minus the maximum plan age), or was
//     redacted.
func CheckPlan(p *PlanRef, bound *PlanBinding, current string, notBefore time.Time) error {
	if p == nil {
		return fmt.Errorf("%w: the plan phase stored no plan", ErrPlanStale)
	}

	if bound != nil && *bound != p.Binding() {
		return fmt.Errorf("%w: the approval binds to other plans", ErrPlanChanged)
	}

	switch {
	case p.StateVersion != current:
		return fmt.Errorf("%w: the plan read state version %q, the current one is %q", ErrPlanStale,
			p.StateVersion, current)
	case p.CreatedAt.Before(notBefore):
		return fmt.Errorf("%w: plan %s is older than the maximum plan age", ErrPlanStale, p.ID)
	case !p.RedactedAt.IsZero():
		return fmt.Errorf("%w: plan %s was redacted", ErrPlanStale, p.ID)
	default:
		return nil
	}
}

// LogRef names one version of the log of one step. Each version is one
// history item, with its own item key. Version starts at 1. A redaction by
// substitution writes the next version and then redacts the older one
// through the history model.
type LogRef struct {
	// ID has the prefix jlv-.
	ID string
	// ItemKey is the ID of the item key of the version, with the prefix
	// ik-.
	ItemKey string
	Version int
	// Chunks is the number of chunks. SHA256 is the hash of the plaintext of
	// all chunks in order. Both are set when the log closes.
	Chunks    int
	SHA256    string
	Size      int64
	CreatedAt time.Time
	ClosedAt  time.Time
	// ExpiresAt is the end of the retention of the version. The server sets
	// it at creation from the job log retention of the history model.
	ExpiresAt time.Time
	// RedactedAt is the time that the history model redacted the version,
	// or the zero time.
	RedactedAt time.Time
	// ReplacedBy is the subject that wrote this version by a redaction, or
	// empty for the first version. Matches is the number of matches that
	// the redaction replaced. The server never keeps the text that it
	// replaced.
	ReplacedBy string
	Matches    int
}

// PlanPartKey returns the blob key of the part k of the plan with the ID
// plan of the job id: job/<job ID>/plan/<plan ID>/<part>.
func PlanPartKey(id ID, plan string, k OutputKind) (blob.Key, error) {
	return blob.ParseKey("job/" + string(id) + "/plan/" + plan + "/" + string(k))
}

// LogChunkKey returns the blob key of chunk seq of the log version with the
// ID version of the job id: job/<job ID>/log/<version ID>/<seq in 8
// digits>.
func LogChunkKey(id ID, version string, seq int) (blob.Key, error) {
	return blob.ParseKey(fmt.Sprintf("job/%s/log/%s/%08d", id, version, seq))
}

// Substitution is one change to the text of a log for a redaction. The
// server replaces each match of Match with the text [redacted]. The server
// keeps the number of matches, never Match.
type Substitution struct {
	Match []byte
}

// PlanUpload is one part of a plan that an executor uploads.
type PlanUpload struct {
	Kind OutputKind
	Body io.Reader
}

// ArtifactStore is the persistence port for the content of plans and logs.
// The content is in the blob store, sealed with the item key of its plan or
// log version. The item key, its blob and its shred are the rules of the
// history model: the server writes the wrapped item key as a blob first,
// and then its key record in the fenced transaction that creates the plan
// or the log version. The adapter also implements the redactor port of the
// history model for plans and log versions, so that a redaction and the
// end of a retention shred the item key in the same way as for a state
// item.
//
// Every write in a namespace checks fence in its transaction, and returns
// namespace.ErrFenced when it is stale.
type ArtifactStore interface {
	// OpenPlanPart returns the plaintext of the part k of the plan with the
	// ID plan. It returns ErrNotFound for an unknown plan or part, and
	// ErrRedacted for a plan that the history model redacted. The caller
	// checks the actions, and masks the readable plan.
	OpenPlanPart(ctx context.Context, ns namespace.Name, plan string, k OutputKind) (io.ReadCloser, error)
	// OpenLog returns the plaintext of the log version with the ID version,
	// its chunks in order. It returns ErrNotFound for an unknown version,
	// and ErrRedacted for a version that the history model redacted.
	OpenLog(ctx context.Context, ns namespace.Name, version string) (io.ReadCloser, error)
	// ReplaceLog writes the next version of the log whose current version
	// is from, with the content body and a new item key, and makes it the
	// current version. by is the subject of the redaction. It returns
	// ErrLogOpen for a log that is not closed, ErrConflict when from is not
	// the current version, and ErrRedacted when from was redacted. The
	// caller then redacts from through the history model.
	ReplaceLog(ctx context.Context, ns namespace.Name, from string, body io.Reader, by string,
		fence namespace.Fence) (LogRef, error)
}
