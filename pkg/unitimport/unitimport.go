// Package unitimport holds the tool-neutral parts of an import: the move of
// units with their state and history from a source into a namespace, and
// the move check that compares the moved units with their source.
//
// An import belongs to one namespace and names one source. It holds one
// import unit for each unit that it moves. Each import unit keeps a ledger
// with one row for each source version that it committed, so a resume
// starts at the first version with no row.
//
// The package has four ports. A Source reads one kind of source. A Tool
// writes the versions of one tool into its own model, so this package never
// reads a checkpoint or a state file. A Store and a UnitStore keep imports
// and import units, and apply each transition in a fenced transaction.
//
// An import never stores a source credential. A Credential lives in memory
// only, and it never prints.
package unitimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Sentinel errors of the import ports.
var (
	// ErrInvalid reports a request or a record with an empty or bad field.
	ErrInvalid = errors.New("import request is not valid")
	// ErrNotFound reports an import or an import unit that does not exist.
	ErrNotFound = errors.New("import not found")
	// ErrPurged reports an import record that the retention purged. Its
	// ledger rows and the import ID of each history item stay.
	ErrPurged = errors.New("import purged")
	// ErrTransition reports a change of state that the transition table
	// does not allow.
	ErrTransition = errors.New("import state change is not allowed")
	// ErrLedgerExists reports a second ledger row for one import, one unit
	// and one source version.
	ErrLedgerExists = errors.New("ledger row already exists")
	// ErrTargetExists reports a target name that a unit of the namespace
	// already holds, deleted units included, and that the hold of the same
	// import does not cover.
	ErrTargetExists = errors.New("target unit already exists")
	// ErrTargetDeleted reports a write to a target unit that is deleted.
	ErrTargetDeleted = errors.New("target unit is deleted")
	// ErrHoldRemoved reports a write to a target unit that the import hold
	// no longer covers.
	ErrHoldRemoved = errors.New("import hold no longer covers the unit")
	// ErrAddressInvalid reports a source address that is not an absolute
	// URL with a host, or that holds userinfo, a query or a fragment.
	ErrAddressInvalid = errors.New("source address is not valid")
	// ErrAddressMismatch reports a source address whose host is not the
	// host that the login block of the profile declares.
	ErrAddressMismatch = errors.New("source address does not match the login block")
	// ErrCredentialNeeded reports a run step that has no credential: the
	// instance that held an ephemeral credential stopped or lost its claim.
	ErrCredentialNeeded = errors.New("source credential is needed")
	// ErrReleased reports a resume or a cancel of a released import.
	ErrReleased = errors.New("import is released")
	// ErrSourceNotFound reports a source unit or a source version that the
	// source does not have.
	ErrSourceNotFound = errors.New("source unit or version not found")
	// ErrSourceDenied reports a source that refused the credential.
	ErrSourceDenied = errors.New("source refused the credential")
	// ErrSourceUnreachable reports a network error, a timeout or a 5xx
	// answer of the source.
	ErrSourceUnreachable = errors.New("source cannot be reached")
	// ErrSourceInvalid reports an answer of the source that the adapter
	// cannot parse, or a body above the size limit.
	ErrSourceInvalid = errors.New("source answer is not valid")
	// ErrSourceChanged reports a source version that the ledger holds with
	// another digest.
	ErrSourceChanged = errors.New("source history changed")
	// ErrUnsupported reports an operation that the source kind does not
	// have, for example a lock or a decrypt.
	ErrUnsupported = errors.New("source does not support the operation")
)

// SourceKind names the protocol that a source adapter speaks, not a
// product.
type SourceKind string

// The source kinds of the first adapters.
const (
	// SourceTFEAPI reads a TFE-compatible API.
	SourceTFEAPI SourceKind = "tfe-api"
	// SourcePulumiService reads a Pulumi Cloud-compatible API.
	SourcePulumiService SourceKind = "pulumi-service"
	// SourceS3 reads an S3-compatible bucket.
	SourceS3 SourceKind = "s3"
	// SourceOpenTofuHTTP reads the OpenTofu http backend.
	SourceOpenTofuHTTP SourceKind = "opentofu-http"
	// SourceUpload reads the versions that a client sends.
	SourceUpload SourceKind = "upload"
)

