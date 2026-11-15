// Package scopeaudit records the refusals of the scoped blob views as audit
// entries. A package that reads blobs through a blob.Scoped view gives its
// Recorder to the view, with the Family of its blobs, so every refusal has one
// shape: one entry of the kind security in the scope @server.
package scopeaudit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a recorder built without its audit store.
var ErrMissingDependency = errors.New("missing dependency")

// ErrInvalidFamily reports a Family with a name that is not valid.
var ErrInvalidFamily = errors.New("family of an out-of-scope recorder is not valid")

// ErrInvalidRecord reports a record with an operation or a place that the
// entry cannot hold.
var ErrInvalidRecord = errors.New("invalid out-of-scope record")

// Recorder records that a call tried to use a blob key outside the scope of
// the call. A recorder never holds the key of the call: the key can name
// another namespace, and it can hold text that an attacker chose.
type Recorder interface {
	// RecordOutOfScope records one refusal in the namespace ns, for the
	// operation op ("put", "get", "delete" or "list"). Where says how far the
	// key was out of scope. It returns an error when it cannot record the
	// refusal.
	RecordOutOfScope(ctx context.Context, ns namespace.ID, op string, where Where) error
}

// IntegrityRecorder records that a call refused a blob because its bytes do
// not match the size and the hash that the record of the blob names. A store
// that replaced the object, or an attacker with write access to the store, can
// cause this. A recorder never holds the key or the bytes of the blob.
type IntegrityRecorder interface {
	// RecordMismatch records one refusal in the namespace ns, for the
	// operation op. Only "get" is an operation of a mismatch. It returns an
	// error when it cannot record the refusal.
	RecordMismatch(ctx context.Context, ns namespace.ID, op string) error
}

// Where says where a refused key was: outside the namespace of the call,
// inside the namespace and outside the directory of the blobs of the call, or
// inside that directory and outside the directory of the unit of the call.
type Where string

// The places of a refused key.
const (
	// OutsideNamespace is a key that is not inside the namespace of the call.
	OutsideNamespace Where = "namespace"
	// OutsideDirectory is a key inside the namespace of the call and outside
	// the directory of the blobs of the call, for example the key of a
	// checkpoint in a call for an item key.
	OutsideDirectory Where = "directory"
	// OutsideUnitDirectory is a key inside the directory of the blobs of the
	// call and outside the directory of the unit of the call, for example the
	// key of another stack in a call for one stack.
	OutsideUnitDirectory Where = "unit-directory"
)

// Family names the blobs of one recorder in the reason of its entries.
type Family struct {
	// Blobs names the blobs, for example "item key blob".
	Blobs string
	// Dir is the directory of the blobs inside the namespace, for example
	// "item-keys".
	Dir string
}

// maxFamilyNameLen bounds each name of a Family.
const maxFamilyNameLen = 32

// validFamilyName reports whether s is a name of a Family: lowercase ASCII
// letters, spaces and hyphens, at most maxFamilyNameLen bytes, with a letter
// first. The name goes into the reason of an audit entry, so the allowlist
// keeps line breaks and other control bytes out.
func validFamilyName(s string) bool {
	if s == "" || len(s) > maxFamilyNameLen || s[0] < 'a' || s[0] > 'z' {
		return false
	}

	for _, c := range []byte(s) {
		if (c < 'a' || c > 'z') && c != ' ' && c != '-' {
			return false
		}
	}

	return true
}

// PlaceOf says where a refused key is. A key that a valid key of the view
// would be, in the namespace of the view, is outside the directory of the
// blobs of the call. Every other key is outside the namespace: another
// namespace, a parent segment, an empty key or a key that is not a valid key
// at all.
func PlaceOf(v *blob.Scoped, key blob.Key) Where {
	if _, err := blob.ParseKey(key.String()); err == nil && strings.HasPrefix(key.String(), v.Prefix()) {
		return OutsideDirectory
	}

	return OutsideNamespace
}

