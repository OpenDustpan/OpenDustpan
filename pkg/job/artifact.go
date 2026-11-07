package job

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
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

// ID prefixes of the artifacts of a job. These are the IDs of the items,
// not of their item keys: an item key ID has the prefix of
// secret.NewItemKeyID.
const (
	// PlanIDPrefix starts the ID of a stored plan.
	PlanIDPrefix = "pln-"
	// LogVersionIDPrefix starts the ID of one version of the log of a step.
	LogVersionIDPrefix = "jlv-"
	// LogAttemptIDPrefix starts the ID of one write attempt of a log chunk.
	LogAttemptIDPrefix = "jla-"
)

// PlanPart is one part of a stored plan: one blob, sealed with the item
// key of its plan. Secret is true for a part that holds plain secret
// values, such as an OpenTofu plan file.
type PlanPart struct {
	Kind OutputKind
	// Size and SHA256 are those of the plain part.
	Size   int64
	SHA256 string
	Secret bool
	// StoredSize and StoredMD5 are the size and the lowercase hex MD5 of the
	// sealed bytes that the blob store holds. A backup copies the blob and
	// checks the copy against them. A plan that an earlier version of the
	// server stored has neither: StoredMD5 is empty, and a backup lists the
	// part as not copied.
	StoredSize int64
	StoredMD5  string
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
	// ItemKey is the ID of the item key of the plan, of the kind
	// secret.ItemKindPlan.
	ItemKey secret.ItemKeyID
	// StateVersion is the state version of the unit that the plan read.
	// The server sets it from Phase.StateVersion, which the store records
	// at the claim of the plan phase. No executor gives it. An apply phase
	// dispatches only while it is still the current version.
	StateVersion string
	Parts        []PlanPart
	CreatedAt    time.Time
	// ExpiresAt is the end of the retention period of the plan:
	// history.ExpiresAt(CreatedAt, history.RetentionSettings.Plan). The
	// server sets it at creation, so a later change of the setting applies
	// to new plans only.
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
//   - ErrNoPlan, which wraps ErrPlanStale, when the plan phase stored no
//     plan: the reconcile schedule fails the job, because a new plan phase
//     would store no plan either,
//   - ErrPlanChanged when bound is not nil and is not the binding of p,
//   - ErrPlanStale when p read another state version than current, was
//     made before notBefore (now minus the maximum plan age), or was
//     redacted.
func CheckPlan(p *PlanRef, bound *PlanBinding, current string, notBefore time.Time) error {
	if p == nil {
		return fmt.Errorf("%w: %w", ErrPlanStale, ErrNoPlan)
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
	// ItemKey is the ID of the item key of the version, of the kind
	// secret.ItemKindJobLog.
	ItemKey secret.ItemKeyID
	Version int
	// Chunks is the number of chunks. SHA256 is the hash of the plaintext of
	// all chunks in order. Both are set when the log closes.
	Chunks    int
	SHA256    string
	Size      int64
	CreatedAt time.Time
	ClosedAt  time.Time
	// ExpiresAt is the end of the retention period of the version:
	// history.ExpiresAt(CreatedAt, history.RetentionSettings.JobLog). The
	// server sets it at creation, so a later change of the setting applies
	// to new versions only.
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

// PlanItem returns the history item of the plan with the ID plan of unit.
func PlanItem(unit history.Unit, plan string) history.ItemRef {
	return history.ItemRef{Unit: unit, Kind: secret.ItemKindPlan, Item: plan}
}

// LogItem returns the history item of the log version with the ID version
// of unit.
func LogItem(unit history.Unit, version string) history.ItemRef {
	return history.ItemRef{Unit: unit, Kind: secret.ItemKindJobLog, Item: version}
}

// CheckPlanRedaction returns the error of a redaction of a stored plan
// whose job is in the state s, for every cause: history.ErrInProgress
// while the job has not ended, so that a plan that an open approval names
// stays readable, and nil after. When the job ends after the expiry time
// of the plan, the next retention sweep redacts the plan.
func CheckPlanRedaction(s State) error {
	if !s.Terminal() {
		return fmt.Errorf("%w: the job of the plan has not ended", history.ErrInProgress)
	}

	return nil
}

// CheckLogRedaction returns the error of a redaction with the cause c of
// the log version v. current is true when no later version of the log
// exists. It returns:
//   - history.ErrInProgress for a version that is open (the step runs),
//     for every cause,
//   - history.ErrLatest for the current version, except with
//     history.CauseRetention: the end of the retention period may expire
//     the current version, a request may not,
//   - nil otherwise.
func CheckLogRedaction(v *LogRef, current bool, c history.Cause) error {
	switch {
	case v.ClosedAt.IsZero():
		return fmt.Errorf("%w: log version %s is open", history.ErrInProgress, v.ID)
	case current && c != history.CauseRetention:
		return fmt.Errorf("%w: log version %s is the current version", history.ErrLatest, v.ID)
	default:
		return nil
	}
}

// PlanPartKey returns the blob key of the part k of the plan with the ID
// plan of the job id: job/<job ID>/plan/<plan ID>/<part>.
func PlanPartKey(id ID, plan string, k OutputKind) (blob.Key, error) {
	return blob.ParseKey("job/" + string(id) + "/plan/" + plan + "/" + string(k))
}

// logChunkPrefix returns the part of the blob key of the chunk seq of the log
// version with the ID version of the job id that every attempt shares:
// job/<job ID>/log/<version ID>/<seq in 8 digits>.
func logChunkPrefix(id ID, version string, seq int) string {
	return fmt.Sprintf("job/%s/log/%s/%08d", id, version, seq)
}

// LogChunkAttemptKey returns the blob key of one write attempt of the chunk
// seq of the log version with the ID version of the job id: the shared
// prefix job/<job ID>/log/<version ID>/<seq in 8 digits>, then "-", then the
// ID of the attempt. Each attempt has its own
// key, so two attempts never write one key.
func LogChunkAttemptKey(id ID, version string, seq int, attempt string) (blob.Key, error) {
	if !validIDSegment(attempt, LogAttemptIDPrefix) {
		return "", fmt.Errorf("%w: log chunk attempt %q", ErrInvalid, attempt)
	}

	return blob.ParseKey(logChunkPrefix(id, version, seq) + "-" + attempt)
}

// ChunkBlobKey returns the blob key of the chunk seq of the log version with
// the ID version of the job id, when the row of the chunk holds the blob key
// stored. The text must be exactly the key that LogChunkAttemptKey makes for
// these parts and some attempt. Any other text, the empty text, and the key of
// another chunk, another log version or another job, is ErrInvalid.
//
// Defense in depth: the store checks the same text when it writes the row, and
// the blob view checks the namespace. This check stays so that a damaged or
// restored row never makes a reader open a blob that is not the blob of its
// chunk.
func ChunkBlobKey(id ID, version string, seq int, stored string) (blob.Key, error) {
	attempt, ok := strings.CutPrefix(stored, logChunkPrefix(id, version, seq)+"-")
	if !ok {
		return "", fmt.Errorf("%w: blob key of chunk %d of log %s", ErrInvalid, seq, version)
	}

	return LogChunkAttemptKey(id, version, seq, attempt)
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
// log version (secret.ItemSealer). The item key follows the rules of the
// history model: the server writes the wrapped item key as a blob outside
// SQL first (secret.ItemKeyBlobs.Key names the blob and secret.ItemKeyBlobs.Put
// writes it), and then its key record in the fenced transaction that creates
// the plan or the log version.
//
// The adapter implements history.Redactor for the items of the kinds
// secret.ItemKindPlan and secret.ItemKindJobLog, with the rules of
// CheckPlanRedaction and CheckLogRedaction. A redaction by a person
// (history.CauseRequest) and the end of a retention period
// (history.CauseRetention) take this one path. Its conformance suite is
// historytest.RunRedactor, with historytest.JobArtifactSituations for plans
// and historytest.JobLogSituations for log versions.
//
// The reads are the port ArtifactReader, which ArtifactReads implements over
// the record store, the blob store and the item keys. The redaction and
// ReplaceLog need the history model and have no implementation yet.
//
// Every write in a namespace checks fence in its transaction, and returns
// namespace.ErrFenced when it is stale.
type ArtifactStore interface {
	history.Redactor
	ArtifactReader
	// ReplaceLog writes the next version of the log whose current version
	// is from, with the content body and a new item key, and makes it the
	// current version. by is the subject of the redaction. It returns
	// history.ErrInProgress for a log that is not closed, ErrConflict when
	// from is not the current version, and secret.ErrShredded when the item
	// key of from is shredded. The caller then redacts from through
	// history.Redactor, with history.CauseRequest.
	ReplaceLog(ctx context.Context, ns namespace.Name, from string, body io.Reader, by string,
		fence namespace.Fence) (LogRef, error)
}

// ArtifactKey is what a blob key in the directory of one job names: one part
// of a plan, or one write attempt of a chunk of a log version. Exactly one of
// Plan and Version is set.
type ArtifactKey struct {
	// Plan is the ID of the plan of a plan part, and Kind its part.
	Plan string
	Kind OutputKind
	// Version is the ID of the log version of a chunk, and Seq its number.
	Version string
	Seq     int
}

// ParseArtifactKey returns what the blob key rel names in the directory of
// the job id. rel is relative to the namespace, as PlanPartKey and
// LogChunkAttemptKey make it. Any other text, also the key of another job, is
// ErrInvalid. It is an allowlist: the sweep of the artifact blobs deletes
// only a key that it parses.
func ParseArtifactKey(id ID, rel string) (ArtifactKey, error) {
	parts := strings.Split(rel, "/")

	const segments = 5

	invalid := fmt.Errorf("%w: %q is not an artifact blob key of job %s", ErrInvalid, rel, id)

	if len(parts) != segments || parts[0] != artifactDir || parts[1] != string(id) {
		return ArtifactKey{}, invalid
	}

	switch parts[2] {
	case "plan":
		return parsePlanPart(id, rel, parts[3], OutputKind(parts[4]), invalid)
	case "log":
		return parseChunkAttempt(id, rel, parts[3], parts[4], invalid)
	default:
		return ArtifactKey{}, invalid
	}
}

// parsePlanPart returns the key of the part kind of the plan, when rel is
// exactly the key that PlanPartKey makes for them, else invalid.
func parsePlanPart(id ID, rel, plan string, kind OutputKind, invalid error) (ArtifactKey, error) {
	if checkParts(id, PlanIDPrefix, plan) != nil || (kind != OutputPlanDisplay && kind != OutputPlanApply) {
		return ArtifactKey{}, invalid
	}

	if key, err := PlanPartKey(id, plan, kind); err != nil || key.String() != rel {
		return ArtifactKey{}, invalid
	}

	return ArtifactKey{Plan: plan, Kind: kind, Version: "", Seq: 0}, nil
}

// parseChunkAttempt returns the key of the chunk of the log version, when rel
// is exactly the key of an attempt that ChunkBlobKey accepts, else invalid.
func parseChunkAttempt(id ID, rel, version, name string, invalid error) (ArtifactKey, error) {
	digits, _, ok := strings.Cut(name, "-")
	if !ok || checkParts(id, LogVersionIDPrefix, version) != nil {
		return ArtifactKey{}, invalid
	}

	seq, err := strconv.Atoi(digits)
	if err != nil || seq < 0 || seq >= MaxLogChunks {
		return ArtifactKey{}, invalid
	}

	if key, keyErr := ChunkBlobKey(id, version, seq, rel); keyErr != nil || key.String() != rel {
		return ArtifactKey{}, invalid
	}

	return ArtifactKey{Plan: "", Kind: "", Version: version, Seq: seq}, nil
}