// redacted is the text of a Credential in every printed form.
const redacted = "[credential]"

// Credential is a source credential in memory. It has no exported field,
// it prints as "[credential]" with every fmt verb, and its JSON and text
// encodings fail, so it cannot reach a log line, an error text or a stored
// record.
type Credential struct {
	// secret returns the bytes. fmt prints a func value as an address, so
	// a Credential in an unexported field of another value, where fmt
	// cannot call Format, still never prints the bytes.
	secret func() []byte
}

// NewCredential returns a Credential that holds a copy of b.
func NewCredential(b []byte) Credential {
	b = append([]byte(nil), b...)

	return Credential{secret: func() []byte { return b }}
}

// Bytes returns a copy of the credential for one request of an adapter.
func (c Credential) Bytes() []byte {
	if c.secret == nil {
		return nil
	}

	return append([]byte(nil), c.secret()...)
}

// Empty reports whether c holds no credential.
func (c Credential) Empty() bool { return c.secret == nil || len(c.secret()) == 0 }

// Clear writes zeros over the bytes of c. Each copy of c shares these
// bytes, so Clear clears every copy, and Empty stays false. Clear is best
// effort: a copy that Bytes returned, or a copy that the runtime made,
// stays in memory until the garbage collector frees it.
func (c Credential) Clear() {
	if c.secret != nil {
		clear(c.secret())
	}
}

// Format writes the redacted form for every verb, so an integer or a rune
// verb (%d, %o, %b, %c, %U) never prints the bytes.
func (Credential) Format(f fmt.State, _ rune) {
	// fmt.State writes to the buffer of fmt, which never fails, and Format
	// has no error to return.
	if _, err := io.WriteString(f, redacted); err != nil {
		return
	}
}

// String returns the redacted form.
func (Credential) String() string { return redacted }

// GoString returns the redacted form.
func (Credential) GoString() string { return redacted }

// LogValue returns the redacted form.
func (Credential) LogValue() slog.Value { return slog.StringValue(redacted) }

// errEncode reports an encoding of a Credential.
var errEncode = errors.New("a source credential is never encoded")

// MarshalJSON always fails.
func (Credential) MarshalJSON() ([]byte, error) { return nil, errEncode }

// MarshalText always fails.
func (Credential) MarshalText() ([]byte, error) { return nil, errEncode }

// CheckAddress checks a source address. The address is an absolute URL
// with a host, and it holds no userinfo, no query and no fragment, so no
// credential is in a stored address. It wraps ErrAddressInvalid.
func CheckAddress(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: the address does not parse as a URL", ErrAddressInvalid)
	}

	switch {
	case u.Scheme == "" || u.Host == "" || u.Opaque != "":
		return fmt.Errorf("%w: the address needs a scheme and a host", ErrAddressInvalid)
	case u.Scheme != "https" && u.Scheme != "s3":
		return fmt.Errorf("%w: the scheme must be https or s3", ErrAddressInvalid)
	case u.User != nil:
		return fmt.Errorf("%w: the address holds userinfo", ErrAddressInvalid)
	case u.RawQuery != "" || u.ForceQuery:
		return fmt.Errorf("%w: the address holds a query", ErrAddressInvalid)
	case strings.Contains(raw, "#"):
		return fmt.Errorf("%w: the address holds a fragment", ErrAddressInvalid)
	}

	return nil
}

// MaxSourceActorBytes is the limit of a source actor in bytes.
const MaxSourceActorBytes = 256

