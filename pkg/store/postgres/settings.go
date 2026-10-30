package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// SettingStore returns the store of settings. Each write checks the installation
// state, and a write to a namespace checks the fence of the namespace. Each
// write appends its audit entry in its own transaction.
func (s *Store) SettingStore() SettingStore {
	return SettingStore{s: s}
}

// SettingStore is setting.Store over the database of a Store.
type SettingStore struct {
	s *Store
}

var _ setting.Store = SettingStore{s: nil}

// settingTx is the SQL of a change of settings inside a transaction. The
// rules are in setting.Change. entryScope is the audit scope that an entry of
// the transaction must name.
type settingTx struct {
	q          *db.Queries
	entryScope namespace.Name
}

var _ setting.Tx = settingTx{q: nil, entryScope: ""}

// serverScopeID is the scope_id of the settings of the installation.
const serverScopeID = string(audit.ServerScope)

// scopeID returns the scope_id of scope: the fixed ID of the installation, or
// the stable ID of the namespace. It returns namespace.ErrNotFound for a
// namespace that does not exist.
func scopeID(ctx context.Context, q *db.Queries, scope setting.Scope) (string, error) {
	if scope.IsInstallation() {
		return serverScopeID, nil
	}

	id, err := q.GetNamespaceID(ctx, scope.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return "", namespace.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select namespace ID: %w", err)
	}

	return id, nil
}

// loadSettings reads the values and the state of scope.
func loadSettings(ctx context.Context, q *db.Queries, scope setting.Scope) (setting.Stored, error) {
	id, err := scopeID(ctx, q, scope)
	if err != nil {
		return setting.Stored{Values: nil, Revision: 0, Digest: ""}, err
	}

	rows, err := q.ListSettings(ctx, id)
	if err != nil {
		return setting.Stored{Values: nil, Revision: 0, Digest: ""}, fmt.Errorf("select settings: %w", err)
	}

	values := make(map[string]string, len(rows))
	for _, row := range rows {
		values[row.Key] = row.Value
	}

	state, err := q.GetSettingsState(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return setting.Stored{Values: values, Revision: 0, Digest: ""}, nil
	}

	if err != nil {
		return setting.Stored{Values: nil, Revision: 0, Digest: ""}, fmt.Errorf("select settings state: %w", err)
	}

	return setting.Stored{Values: values, Revision: state.Revision, Digest: state.StaticDigest}, nil
}

// Load implements setting.Tx. The lock of the server audit scope, or the row of the
// namespace, orders two changes of one scope.
func (t settingTx) Load(ctx context.Context, scope setting.Scope) (setting.Stored, error) {
	return loadSettings(ctx, t.q, scope)
}

// Put implements setting.Tx.
func (t settingTx) Put(ctx context.Context, scope setting.Scope, key, value string, atMs int64) error {
	id, err := scopeID(ctx, t.q, scope)
	if err != nil {
		return err
	}

	err = t.q.UpsertSetting(ctx, db.UpsertSettingParams{ScopeID: id, Key: key, Value: value, UpdatedAtMs: atMs})
	if err != nil {
		return fmt.Errorf("upsert setting: %w", err)
	}

	return nil
}

// Remove implements setting.Tx.
func (t settingTx) Remove(ctx context.Context, scope setting.Scope, key string) error {
	id, err := scopeID(ctx, t.q, scope)
	if err != nil {
		return err
	}

	if err := t.q.DeleteSetting(ctx, db.DeleteSettingParams{ScopeID: id, Key: key}); err != nil {
		return fmt.Errorf("delete setting: %w", err)
	}

	return nil
}

// SetRevision implements setting.Tx.
func (t settingTx) SetRevision(ctx context.Context, scope setting.Scope, revision int64) error {
	id, err := scopeID(ctx, t.q, scope)
	if err != nil {
		return err
	}

	params := db.UpsertSettingsRevisionParams{ScopeID: id, Revision: revision}
	if err := t.q.UpsertSettingsRevision(ctx, params); err != nil {
		return fmt.Errorf("upsert settings revision: %w", err)
	}

	return nil
}

