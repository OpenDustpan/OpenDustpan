// Package sinkstore holds the rules of the notification sink tables that the
// SQLite and PostgreSQL adapters share: the check of a stored row, and the
// plan of a write of the secret items of a sink. A row is untrusted when it
// is read, because a backup, a restore or another version of the server can
// have written it.
package sinkstore

import (
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ErrCorrupt reports a stored row that does not pass the checks of a sink.
// The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored sink is not valid")

// Row is a sink row as the database holds it. Namespace is the name of the
// namespace.
type Row struct {
	ID        string
	Namespace string
	Kind      string
	Name      string
	Platform  string
	Target    string
	CreatedBy string
	Version   int64
}

// Sink builds the sink of a stored row with the secret refs refs, in their
// order, and checks it again with notify.Sink.Validate. It returns
// ErrCorrupt for a row that fails.
func Sink(r Row, refs []string) (notify.Sink, error) {
	s := notify.Sink{
		ID:         r.ID,
		Scope:      namespace.Name(r.Namespace),
		Kind:       notify.SinkKind(r.Kind),
		Name:       r.Name,
		Platform:   r.Platform,
		Target:     r.Target,
		SecretRefs: slices.Clone(refs),
		CreatedBy:  r.CreatedBy,
		Version:    r.Version,
	}

	if s.Version < 1 {
		return notify.Sink{}, fmt.Errorf("%w: version %d", ErrCorrupt, s.Version)
	}

	if err := s.Validate(); err != nil {
		return notify.Sink{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return s, nil
}

// Change is what a write of a sink does to its secret items.
type Change struct {
	// Claim lists the refs of the new secret items, in order. The store
	// records each one as held and refuses the write when the namespace held
	// it before.
	Claim []string
	// Drop lists the stored refs that the sink no longer names.
	Drop []string
}

// Plan decides what the write of s does to its secret items. stored holds the
// refs of the secret items that the store keeps for the sink now, and fresh
// the refs of the new items of the write. It returns notify.ErrInvalid for a
// ref of s that is neither new nor stored for the sink. A new item must have
// a ref that s names: notify.Sink.ValidateSealed checks this before.
func Plan(s notify.Sink, stored, fresh []string) (Change, error) {
	var c Change

	for _, ref := range s.SecretRefs {
		switch {
		case slices.Contains(fresh, ref):
			c.Claim = append(c.Claim, ref)
		case !slices.Contains(stored, ref):
			return Change{}, fmt.Errorf(
				"%w: sink %s names the secret %s, which has no value",
				notify.ErrInvalid,
				s.ID,
				ref,
			)
		default:
			// The ref is stored for the sink and the write adds no new value.
		}
	}

	for _, ref := range stored {
		if !slices.Contains(s.SecretRefs, ref) {
			c.Drop = append(c.Drop, ref)
		}
	}

	return c, nil
}

// Secret is a secret item row as the database holds it, without its value.
type Secret struct {
	Namespace string
	Ref       string
	SinkID    string
}

// CheckStaged checks the sink tables of a staged backup. Every sink row must
// pass Sink with the refs of its secret items in the order of rows. Every
// secret item must belong to a sink of its own namespace, and its ref must be
// in held, the set of "namespace/ref" keys that the namespace held. A backup
// that breaks a rule is corrupt: a restore never brings in a sink that the
// server would not have stored, and never frees a ref for a second value. It
// returns ErrCorrupt.
//
// Defense in depth: the store checks a row again when it reads it. This check
// stays so that a bad row fails the restore and does not wait for a reader.
func CheckStaged(sinks []Row, secrets []Secret, held map[string]bool) error {
	byID := make(map[string]Row, len(sinks))
	refs := make(map[string][]string, len(sinks))

	for _, r := range sinks {
		byID[r.ID] = r
	}

	for _, sec := range secrets {
		owner, ok := byID[sec.SinkID]
		if !ok || owner.Namespace != sec.Namespace {
			return fmt.Errorf("%w: secret %q names no sink of its namespace", ErrCorrupt, sec.Ref)
		}

		if !held[sec.Namespace+"/"+sec.Ref] {
			return fmt.Errorf("%w: secret %q is not in the refs that the namespace held", ErrCorrupt, sec.Ref)
		}

		refs[sec.SinkID] = append(refs[sec.SinkID], sec.Ref)
	}

	for _, r := range sinks {
		if _, err := Sink(r, refs[r.ID]); err != nil {
			return fmt.Errorf("staged sink %q: %w", r.ID, err)
		}
	}

	return nil
}

// CheckWrite decides a write of the sink s against the stored row prev, which
// is nil for a sink that the store does not hold. It returns the sink as the
// store keeps it after the write: one version above the stored version, and
// with the creator of the stored row. It returns notify.ErrNotFound when the
// stored sink belongs to another scope, notify.ErrConflict when s.Version is
// not the stored version (0 for a new sink), and notify.ErrInvalid for a
// change of the kind or the platform.
func CheckWrite(prev *Row, s notify.Sink) (notify.Sink, error) {
	var version int64

	stored := s

	if prev != nil {
		if prev.Namespace != s.Scope.String() {
			return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrNotFound)
		}

		version = prev.Version
		stored.CreatedBy = prev.CreatedBy
	}

	if s.Version != version {
		return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
	}

	if prev != nil && (prev.Kind != string(s.Kind) || prev.Platform != s.Platform) {
		return notify.Sink{}, fmt.Errorf(
			"put sink %s: %w: a new kind or platform is a new sink",
			s.ID,
			notify.ErrInvalid,
		)
	}

	stored.Version = version + 1

	return stored, nil
}