// Actions and fields of the audit entry of a refusal.
const (
	// ActionOutOfScope is the action of the audit entry of a refusal.
	ActionOutOfScope = "blob:out-of-scope"
	// ActionMismatch is the action of the audit entry of a blob that does not
	// match its record.
	ActionMismatch = "blob:mismatch"
	// subject is the subject of that entry: the server itself.
	subject = "server"
)

// Audit is a Recorder that writes one audit entry of the kind security in the
// scope @server. The channel of the entry is the channel of the request, which
// the context of the call carries (audit.WithChannel). A call without one is
// an API call.
type Audit struct {
	entries audit.ServerStore
	family  Family
	now     func() time.Time
}

var (
	_ Recorder          = (*Audit)(nil)
	_ IntegrityRecorder = (*Audit)(nil)
)

// New returns an Audit that appends to entries, for the blobs of family. now
// may be nil; it then is time.Now.
func New(entries audit.ServerStore, family Family, now func() time.Time) (*Audit, error) {
	if entries == nil {
		return nil, fmt.Errorf("%w: audit store", ErrMissingDependency)
	}

	if !validFamilyName(family.Blobs) || !validFamilyName(family.Dir) {
		return nil, fmt.Errorf("%w: %q, %q", ErrInvalidFamily, family.Blobs, family.Dir)
	}

	if now == nil {
		now = time.Now
	}

	return &Audit{entries: entries, family: family, now: now}, nil
}

// RecordOutOfScope appends the entry. The object is the ID of the namespace of
// the call, and the reason names op. The entry never holds the refused key.
func (a *Audit) RecordOutOfScope(ctx context.Context, ns namespace.ID, op string, where Where) error {
	switch op {
	case "put", "get", "delete", "list":
	default:
		return fmt.Errorf("%w: operation %q", ErrInvalidRecord, op)
	}

	var place string

	switch where {
	case OutsideNamespace:
		place = "outside the namespace"
	case OutsideDirectory:
		place = "outside the " + a.family.Dir + " directory of the namespace"
	case OutsideUnitDirectory:
		place = "outside the unit directory"
	default:
		return fmt.Errorf("%w: place %q", ErrInvalidRecord, where)
	}

	if _, err := namespace.ParseID(ns.String()); err != nil {
		return fmt.Errorf("record out-of-scope refusal: %w", err)
	}

	return a.append(ctx, ns, ActionOutOfScope, a.family.Blobs+" "+op+" refused: key "+place)
}

// RecordMismatch appends the entry of a blob that does not match its record.
// The object is the ID of the namespace of the call. The entry never holds the
// key or the bytes of the blob.
func (a *Audit) RecordMismatch(ctx context.Context, ns namespace.ID, op string) error {
	// The allowlist keeps the reason to a fixed text: a read is the only
	// operation that compares the bytes with the record.
	if op != "get" {
		return fmt.Errorf("%w: operation %q", ErrInvalidRecord, op)
	}

	if _, err := namespace.ParseID(ns.String()); err != nil {
		return fmt.Errorf("record blob mismatch: %w", err)
	}

	return a.append(ctx, ns, ActionMismatch,
		a.family.Blobs+" "+op+" refused: the size or the MD5 of the blob does not match its record")
}

// append writes one entry of the kind security with the outcome denied.
func (a *Audit) append(ctx context.Context, ns namespace.ID, action, reason string) error {
	entry := audit.Entry{
		At:            a.now().UTC(),
		Namespace:     audit.ServerScope,
		Subject:       subject,
		IDPSubject:    "",
		TokenID:       "",
		Action:        action,
		Object:        ns.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelFrom(ctx),
		Kind:          audit.KindSecurity,
		Outcome:       audit.OutcomeDenied,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        reason,
	}

	if _, err := a.entries.AppendServer(ctx, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}