// SanitizeSourceActor returns the text of a source actor that a history
// item can keep: valid UTF-8 with no control character, at most
// MaxSourceActorBytes bytes. A source actor is a claim of the source, so
// a reader shows it as text only.
func SanitizeSourceActor(s string) string {
	var b strings.Builder

	for _, r := range strings.ToValidUTF8(s, string(utf8.RuneError)) {
		if unicode.IsControl(r) {
			continue
		}

		if b.Len()+utf8.RuneLen(r) > MaxSourceActorBytes {
			break
		}

		b.WriteRune(r)
	}

	return b.String()
}

// Selection names the units of a source that an import reads: each name,
// or every unit when Names is empty.
type Selection struct {
	Names []string
}

// SourceUnit is one unit of a source. Name is the name in the source, for
// example "acme/web/prod" or a workspace name. The core keeps it opaque.
type SourceUnit struct {
	Tool string
	Name string
}

// VersionID identifies a version in its source. The core keeps it opaque.
type VersionID string

// SourceVersion is one version of a source unit. At is the time in the
// source, and Actor is the user or the run in the source, as text. Both
// are claims of the source. Metadata is the tool part of the version as
// JSON, for example the kind and message of a Pulumi update, or the serial
// and lineage of a state version. Only the Tool port reads it.
// ClientReported is true when a client sent At, Actor and Metadata with an
// upload, so a reader labels them "reported by the client".
type SourceVersion struct {
	ID             VersionID
	At             time.Time
	Size           int64
	SHA256         string
	Actor          string
	Metadata       []byte
	ClientReported bool
}

// Source reads one kind of source. Each method wraps ErrSourceNotFound,
// ErrSourceDenied, ErrSourceUnreachable, ErrSourceInvalid or
// ErrUnsupported. No error text holds the credential, a URL query or a byte
// of state.
type Source interface {
	// Kind returns the source kind of the adapter.
	Kind() SourceKind
	// Units lists the units that sel names.
	Units(ctx context.Context, cred Credential, sel Selection) ([]SourceUnit, error)
	// Versions lists at most limit versions of u after the version after,
	// oldest first. An empty after starts at the oldest version.
	Versions(ctx context.Context, cred Credential, u SourceUnit, after VersionID, limit int) ([]SourceVersion, error)
	// Open streams the bytes of one version. The caller closes the reader.
	Open(ctx context.Context, cred Credential, u SourceUnit, id VersionID) (io.ReadCloser, error)
	// Head returns the ID of the newest version of u.
	Head(ctx context.Context, cred Credential, u SourceUnit) (VersionID, error)
}

// Decrypter is the optional capability of a source that decrypts the
// ciphertexts of one unit through its own API. It returns one plain value
// for each ciphertext, in the same order.
type Decrypter interface {
	Decrypt(ctx context.Context, cred Credential, u SourceUnit, ciphertexts [][]byte) ([][]byte, error)
}

// Locker is the optional capability of a source that locks one unit during
// its move.
type Locker interface {
	Lock(ctx context.Context, cred Credential, u SourceUnit) error
	Unlock(ctx context.Context, cred Credential, u SourceUnit) error
}

// DecryptFunc decrypts a batch of ciphertexts of the source unit of a
// write. It is nil when the source has no Decrypter.
type DecryptFunc func(ctx context.Context, ciphertexts [][]byte) ([][]byte, error)

// LedgerRow records one committed source version of an import unit. A
// ledger row stays with its unit after the purge of its import record.
type LedgerRow struct {
	ImportID     string
	ImportUnitID string
	Version      VersionID
	SourceSHA256 string
	StoredSHA256 string
	Item         history.ItemRef
	CommittedAt  time.Time
}

// CreateRequest asks a Tool to create the target unit of an import unit,
// with the import hold on it. Description and Tags are the source
// metadata of the unit: the stack tags of a Pulumi stack, or the
// description and tags of an OpenTofu workspace.
type CreateRequest struct {
	Fence        namespace.Fence
	Name         string
	ImportID     string
	ImportUnitID string
	LockHolder   string
	Description  string
	Tags         map[string]string
}

