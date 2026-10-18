package history

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// TombstoneID is the stable, opaque ID of the tombstone of one purged
// unit, with the prefix "tmb-".
type TombstoneID string

// tombstonePrefix starts every tombstone ID.
const tombstonePrefix = "tmb-"

// NewTombstoneID returns a new random tombstone ID.
func NewTombstoneID() TombstoneID {
	return TombstoneID(ident.NewID(tombstonePrefix))
}

// Tombstone is the durable shred record of the final delete of one unit.
// The purge writes it in its transaction, in a control table, so it
// survives the restore swap. It names every item key of the unit and the
// blob key prefixes that hold the content of the unit, and it has one shred
// target for the live blob store and one for each backup target. The shred
// operation deletes the key blobs and every blob under the prefixes on each
// target, after it writes the ledger object of the tombstone there. Its
// cause is always CausePurge. AuditHash is the hash of the audit entry of
// the purge. A tombstone never holds content, a reason or a name.
type Tombstone struct {
	At        time.Time
	Unit      Unit
	ID        TombstoneID
	By        Actor
	AuditHash string
	ItemKeys  []secret.ItemKeyID
	Prefixes  []string
	Targets   []ShredTarget
}

// Pending reports whether a target of t is still pending.
func (t Tombstone) Pending() bool {
	for _, target := range t.Targets {
		if target.State != ShredDone {
			return true
		}
	}

	return false
}

// Validate reports whether t is a record that a store can append. It
// returns ErrInvalidItem for a bad unit and ErrInvalidTombstone for other
// fields. Each prefix ends with "/" and is not a prefix of the ledger or
// of the key blobs. It does not check the targets: the store makes one
// pending row for each target name.
func (t Tombstone) Validate() error {
	if err := t.Unit.Validate(); err != nil {
		return err
	}

	if !strings.HasPrefix(string(t.ID), tombstonePrefix) || !ident.Valid(string(t.ID), maxIDLength) {
		return fmt.Errorf("%w: ID %q", ErrInvalidTombstone, t.ID)
	}

	if t.By.Subject == "" || t.At.IsZero() {
		return fmt.Errorf("%w: actor and time are required", ErrInvalidTombstone)
	}

	for _, id := range t.ItemKeys {
		if _, err := secret.ParseItemKeyID(string(id)); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidTombstone, err)
		}
	}

	if len(t.Prefixes) == 0 {
		return fmt.Errorf("%w: a blob key prefix is required", ErrInvalidTombstone)
	}

	for _, p := range t.Prefixes {
		if !validPrefix(p) {
			return fmt.Errorf("%w: prefix %q", ErrInvalidTombstone, p)
		}
	}

	return nil
}

// validPrefix reports whether p is a blob key prefix of unit content: it
// ends with "/", and it does not hold the ledger or the key blobs.
func validPrefix(p string) bool {
	if len(p) < 2 || !strings.HasSuffix(p, "/") || strings.HasPrefix(p, "/") {
		return false
	}

	for _, reserved := range []string{"item-keys/", "redactions/", "tombstones/"} {
		if strings.HasPrefix(p, reserved) {
			return false
		}
	}

	return true
}

// TombstoneFailure is one failed try of the shred operation on one target
// of one tombstone. Text is for an operator. It never holds a credential or
// driver text.
type TombstoneFailure struct {
	At     time.Time
	ID     TombstoneID
	Target string
	Text   string
}

// TombstoneLog is the persistence port of tombstones. A tombstone is
// append-only: only the state of its shred targets changes. The tombstone
// outlives its unit and can outlive its namespace, so only the append
// carries the fence: the purge writes it in the fenced transaction that
// deletes the unit. The shred operation of the server finishes the targets
// without a fence, as it finishes a blob purge of a unit.
type TombstoneLog interface {
	// AppendTombstone stores t with one pending shred row for LiveTarget
	// and one for each name in targets, in this order, and returns the
	// stored record. It returns ErrPurged with the stored record when the
	// unit already has a tombstone, the error of Tombstone.Validate for an
	// invalid record, and namespace.ErrFenced when the fence is stale.
	AppendTombstone(ctx context.Context, t Tombstone, targets []string, fence namespace.Fence) (Tombstone, error)
	// TombstoneOf returns the tombstone of unit, or ErrTombstoneNotFound.
	TombstoneOf(ctx context.Context, unit Unit) (Tombstone, error)
	// ListTombstones returns at most limit tombstones of every namespace,
	// ordered by ID, after the ID after (empty for the first page). A
	// backup reads it to write the ledger objects, and a restore reads it
	// to apply each purge again.
	ListTombstones(ctx context.Context, after TombstoneID, limit int) ([]Tombstone, error)
	// ListPendingTombstones returns at most limit tombstones that have a
	// pending target, ordered by ID, after the ID after. The shred
	// operation reads it.
	ListPendingTombstones(ctx context.Context, after TombstoneID, limit int) ([]Tombstone, error)
	// FinishTombstone marks the target of the tombstone id done at the time
	// at. A second call keeps the first time and answers nil. It returns
	// ErrTombstoneNotFound and ErrTargetNotFound.
	FinishTombstone(ctx context.Context, id TombstoneID, target string, at time.Time) error
	// NoteTombstoneFailure records the failed try f: it counts the try on
	// the target and keeps the time and the text. The target stays pending.
	// It returns ErrTombstoneNotFound and ErrTargetNotFound. A call on a done
	// target changes nothing and answers nil.
	NoteTombstoneFailure(ctx context.Context, f TombstoneFailure) error
	// RequeueTombstones sets every target of every tombstone that names one
	// of the targets in names to pending again. A restore calls it after the
	// swap with LiveTarget and the source target, so the shred operation
	// deletes a blob that came back. A done target of another name stays
	// done.
	RequeueTombstones(ctx context.Context, names []string) error
}

