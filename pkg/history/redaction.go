package history

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// MaxReasonLength is the longest redaction reason, in characters.
const MaxReasonLength = 1000

// RetentionReason is the reason of a redaction at the end of a retention
// period.
const RetentionReason = "retention period ended"

// RedactionID is the stable, opaque ID of one redaction, with the prefix
// "rdx-".
type RedactionID string

// redactionPrefix starts every redaction ID.
const redactionPrefix = "rdx-"

// NewRedactionID returns a new random redaction ID.
func NewRedactionID() RedactionID {
	return RedactionID(ident.NewID(redactionPrefix))
}

// Cause says why a redaction exists.
type Cause string

// The causes of a redaction.
const (
	// CauseRequest is a redaction that a principal asked for.
	CauseRequest Cause = "request"
	// CauseRetention is the end of the retention period of a plan or a job
	// log. Its actor is ServerSubject and its reason is RetentionReason.
	CauseRetention Cause = "retention"
	// CauseRestore is a redaction that a restore found in the ledger of a
	// backup target. It has no reason, because the ledger holds none.
	CauseRestore Cause = "restore"
)

// Valid reports whether c is one of the causes.
func (c Cause) Valid() bool {
	switch c {
	case CauseRequest, CauseRetention, CauseRestore:
		return true
	default:
		return false
	}
}

// LiveTarget is the shred target of the live blob store. Every redaction
// has it, as its first target.
const LiveTarget = "@live"

// ShredState is the state of one shred target of a redaction.
type ShredState string

// The states of a shred target.
const (
	// ShredPending is a target where the key blob or a content blob may
	// still exist.
	ShredPending ShredState = "pending"
	// ShredDone is a target that holds the ledger object of the redaction
	// and no longer holds the key blob or a content blob of the item.
	ShredDone ShredState = "done"
)

// ShredTarget is one place where a redaction deletes the key blob and the
// content blobs of its item: LiveTarget, or a backup target as the backups
// table names it, for example "dir:/backups" or "s3://bucket/prefix".
// LastError is text for an operator. It never holds a credential or driver
// text.
type ShredTarget struct {
	DoneAt    time.Time
	LastTryAt time.Time
	Target    string
	State     ShredState
	LastError string
	Attempts  int
}

// Redaction is the record of one redaction. It keeps who redacted which
// item, when and why, the content key that it shredded, the content blobs
// that it deletes, and the state of each shred target. It never holds
// content.
type Redaction struct {
	At         time.Time
	Item       ItemRef
	ID         RedactionID
	By         Actor
	Reason     string
	Cause      Cause
	ContentKey secret.ContentKeyID
	Blobs      []blob.Key
	Targets    []ShredTarget
}

// Pending reports whether a target of r is still pending.
func (r Redaction) Pending() bool {
	for _, t := range r.Targets {
		if t.State != ShredDone {
			return true
		}
	}

	return false
}

// Validate reports whether r is a record that a store can append. It
// returns ErrInvalidReason for a bad reason and ErrInvalidRedaction or
// ErrInvalidItem for other fields. It does not check the targets: the
// store makes one pending row for each target name.
func (r Redaction) Validate() error {
	if err := r.Item.Validate(); err != nil {
		return err
	}

	if !strings.HasPrefix(string(r.ID), redactionPrefix) || !ident.Valid(string(r.ID), maxIDLength) {
		return fmt.Errorf("%w: ID %q", ErrInvalidRedaction, r.ID)
	}

	if !r.Cause.Valid() {
		return fmt.Errorf("%w: cause %q", ErrInvalidRedaction, r.Cause)
	}

	if r.By.Subject == "" || r.At.IsZero() {
		return fmt.Errorf("%w: actor and time are required", ErrInvalidRedaction)
	}

	if _, err := secret.ParseContentKeyID(string(r.ContentKey)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRedaction, err)
	}

	if r.Cause == CauseRestore && r.Reason == "" {
		return nil
	}

	return ValidateReason(r.Reason)
}

// ValidateReason reports whether reason is 1 to MaxReasonLength characters
// of plain text with no control characters. It returns ErrInvalidReason
// otherwise.
func ValidateReason(reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: a reason is required", ErrInvalidReason)
	}

	if !utf8.ValidString(reason) {
		return fmt.Errorf("%w: the reason is not UTF-8", ErrInvalidReason)
	}

	if utf8.RuneCountInString(reason) > MaxReasonLength {
		return fmt.Errorf("%w: the reason is longer than %d characters", ErrInvalidReason, MaxReasonLength)
	}

	if strings.ContainsFunc(reason, unicode.IsControl) {
		return fmt.Errorf("%w: the reason holds a control character", ErrInvalidReason)
	}

	return nil
}

// ShredFailure is one failed try of the shred operation on one target of
// one redaction. Text is for an operator. It never holds a credential or
// driver text.
type ShredFailure struct {
	At     time.Time
	ID     RedactionID
	Target string
	Text   string
}

