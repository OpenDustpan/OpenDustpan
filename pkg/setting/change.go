package setting

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
)

// textNotSet is the text of an audit change for a value that is not set, and
// for a secret, whose value no entry holds.
const textNotSet = "not set"

// Stored is what a store keeps for one scope.
type Stored struct {
	// Values holds the database value of each key that has one.
	Values map[string]string
	// Revision is the revision of the resource. A scope that never changed
	// has revision 0.
	Revision int64
	// Digest is the digest of the static layer at the last sync. It is empty
	// for a namespace.
	Digest string
}

// Tx is what a store gives inside one of its transactions. Change runs the
// rules of a change on it, so each adapter keeps the SQL and nothing more,
// and the rules have one source.
type Tx interface {
	// Load returns what the store keeps for scope. A store that has row locks
	// takes the lock of the scope here, so two changes of one scope wait for
	// each other. It returns namespace.ErrNotFound for a namespace that does
	// not exist.
	Load(ctx context.Context, scope Scope) (Stored, error)
	// Put writes the database value of key at the time atMs in Unix
	// milliseconds.
	Put(ctx context.Context, scope Scope, key, value string, atMs int64) error
	// Remove deletes the database value of key.
	Remove(ctx context.Context, scope Scope, key string) error
	// SetRevision writes the revision of the scope.
	SetRevision(ctx context.Context, scope Scope, revision int64) error
	// SetDigest writes the digest of the static layer and the revision of
	// the installation.
	SetDigest(ctx context.Context, digest string, revision int64) error
	// Append appends the audit entry e. It returns audit.ErrInvalidEntry when
	// e fails validation.
	Append(ctx context.Context, e audit.Entry) error
}

// ChangeInput is the request of Change.
type ChangeInput struct {
	Registry *Registry
	// Static is the static layer of the installation. It is ignored for a
	// namespace scope.
	Static Static
	Patch  Patch
	// Entry is the audit entry of the change. Change adds the changed keys.
	Entry audit.Entry
	// IfMatch is the revision that the caller read.
	IfMatch int64
	// Fresh says that the caller proved a fresh authentication, or that none
	// is needed. Without it, a change that reduces security is refused.
	Fresh bool
	// AtMs is the time of the change in Unix milliseconds.
	AtMs int64
}

// Result is the answer of a change.
type Result struct {
	Resource Resource
	// Changed holds the keys whose database value changed, in key order.
	Changed []string
}

// lookupStatic returns the value that the static layer gives for key in
// scope. A namespace has no static layer, so it gives none.
func lookupStatic(scope Scope, static Static, key string) *StaticValue {
	if !scope.IsInstallation() || static == nil {
		return nil
	}

	value, ok := static.Lookup(key)
	if !ok {
		return nil
	}

	return &value
}

// effectiveOf returns the setting d of scope for the database values in
// values and the static layer.
func effectiveOf(d Declaration, scope Scope, static Static, values map[string]string) Item {
	var stored *string

	// A stored value that the declaration refuses, for example after a
	// downgrade or a restore from another version, is not read: the setting
	// has its default, as if no value were stored.
	if value, ok := values[d.Key]; ok && d.CheckValue(value) == nil {
		stored = &value
	}

	return Effective(d, stored, lookupStatic(scope, static, d.Key))
}

// Read builds the resource of scope from what a store holds.
func Read(reg *Registry, scope Scope, static Static, stored Stored) Resource {
	decls := reg.Declarations(scope.Level())
	items := make([]Item, 0, len(decls))

	for _, d := range decls {
		items = append(items, effectiveOf(d, scope, static, stored.Values))
	}

	return Resource{Items: items, Revision: stored.Revision}
}