// SetDigest implements setting.Tx.
func (t settingTx) SetDigest(ctx context.Context, digest string, revision int64) error {
	err := t.q.UpsertSettingsDigest(ctx, db.UpsertSettingsDigestParams{
		ScopeID: serverScopeID, Revision: revision, StaticDigest: digest,
	})
	if err != nil {
		return fmt.Errorf("upsert settings digest: %w", err)
	}

	return nil
}

// Append implements setting.Tx.
func (t settingTx) Append(ctx context.Context, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	// Defense in depth: Update checks the scope of the entry before it opens
	// the transaction. This check stays so that no entry reaches the log of a
	// scope other than the scope of the change.
	if e.Namespace != t.entryScope {
		return fmt.Errorf("%w: entry names %q, want the scope %q", audit.ErrInvalidEntry, e.Namespace, t.entryScope)
	}

	if _, err := appendAuditEntry(ctx, t.q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// Load implements setting.Store.
func (st SettingStore) Load(ctx context.Context, scope setting.Scope) (setting.Stored, error) {
	return loadSettings(ctx, st.s.queries, scope)
}

// Update implements setting.Store. It runs setting.Change in one transaction
// with the audit entry. A namespace write carries the fence.
func (st SettingStore) Update(
	ctx context.Context, scope setting.Scope, fence namespace.Fence, in setting.ChangeInput,
) (setting.Result, error) {
	none := setting.Result{Resource: setting.Resource{Items: nil, Revision: 0}, Changed: nil}

	entryScope, err := settingEntryScope(scope, fence, in.Entry)
	if err != nil {
		return none, err
	}

	var result setting.Result

	run := func(q *db.Queries) error {
		// Every audited write takes the lock of the server audit scope first,
		// so the lock order is the same everywhere.
		if lockErr := q.LockServerAuditScope(ctx); lockErr != nil {
			return fmt.Errorf("lock server audit scope: %w", lockErr)
		}

		res, changeErr := setting.Change(ctx, settingTx{q: q, entryScope: entryScope}, scope, in)
		result = res

		return changeErr
	}

	if scope.IsInstallation() {
		err = st.s.inTx(ctx, run)
	} else {
		err = st.s.inFencedUpdateTx(ctx, fence, run)
	}

	if err != nil {
		return none, err
	}

	return result, nil
}

// settingEntryScope checks the entry and the fence of a change before the
// transaction opens, and returns the audit scope that the entry names.
//
// Defense in depth: the service sets the scope of the entry, and the
// transaction checks it again. This check stays so that a caller that skips
// the service still writes nothing.
func settingEntryScope(scope setting.Scope, fence namespace.Fence, e audit.Entry) (namespace.Name, error) {
	if err := e.Validate(); err != nil {
		return "", fmt.Errorf("audit entry: %w", err)
	}

	want := audit.ServerScope

	if !scope.IsInstallation() {
		want = scope.Namespace

		if fence.Namespace != scope.Namespace {
			return "", fmt.Errorf(
				"%w: fence names %q, scope names %q",
				namespace.ErrFenced,
				fence.Namespace,
				scope.Namespace,
			)
		}
	}

	if e.Namespace != want {
		return "", fmt.Errorf("%w: entry names %q, want the scope %q", audit.ErrInvalidEntry, e.Namespace, want)
	}

	return want, nil
}

// Sync implements setting.Store.
func (st SettingStore) Sync(ctx context.Context, digest string) (int64, bool, error) {
	var (
		revision int64
		changed  bool
	)

	err := st.s.inTx(ctx, func(q *db.Queries) error {
		if lockErr := q.LockServerAuditScope(ctx); lockErr != nil {
			return fmt.Errorf("lock server audit scope: %w", lockErr)
		}

		var syncErr error

		revision, changed, syncErr = setting.Sync(ctx, settingTx{q: q, entryScope: audit.ServerScope}, digest)

		return syncErr
	})
	if err != nil {
		return 0, false, err
	}

	return revision, changed, nil
}