// TombstoneObject returns the blob key of the ledger object of the
// tombstone id in a backup target: tombstones/<id>.json. The shred
// operation writes it one time in each target before it deletes a blob
// there. id must be valid.
func TombstoneObject(id TombstoneID) blob.Key {
	return blob.Key("tombstones/" + string(id) + ".json")
}

// TombstoneRecord is the content of the ledger object of a tombstone.
// Restore and the backup check read it, so they treat a blob that it names
// as purged, not missing, and a restore deletes the unit from the staged
// data again. It holds the hash of the audit entry of the purge, never a
// name.
type TombstoneRecord struct {
	ID        TombstoneID        `json:"id"`
	Cause     Cause              `json:"cause"`
	Namespace namespace.Name     `json:"namespace"`
	Tool      string             `json:"tool"`
	UnitID    string             `json:"unitID"`
	AuditHash string             `json:"auditHash"`
	ItemKeys  []secret.ItemKeyID `json:"itemKeys"`
	Prefixes  []string           `json:"prefixes"`
	AtMs      int64              `json:"atMs"`
}

// Ledger returns the ledger record of t.
func (t Tombstone) Ledger() TombstoneRecord {
	return TombstoneRecord{
		ID:        t.ID,
		Cause:     CausePurge,
		Namespace: t.Unit.Namespace,
		Tool:      t.Unit.Tool,
		UnitID:    t.Unit.ID,
		AuditHash: t.AuditHash,
		ItemKeys:  t.ItemKeys,
		Prefixes:  t.Prefixes,
		AtMs:      t.At.UnixMilli(),
	}
}

// Gaps is the set of blobs that the ledger of a backup target names: the
// key blob and the content blobs of each redaction, and the key blobs and
// the blob key prefixes of each tombstone. `backup check` and a restore
// treat a blob in the set that the target does not hold as an expected
// gap, not a missing blob, and a restore deletes each purged unit from the
// staged data again. Keys are blob keys as SQL names them, without the
// "blobs/" prefix of the target.
type Gaps struct {
	blobs    map[blob.Key]struct{}
	units    map[Unit]struct{}
	prefixes []string
}

// NewGaps returns the gaps of the ledger records redactions and
// tombstones.
func NewGaps(redactions []LedgerRecord, tombstones []TombstoneRecord) Gaps {
	g := Gaps{blobs: map[blob.Key]struct{}{}, units: map[Unit]struct{}{}, prefixes: nil}

	for i := range redactions {
		r := &redactions[i]
		g.blobs[r.ItemKey.Object()] = struct{}{}

		for _, k := range r.Blobs {
			g.blobs[k] = struct{}{}
		}
	}

	for i := range tombstones {
		t := &tombstones[i]
		g.units[Unit{Namespace: t.Namespace, Tool: t.Tool, ID: t.UnitID}] = struct{}{}

		for _, id := range t.ItemKeys {
			g.blobs[id.Object()] = struct{}{}
		}

		g.prefixes = append(g.prefixes, t.Prefixes...)
	}

	return g
}

// Expected reports whether key is a blob that a redaction or a purge
// deleted, so a target that does not hold it is not missing a blob.
func (g Gaps) Expected(key blob.Key) bool {
	if _, ok := g.blobs[key]; ok {
		return true
	}

	for _, p := range g.prefixes {
		if strings.HasPrefix(string(key), p) {
			return true
		}
	}

	return false
}

// Purged reports whether a tombstone names unit. A restore deletes such a
// unit from the staged data before the swap.
func (g Gaps) Purged(unit Unit) bool {
	_, ok := g.units[unit]

	return ok
}