// WriteRequest asks a Tool to write one source version as one history
// item. The Tool writes the item, the ledger row and the inventory publish
// in one fenced transaction. Ledger.ImportID names the import whose hold
// must cover the unit.
type WriteRequest struct {
	Fence   namespace.Fence
	Unit    history.Unit
	Version SourceVersion
	Body    io.Reader
	Ledger  LedgerRow
	Decrypt DecryptFunc
}

// WriteResult is the outcome of one write.
type WriteResult struct {
	Item         history.ItemRef
	StoredSHA256 string
	Reencrypted  int
}

// Facts are the values that the move check compares for one state. Digest
// is the digest that the tool defines: for the Pulumi CLI the masked
// digest of the deployment, for OpenTofu the SHA-256 of the bytes. Extra
// holds tool facts as text, for example the serial and the lineage. Opaque
// is true for a state that a client encrypted.
type Facts struct {
	Digest        string
	ResourceCount int
	SecretCount   int
	Opaque        bool
	Extra         map[string]string
}

// Tool writes the imported versions of one tool into its own model.
// Create and Write publish the unit to the inventory, with the import ID,
// in their own transaction. Create wraps ErrTargetExists. Write wraps
// ErrSourceChanged when the ledger holds the version with another digest,
// ErrHoldRemoved when the import hold no longer covers the unit, and
// ErrTargetDeleted for a deleted unit. Both wrap namespace.ErrFenced for a
// stale fence. Write wraps ErrLedgerExists for a version that the ledger
// already holds with the same digest. LatestFacts wraps history.ErrItemNotFound for a unit with no
// state.
type Tool interface {
	// Tool returns the tool name of the units, as in history.Unit.
	Tool() string
	// Create creates the target unit with the import hold on it, and
	// records the unit as the Target of the import unit, in one
	// transaction. Create is idempotent: when the name exists and the hold
	// of the same import covers it, Create returns that unit.
	Create(ctx context.Context, req CreateRequest) (history.Unit, error)
	// Write writes one source version as one history item. It checks in
	// its transaction that the hold of the import still covers the unit.
	Write(ctx context.Context, req WriteRequest) (WriteResult, error)
	// Facts reads the facts of the bytes of one version.
	Facts(ctx context.Context, body io.Reader) (Facts, error)
	// LatestFacts reads the facts of the latest state item of u.
	LatestFacts(ctx context.Context, u history.Unit) (Facts, error)
}

// State is the state of an import.
type State string

// The states of an import.
const (
	StatePlanning State = "planning"
	StatePlanned  State = "planned"
	StateRunning  State = "running"
	StatePaused   State = "paused"
	StateHolding  State = "holding"
	StateReleased State = "released"
	StateFailed   State = "failed"
	StateCanceled State = "canceled"
)

// UnitState is the state of an import unit.
type UnitState string

// The states of an import unit.
const (
	UnitPlanned  UnitState = "planned"
	UnitSkipped  UnitState = "skipped"
	UnitRunning  UnitState = "running"
	UnitPaused   UnitState = "paused"
	UnitImported UnitState = "imported"
	UnitFailed   UnitState = "failed"
	UnitCanceled UnitState = "canceled"
)

// CredentialWay names how a run gets its source credential.
type CredentialWay string

// The ways of a source credential.
const (
	// CredentialProfile opens a login block of a profile at each run step.
	CredentialProfile CredentialWay = "profile"
	// CredentialEphemeral takes the credential in the run call and keeps
	// it in the memory of one instance.
	CredentialEphemeral CredentialWay = "ephemeral"
	// CredentialNone needs no credential, for example the upload source.
	CredentialNone CredentialWay = "none"
)

// Options are the options of an import.
type Options struct {
	// HistoryLimit keeps the newest versions only. Zero keeps all.
	HistoryLimit int
	// Since keeps the versions from this time on. The zero time keeps all.
	Since time.Time
	// LockSource locks each source unit during its move.
	LockSource bool
}

// Capability is an optional capability of a source.
type Capability string