// Change applies the patch of in to scope through tx, as the Update of a
// Store must. It reads the scope, checks the revision, checks every edit
// against the declarations and the static layer, and, when a database value
// changes, writes the values, the new revision and the audit entry. A patch
// that changes no database value writes nothing. The caller runs it in one
// transaction, so a failed call changes nothing.
//
// The checks run in this order: the revision (ErrPreconditionFailed), then
// each edit (ErrUnknownKey, ErrInvalidValue, ErrStaticOnly, ErrLocked), then
// the fresh authentication (ErrFreshStepRequired).
func Change(ctx context.Context, tx Tx, scope Scope, in ChangeInput) (Result, error) {
	none := Result{Resource: Resource{Items: nil, Revision: 0}, Changed: nil}

	stored, err := tx.Load(ctx, scope)
	if err != nil {
		return none, err
	}

	if in.IfMatch != stored.Revision {
		return none, ErrPreconditionFailed
	}

	next, changed, err := planEdits(in, scope, stored.Values)
	if err != nil {
		return none, err
	}

	if len(changed) == 0 {
		return Result{Resource: Read(in.Registry, scope, in.Static, stored), Changed: []string{}}, nil
	}

	entry, reduces := changeEntry(in, scope, entryValues{before: stored.Values, after: next}, changed)
	if reduces && !in.Fresh {
		return none, ErrFreshStepRequired
	}

	write := revisionWrite{scope: scope, atMs: in.AtMs, revision: stored.Revision + 1}
	if err := writeChange(ctx, tx, write, changed); err != nil {
		return none, err
	}

	if err := tx.Append(ctx, entry); err != nil {
		return none, fmt.Errorf("append audit entry: %w", err)
	}

	after := Stored{Values: next, Revision: write.revision, Digest: stored.Digest}
	keys := make([]string, 0, len(changed))

	for _, edit := range changed {
		keys = append(keys, edit.Key)
	}

	return Result{Resource: Read(in.Registry, scope, in.Static, after), Changed: keys}, nil
}

// planEdits checks the edits of in against the declarations and the static
// layer. It returns the database values after the edits and the edits that
// change a database value. The edits apply in key order, so the result does
// not depend on the order of the patch. A key twice in a patch is refused:
// the second edit would silently win.
func planEdits(in ChangeInput, scope Scope, stored map[string]string) (map[string]string, []Edit, error) {
	edits := slices.SortedFunc(slices.Values(in.Patch), func(a, b Edit) int { return strings.Compare(a.Key, b.Key) })

	unique := slices.CompactFunc(slices.Clone(edits), func(a, b Edit) bool { return a.Key == b.Key })
	if len(unique) != len(edits) {
		return nil, nil, fmt.Errorf("%w: a key is twice in the patch", ErrInvalidPatch)
	}

	next := maps.Clone(stored)
	if next == nil {
		next = map[string]string{}
	}

	var changed []Edit

	for _, edit := range edits {
		decl, ok := in.Registry.Lookup(scope.Level(), edit.Key)
		if !ok {
			return nil, nil, fmt.Errorf("%w: %s", ErrUnknownKey, shown(edit.Key))
		}

		moved, err := applyEdit(decl, edit, lookupStatic(scope, in.Static, edit.Key), next)
		if err != nil {
			return nil, nil, err
		}

		if moved {
			changed = append(changed, edit)
		}
	}

	return next, changed, nil
}

// applyEdit checks edit against decl and the static value, and applies it to
// values. It reports whether the database value changed.
func applyEdit(decl Declaration, edit Edit, static *StaticValue, values map[string]string) (bool, error) {
	if !decl.Database {
		err := keyError(ErrStaticOnly, edit.Key, "")
		if static != nil {
			err.Source, err.By = static.Source, static.By
		}

		return false, err
	}

	// Defense in depth: ParsePatch checks the value too. This check stays so
	// that a patch built by hand never stores a value the declaration refuses.
	if !edit.Delete {
		if err := decl.CheckValue(edit.Value); err != nil {
			return false, err
		}
	}

	current, has := values[edit.Key]

	if edit.Delete {
		delete(values, edit.Key)

		return has, nil
	}

	if static != nil {
		err := keyError(ErrLocked, edit.Key, "")
		err.Source, err.By = static.Source, static.By

		return false, err
	}

	values[edit.Key] = edit.Value

	return !has || current != edit.Value, nil
}