// RedactionCursor is the position after which a page of redactions starts:
// the order key of the last record of the page before.
type RedactionCursor struct {
	At time.Time
	ID RedactionID
}

// RedactionLog is the persistence port of redaction records. A record is
// append-only: only the state of its shred targets changes.
type RedactionLog interface {
	// AppendRedaction stores r with one pending shred row for LiveTarget
	// and one for each name in targets, in this order, and returns the
	// stored record. A redaction writes it in the transaction that marks
	// the item. It returns ErrRedacted with the stored record when the item
	// already has a redaction, the error of Redaction.Validate for an
	// invalid record, and namespace.ErrFenced when the fence is stale.
	AppendRedaction(ctx context.Context, r Redaction, targets []string, fence namespace.Fence) (Redaction, error)
	// GetRedaction returns the record id of the namespace ns, or
	// ErrRedactionNotFound.
	GetRedaction(ctx context.Context, ns namespace.Name, id RedactionID) (Redaction, error)
	// RedactionOf returns the record of item, or ErrRedactionNotFound when
	// the item has none.
	RedactionOf(ctx context.Context, item ItemRef) (Redaction, error)
	// ListRedactions returns at most limit records of unit, newest first by
	// time, then by ID. With a cursor, it returns only the records after the
	// cursor in that order.
	ListRedactions(ctx context.Context, unit Unit, after *RedactionCursor, limit int) ([]Redaction, error)
	// ListPendingShreds returns at most limit records of every namespace
	// that have a pending target, ordered by ID, after the ID after (empty
	// for the first page). The shred operation reads it.
	ListPendingShreds(ctx context.Context, after RedactionID, limit int) ([]Redaction, error)
	// FinishShred marks the target of the record id done at the time at. A
	// second call keeps the first time and answers nil. It returns
	// ErrRedactionNotFound, ErrTargetNotFound, and namespace.ErrFenced when
	// the fence is stale.
	FinishShred(ctx context.Context, id RedactionID, target string, at time.Time, fence namespace.Fence) error
	// NoteShredFailure records the failed try f: it counts the try on the
	// target of the record and keeps the time and the text. The target stays
	// pending. It returns ErrRedactionNotFound, ErrTargetNotFound, and
	// namespace.ErrFenced when the fence is stale. A call on a done target
	// changes nothing and answers nil.
	NoteShredFailure(ctx context.Context, f ShredFailure, fence namespace.Fence) error
}

// Redactor redacts one kind of history item. Each tool implements it for its
// own state items with its own tables, and the plan and job log stores
// implement it for their items, so shared code redacts an item by its
// reference only.
type Redactor interface {
	// Redact checks the item of r and, in one fenced transaction, marks the
	// item redacted with r.ID, marks its content key shredded, appends r to
	// the redaction log with a pending shred row for LiveTarget and for each
	// name in targets, and writes the audit entry e. The tool stamps the
	// time of e. A failed audit write makes the redaction fail. It returns
	// the stored record, and:
	//   - ErrItemNotFound when the item does not exist,
	//   - ErrLatest for the latest state item of the unit,
	//   - ErrInProgress for an item that is not finished,
	//   - ErrBlamePending for a state item before blame compared it and the
	//     next state item,
	//   - ErrRedacted with the earlier record for an item that is redacted,
	//   - namespace.ErrFenced when the fence is stale.
	Redact(ctx context.Context, r Redaction, targets []string, e audit.Entry, fence namespace.Fence) (Redaction, error)
}

// LedgerObject returns the blob key of the ledger object of the redaction
// id in a backup target: redactions/<id>.json. The shred operation writes it
// one time in each target before it deletes a blob there. id must be valid.
func LedgerObject(id RedactionID) blob.Key {
	return blob.Key("redactions/" + string(id) + ".json")
}

// LedgerRecord is the content of a ledger object. Restore and the backup
// check read it, so they treat a blob that it names as redacted, not
// missing. It holds no reason and no name.
type LedgerRecord struct {
	ID         RedactionID         `json:"id"`
	Namespace  namespace.Name      `json:"namespace"`
	Tool       string              `json:"tool"`
	UnitID     string              `json:"unitID"`
	Kind       secret.ContentKind  `json:"kind"`
	Item       string              `json:"item"`
	ContentKey secret.ContentKeyID `json:"contentKey"`
	Blobs      []blob.Key          `json:"blobs"`
	AtMs       int64               `json:"atMs"`
}

// Ledger returns the ledger record of r.
func (r Redaction) Ledger() LedgerRecord {
	return LedgerRecord{
		ID:         r.ID,
		Namespace:  r.Item.Unit.Namespace,
		Tool:       r.Item.Unit.Tool,
		UnitID:     r.Item.Unit.ID,
		Kind:       r.Item.Kind,
		Item:       r.Item.Item,
		ContentKey: r.ContentKey,
		Blobs:      r.Blobs,
		AtMs:       r.At.UnixMilli(),
	}
}