// The capabilities of a source.
const (
	// CapabilityDecrypt is the Decrypter capability.
	CapabilityDecrypt Capability = "decrypt"
	// CapabilityLock is the Locker capability.
	CapabilityLock Capability = "lock"
)

// Plan is the import plan that a dry run writes. Requests lists each
// request to the source that a person cannot see in the source, for
// example the sample decrypt of each unit or a lock. Reservations are the
// IDs of the quota reservations of the import; a cancel frees each, a
// release settles each. The list is empty until a run reserves.
type Plan struct {
	At           time.Time
	Units        int
	Versions     int
	Bytes        int64
	Capabilities []Capability
	Findings     []Finding
	Requests     []string
	Reservations []string
}

// Import is one import of a namespace. EndedAt is the time of its release
// or its cancel. The retention purges the record 400 days after it.
type Import struct {
	ID          string
	Namespace   namespace.Name
	Kind        SourceKind
	Address     string
	Selection   Selection
	Credential  CredentialWay
	Profile     string
	ProfilePath string
	Options     Options
	State       State
	Reason      string
	CreatedBy   string
	CreatedAt   time.Time
	Plan        Plan
	EndedAt     time.Time
}

// ImportUnit is one unit that an import moves. Versions, Bytes,
// Encryption and Findings come from the plan. Committed counts the
// ledger rows. A failed unit with committed versions is partial.
type ImportUnit struct {
	ID         string
	ImportID   string
	Source     SourceUnit
	TargetName string
	Target     history.Unit
	State      UnitState
	ErrorCode  string
	Versions   int
	Committed  int
	Bytes      int64
	// Encryption is how the tool protects the secrets of the unit, as the
	// Tool port names it: for example the Pulumi secrets provider, or
	// "client" for client-encrypted OpenTofu state.
	Encryption string
	Findings   []Finding
}

// Filter selects imports of a namespace.
type Filter struct {
	States []State
}

// Move is a change of state of an import: the state that the caller expects
// the import to be in, the state to move to, and the reason.
type Move struct {
	From   State
	To     State
	Reason string
}

// UnitKey names one import unit: its import and its ID in the import.
type UnitKey struct {
	ImportID string
	UnitID   string
}

// UnitMove is a change of state of an import unit: the state that the caller
// expects the unit to be in, the state to move to, and the error code of a
// failed or skipped unit.
type UnitMove struct {
	From UnitState
	To   UnitState
	Code string
}

// Counts are the counts of an import unit: its source versions and the
// versions that it committed.
type Counts struct {
	Versions  int
	Committed int
}

// Store keeps imports and their plans. Each write takes the fence of the
// namespace and the audit entry of the write, and checks the fence and
// appends the entry in the same transaction. The entry names the namespace
// of the fence. Create wraps ErrInvalid. Get wraps ErrNotFound, and
// ErrPurged for a purged import. Transition wraps ErrTransition for a
// change that the transition table does not allow, or when the import is
// not in the state from, or for a move from StatePaused to StatePlanning
// of an import that has ledger rows. Every method that takes an ID wraps ErrNotFound
// and ErrPurged.
type Store interface {
	// Create stores a new import in StatePlanning. The caller gives the ID.
	// Create sets the state, and it ignores the plan and the end time of
	// imp.
	Create(ctx context.Context, fence namespace.Fence, imp Import, e audit.Entry) (Import, error)
	// Get returns one import.
	Get(ctx context.Context, ns namespace.Name, id string) (Import, error)
	// List returns the imports of a namespace that f selects.
	List(ctx context.Context, ns namespace.Name, f Filter) ([]Import, error)
	// Transition moves an import from m.From to m.To, with a reason.
	// A move to StateReleased or StateCanceled sets the end time to the
	// time of the audit entry.
	Transition(ctx context.Context, fence namespace.Fence, id string, m Move, e audit.Entry) error
	// SetPlan replaces the plan of an import in StatePlanning or
	// StatePlanned. In each other state it replaces the reservations of the
	// plan only and keeps the rest of the stored plan.
	SetPlan(ctx context.Context, fence namespace.Fence, id string, plan Plan, e audit.Entry) error
	// Purge deletes the record of an ended import (StateReleased or
	// StateCanceled) and its import units, and keeps its ID as purged. It
	// deletes no ledger row and no history item. Purge wraps ErrTransition
	// for an import that did not end.
	Purge(ctx context.Context, fence namespace.Fence, id string, e audit.Entry) error
}