// entryValues holds the database values before and after a change.
type entryValues struct {
	before, after map[string]string
}

// changeEntry returns the audit entry of the change and whether one edit
// reduces security. For each changed key it records the effective value
// before and after, and the layer and the time of effect of the new value.
func changeEntry(in ChangeInput, scope Scope, values entryValues, changed []Edit) (audit.Entry, bool) {
	entry := in.Entry
	entry.Changes = slices.Clone(entry.Changes)
	reduces := false

	for _, edit := range changed {
		decl, _ := in.Registry.Lookup(scope.Level(), edit.Key)

		old := effectiveOf(decl, scope, in.Static, values.before)
		next := effectiveOf(decl, scope, in.Static, values.after)

		if decl.ReducesSecurity != nil && decl.ReducesSecurity(old.Value, next.Value) {
			reduces = true
		}

		oldText, newText, layer := old.Value, next.Value, next.Source
		if decl.Secret {
			oldText, newText = textNotSet, textNotSet
		}

		// A delete of a database value that the static layer hides changes no
		// effective value. The entry records the change that happened: the
		// database value goes away, in the database layer.
		if old.Shadowed {
			oldText, newText, layer = old.DatabaseValue, textNotSet, SourceDatabase
		}

		entry.Changes = append(entry.Changes, audit.Change{
			Key:         edit.Key,
			Old:         &oldText,
			New:         newText,
			Layer:       string(layer),
			TakesEffect: takesEffect(decl.Apply),
		})
	}

	return entry, reduces
}

// takesEffect is the text of audit.Change.TakesEffect for apply: empty when
// the change applies at once.
func takesEffect(apply Apply) string {
	if apply == ApplyRestart {
		return "next start"
	}

	return ""
}

// revisionWrite names the scope of a write, its time and its new revision.
type revisionWrite struct {
	scope    Scope
	atMs     int64
	revision int64
}

// writeChange writes the edits, then the revision.
func writeChange(ctx context.Context, tx Tx, write revisionWrite, changed []Edit) error {
	for _, edit := range changed {
		if edit.Delete {
			if err := tx.Remove(ctx, write.scope, edit.Key); err != nil {
				return fmt.Errorf("delete setting %s: %w", edit.Key, err)
			}

			continue
		}

		if err := tx.Put(ctx, write.scope, edit.Key, edit.Value, write.atMs); err != nil {
			return fmt.Errorf("write setting %s: %w", edit.Key, err)
		}
	}

	if err := tx.SetRevision(ctx, write.scope, write.revision); err != nil {
		return fmt.Errorf("write revision: %w", err)
	}

	return nil
}

// Sync records that the static layer changed. When digest differs from the
// stored one, it stores digest and the next revision. It reports the
// revision and whether it changed. The caller runs it in one transaction.
func Sync(ctx context.Context, tx Tx, digest string) (int64, bool, error) {
	stored, err := tx.Load(ctx, InstallationScope())
	if err != nil {
		return 0, false, err
	}

	if stored.Digest == digest {
		return stored.Revision, false, nil
	}

	if strings.TrimSpace(digest) == "" {
		return stored.Revision, false, fmt.Errorf("%w: empty digest", ErrScope)
	}

	next := stored.Revision + 1
	// A first start has no stored digest and no change of an effective value:
	// the resource is new, so it keeps revision 0 and records the digest.
	if stored.Digest == "" {
		next = stored.Revision
	}

	if err := tx.SetDigest(ctx, digest, next); err != nil {
		return 0, false, fmt.Errorf("write digest: %w", err)
	}

	return next, next != stored.Revision, nil
}