// UnitStore keeps the import units of an import and reads their ledgers.
// The Tool port writes each ledger row in the transaction of its history
// item, and the Target of each import unit in the transaction of Create.
// Each write takes the fence of the namespace and the audit entry of the
// write, and checks the fence and appends the entry in the same
// transaction. Units and Unit wrap ErrNotFound and ErrPurged. Ledger wraps
// ErrNotFound for an import that never existed, and it still answers the
// rows of a purged import. Ledger returns an empty list for a unit ID that
// the import does not hold. A second ledger row for one import, one unit and
// one version wraps ErrLedgerExists. A unit of an import that ended
// (StateReleased or StateCanceled) takes no change except a move to
// UnitCanceled: TransitionUnit and SetCounts wrap ErrTransition. PutUnits wraps ErrInvalid for a bad unit and
// ErrTransition for an import that is not in StatePlanning or StatePlanned.
// TransitionUnit wraps ErrTransition for a change that the transition table
// does not allow, when the unit is not in the state from, or when the unit
// failed with CodeSourceHistoryChanged and the move is to UnitRunning.
type UnitStore interface {
	// PutUnits replaces the import units of a planned import.
	PutUnits(ctx context.Context, fence namespace.Fence, id string, units []ImportUnit, e audit.Entry) error
	// Units returns the import units of an import.
	Units(ctx context.Context, ns namespace.Name, id string) ([]ImportUnit, error)
	// Unit returns one import unit.
	Unit(ctx context.Context, ns namespace.Name, id, unitID string) (ImportUnit, error)
	// TransitionUnit moves an import unit from one state to another, with
	// the error code of a failed or skipped unit.
	TransitionUnit(ctx context.Context, fence namespace.Fence, key UnitKey, m UnitMove, e audit.Entry) error
	// SetCounts sets the count of source versions of an import unit, after
	// the writer check finds new versions, and its count of committed
	// versions.
	SetCounts(ctx context.Context, fence namespace.Fence, key UnitKey, c Counts, e audit.Entry) error
	// Ledger returns the ledger rows of an import unit, oldest first.
	Ledger(ctx context.Context, ns namespace.Name, id, unitID string) ([]LedgerRow, error)
}

// CheckStatus is the status of a step, a unit or a whole move check.
type CheckStatus string

// The statuses of a move check, from the worst to the best. A skipped step
// does not count.
const (
	CheckFailed  CheckStatus = "failed"
	CheckPending CheckStatus = "pending"
	CheckWarning CheckStatus = "warning"
	CheckPassed  CheckStatus = "passed"
	CheckSkipped CheckStatus = "skipped"
)

// CheckStep is one step of the move check of a unit. Expected and Actual
// never hold a secret value.
type CheckStep struct {
	Name     string
	Status   CheckStatus
	Expected string
	Actual   string
	Message  string
}

// Severity is the severity of a carry-over finding.
type Severity string

// The severities of a carry-over finding.
const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding names a part of a unit that the move did not carry over, with
// the command or the route that helps.
type Finding struct {
	Code     string
	Severity Severity
	Message  string
	Help     string
}

// UnitReport is the move check of one unit.
type UnitReport struct {
	Unit     history.Unit
	Source   SourceUnit
	Status   CheckStatus
	Steps    []CheckStep
	Findings []Finding
}

// Report is one move check: of one unit, or of a namespace.
type Report struct {
	ID        string
	Namespace namespace.Name
	ImportID  string
	Status    CheckStatus
	At        time.Time
	Units     []UnitReport
	Missing   []SourceUnit
	Extra     []history.Unit
}
