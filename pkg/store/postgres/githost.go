package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/githoststore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// GitHost returns the store of the Git host connections of the namespaces,
// of the registration of the installation-level GitHub App and of the
// installation-to-namespace map. Each write checks the installation state, and
// for a namespace its fence, and appends its audit entry in its own
// transaction.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) GitHost() forge.Store {
	return gitHost{s: s}
}

// gitHost is forge.Store over the database of a Store.
type gitHost struct {
	s *Store
}

var _ forge.Store = gitHost{s: nil}

func connectionRow(r db.GetGitHostConnectionRow) githoststore.ConnectionRow {
	return githoststore.ConnectionRow{
		Namespace: r.Namespace, ID: r.ID, Kind: r.Kind, Auth: r.Auth, AppScope: r.AppScope,
		InstallationID: r.InstallationID, Push: r.Push, IsDefault: r.IsDefault, Version: r.Version,
		CreatedAtMs: r.CreatedAtMs, UpdatedAtMs: r.UpdatedAtMs,
	}
}

func listedConnectionRow(r db.ListGitHostConnectionsRow) githoststore.ConnectionRow {
	return connectionRow(db.GetGitHostConnectionRow(r))
}

// CreateConnection implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer for the connection.
func (g gitHost) CreateConnection(
	ctx context.Context, c forge.ConnectionRecord, fence namespace.Fence, e audit.Entry,
) (forge.ConnectionRecord, error) {
	if err := c.Validate(); err != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("create connection: %w", err)
	}

	// Defense in depth: the fence check of the transaction compares the fence
	// with the namespace row. This check stays so that a connection of one
	// namespace cannot be written under the fence of another namespace,
	// whatever the audit entry says. It does not rely on the service.
	if c.Namespace != fence.Namespace {
		return forge.ConnectionRecord{}, fmt.Errorf(
			"%w: connection names %q, fence names %q", namespace.ErrFenced, c.Namespace, fence.Namespace,
		)
	}

	var out forge.ConnectionRecord

	err := g.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		e.ObjectID = c.ID
		ns := c.Namespace.String()

		n, countErr := q.CountGitHostConnections(ctx, ns)
		if countErr != nil {
			return fmt.Errorf("count connections: %w", countErr)
		}

		// The transaction holds the namespace row FOR UPDATE, so the count is exact: no
		// other create in the namespace runs between the count and the commit.
		if n >= forge.MaxConnectionsPerNamespace {
			return forge.ErrConnectionLimit
		}

		inserted, insErr := q.InsertGitHostConnection(ctx, db.InsertGitHostConnectionParams{
			ID: c.ID, Kind: string(c.Kind), Auth: string(c.Auth), AppScope: string(c.Scope),
			InstallationID: c.InstallationID, Push: c.Push, CreatedAtMs: codec.Millis(c.CreatedAt),
			UpdatedAtMs: codec.Millis(c.UpdatedAt), Namespace: ns,
		})
		if insErr != nil {
			return fmt.Errorf("insert connection: %w", insErr)
		}

		if inserted == 0 {
			return forge.ErrConnectionExists
		}

		stored, readErr := readConnection(ctx, q, c.Namespace, c.ID)
		if readErr != nil {
			return readErr
		}

		out = stored

		return finishCompletion(ctx, q, stored)
	})
	if err != nil {
		return forge.ConnectionRecord{}, err
	}

	return out, nil
}

func readConnection(
	ctx context.Context, q *db.Queries, ns namespace.Name, id string,
) (forge.ConnectionRecord, error) {
	row, err := q.GetGitHostConnection(ctx, db.GetGitHostConnectionParams{Namespace: ns.String(), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
	}

	if err != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("select connection: %w", err)
	}

	rec, err := githoststore.Connection(connectionRow(row))
	if err != nil {
		// A row that fails its checks is not served, and it reads as no
		// connection.
		return forge.ConnectionRecord{}, forge.ErrConnectionNotFound
	}

	return rec, nil
}

// GetConnection implements forge.Store.
func (g gitHost) GetConnection(ctx context.Context, ns namespace.Name, id string) (forge.ConnectionRecord, error) {
	return readConnection(ctx, g.s.queries, ns, id)
}

// ListConnections implements forge.Store. A row that fails its checks is left
// out.
func (g gitHost) ListConnections(ctx context.Context, ns namespace.Name) ([]forge.ConnectionRecord, error) {
	rows, err := g.s.queries.ListGitHostConnections(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select connections: %w", err)
	}

	out := make([]forge.ConnectionRecord, 0, len(rows))

	for i := range rows {
		rec, recErr := githoststore.Connection(listedConnectionRow(rows[i]))
		// Defense in depth: the query matched the namespace. This check stays so
		// that a change of the query cannot list a connection of another
		// namespace.
		if recErr != nil || rec.Namespace != ns {
			continue
		}

		out = append(out, rec)
	}

	return out, nil
}

// DeleteConnection implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
//
//nolint:revive // argument-limit: the signature is the one of forge.Store.
func (g gitHost) DeleteConnection(
	ctx context.Context, ns namespace.Name, id string, ifVersion int64, fence namespace.Fence, e audit.Entry,
) error {
	// Defense in depth: the fence check of the transaction compares the fence
	// with the namespace row. This check stays so that a delete in one
	// namespace cannot run under the fence of another namespace. It does not
	// rely on the service.
	if ns != fence.Namespace {
		return fmt.Errorf("%w: connection names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return g.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		e.ObjectID = id

		current, err := readConnection(ctx, q, ns, id)
		if err != nil {
			return err
		}

		if checkErr := checkDeletable(ctx, q, &current, ifVersion); checkErr != nil {
			return checkErr
		}

		n, delErr := q.DeleteGitHostConnection(ctx, db.DeleteGitHostConnectionParams{
			ID: id, Namespace: ns.String(), IfVersion: ifVersion,
		})
		if delErr != nil {
			return fmt.Errorf("delete connection: %w", delErr)
		}

		if n == 0 {
			return forge.ErrConnectionNotFound
		}

		return finishCompletion(ctx, q, current)
	})
}

// checkDeletable returns ErrVersionConflict for another version than
// ifVersion, and ErrDefaultConnection for the default connection while the
// namespace has another one.
func checkDeletable(ctx context.Context, q *db.Queries, current *forge.ConnectionRecord, ifVersion int64) error {
	if ifVersion > 0 && current.Version != ifVersion {
		return forge.ErrVersionConflict
	}

	if !current.Default {
		return nil
	}

	count, err := q.CountGitHostConnections(ctx, current.Namespace.String())
	if err != nil {
		return fmt.Errorf("count connections: %w", err)
	}

	if count > 1 {
		return forge.ErrDefaultConnection
	}

	return nil
}

// MakeDefault implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
//
//nolint:revive // argument-limit: the signature is the one of forge.Store.
func (g gitHost) MakeDefault(
	ctx context.Context, ns namespace.Name, id string, ifVersion int64, fence namespace.Fence, e audit.Entry,
) (forge.ConnectionRecord, error) {
	// Defense in depth: see DeleteConnection.
	if ns != fence.Namespace {
		return forge.ConnectionRecord{}, fmt.Errorf(
			"%w: connection names %q, fence names %q", namespace.ErrFenced, ns, fence.Namespace,
		)
	}

	if err := checkEntryFence(e, fence); err != nil {
		return forge.ConnectionRecord{}, err
	}

	var out forge.ConnectionRecord

	// The audit append reads the last entry, so the namespace row is held FOR
	// UPDATE: two make-default calls then run one after the other.
	err := g.s.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		stored, err := moveDefault(ctx, q, defaultTarget{ns: ns, id: id, ifVersion: ifVersion}, &e)
		if err != nil {
			return err
		}

		out = stored

		return finishCompletion(ctx, q, stored)
	})
	if err != nil {
		return forge.ConnectionRecord{}, err
	}

	return out, nil
}

// checkEntryFence returns an error for an audit entry that fails validation or
// names another namespace than fence. A write that does not use
// inFencedAuditedTx runs it first, so an invalid entry changes nothing.
func checkEntryFence(e audit.Entry, fence namespace.Fence) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != fence.Namespace {
		return fmt.Errorf("%w: entry names %q, fence names %q", audit.ErrInvalidEntry, e.Namespace, fence.Namespace)
	}

	return nil
}

// defaultTarget names the connection that MakeDefault changes, and the version
// that the caller read. A version of 0 sets no condition.
type defaultTarget struct {
	ns        namespace.Name
	id        string
	ifVersion int64
}

// moveDefault makes the target the default of its namespace in the transaction
// of q, and appends e. A connection that is the default already changes
// nothing and appends nothing.
func moveDefault(
	ctx context.Context, q *db.Queries, target defaultTarget, e *audit.Entry,
) (forge.ConnectionRecord, error) {
	ns, id, ifVersion := target.ns, target.id, target.ifVersion

	current, err := readConnection(ctx, q, ns, id)
	if err != nil {
		return forge.ConnectionRecord{}, err
	}

	if ifVersion > 0 && current.Version != ifVersion {
		return forge.ConnectionRecord{}, forge.ErrVersionConflict
	}

	if current.Default {
		return current, nil
	}

	at := codec.Millis(e.At)

	_, clearErr := q.ClearGitHostDefault(ctx, db.ClearGitHostDefaultParams{
		UpdatedAtMs: at, Namespace: ns.String(), ID: id,
	})
	if clearErr != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("clear default: %w", clearErr)
	}

	n, setErr := q.SetGitHostDefault(ctx, db.SetGitHostDefaultParams{
		UpdatedAtMs: at, Namespace: ns.String(), ID: id, IfVersion: ifVersion,
	})
	if isUniqueViolation(setErr) {
		// Defense in depth: the namespace lock already orders the calls. If
		// another default appears anyway, the caller read stale state.
		return forge.ConnectionRecord{}, fmt.Errorf("set default: %w", forge.ErrVersionConflict)
	}

	if setErr != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("set default: %w", setErr)
	}

	if n == 0 {
		return forge.ConnectionRecord{}, forge.ErrVersionConflict
	}

	stored, err := readConnection(ctx, q, ns, id)
	if err != nil {
		return forge.ConnectionRecord{}, err
	}

	e.ObjectID = id

	if _, appendErr := appendAuditEntry(ctx, q, *e); appendErr != nil {
		return forge.ConnectionRecord{}, fmt.Errorf("append audit entry: %w", appendErr)
	}

	return stored, nil
}

func readRegistration(ctx context.Context, q *db.Queries, kind forge.Kind) (forge.Registration, error) {
	row, err := q.GetGitHostRegistration(ctx, string(kind))
	if errors.Is(err, pgx.ErrNoRows) {
		return forge.Registration{}, forge.ErrNoRegistration
	}

	if err != nil {
		return forge.Registration{}, fmt.Errorf("select registration: %w", err)
	}

	out, err := githoststore.Registration(githoststore.RegistrationRow{
		Kind: row.Kind, AppID: row.AppID, BaseURL: row.BaseUrl, HookID: row.HookID, SealedKey: row.SealedKey,
		KeySetID: row.KeySetID, KeyCreatedAtMs: row.KeyCreatedAtMs, SealedEventSecret: row.SealedEventSecret,
		EventSecretSetID: row.EventSecretSetID, EventSecretCreatedAtMs: row.EventSecretCreatedAtMs,
		AddressDigest: row.AddressDigest, Version: row.Version, CreatedAtMs: row.CreatedAtMs,
		UpdatedAtMs: row.UpdatedAtMs,
	})
	if err != nil {
		return forge.Registration{}, fmt.Errorf("read registration: %w", err)
	}

	return out, nil
}

// GetRegistration implements forge.Store. A stored row that fails its checks
// is an error, and never a registration.
func (g gitHost) GetRegistration(ctx context.Context, kind forge.Kind) (forge.Registration, error) {
	return readRegistration(ctx, g.s.queries, kind)
}

// PutRegistration implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (g gitHost) PutRegistration(
	ctx context.Context, r forge.Registration, ifVersion int64, e audit.Entry,
) (forge.Registration, error) {
	if err := r.Validate(); err != nil {
		return forge.Registration{}, fmt.Errorf("put registration: %w", err)
	}

	var out forge.Registration

	err := g.s.inAuditedTxOf(ctx, &e, func(q *db.Queries) error {
		_, getErr := readRegistration(ctx, q, r.Kind)
		if getErr != nil && !errors.Is(getErr, forge.ErrNoRegistration) {
			return getErr
		}

		var writeErr error

		if errors.Is(getErr, forge.ErrNoRegistration) {
			writeErr = insertRegistration(ctx, q, r, ifVersion)
		} else {
			writeErr = updateRegistration(ctx, q, r, ifVersion)
		}

		if writeErr != nil {
			return writeErr
		}

		stored, readErr := readRegistration(ctx, q, r.Kind)
		if readErr != nil {
			return readErr
		}

		out = stored

		return finishCompletion(ctx, q, stored)
	})
	if err != nil {
		return forge.Registration{}, err
	}

	return out, nil
}

func insertRegistration(ctx context.Context, q *db.Queries, r forge.Registration, ifVersion int64) error {
	if ifVersion > 0 {
		return forge.ErrNoRegistration
	}

	n, err := q.InsertGitHostRegistration(ctx, db.InsertGitHostRegistrationParams{
		Kind: string(r.Kind), AppID: r.AppID, BaseUrl: r.BaseURL, HookID: r.HookID, SealedKey: r.SealedKey,
		KeySetID: r.KeySetID, KeyCreatedAtMs: codec.Millis(r.KeyCreatedAt), SealedEventSecret: r.SealedEventSecret,
		EventSecretSetID: r.EventSecretSetID, EventSecretCreatedAtMs: codec.Millis(r.EventSecretCreatedAt),
		AddressDigest: r.AddressDigest, CreatedAtMs: codec.Millis(r.CreatedAt), UpdatedAtMs: codec.Millis(r.UpdatedAt),
	})
	if err != nil {
		return fmt.Errorf("insert registration: %w", err)
	}

	if n == 0 {
		// Another write made the registration between the read and the insert.
		return forge.ErrVersionConflict
	}

	return nil
}

func updateRegistration(ctx context.Context, q *db.Queries, r forge.Registration, ifVersion int64) error {
	n, err := q.UpdateGitHostRegistration(ctx, db.UpdateGitHostRegistrationParams{
		AppID: r.AppID, BaseUrl: r.BaseURL, SealedKey: r.SealedKey, KeySetID: r.KeySetID,
		KeyCreatedAtMs: codec.Millis(r.KeyCreatedAt), SealedEventSecret: r.SealedEventSecret,
		EventSecretSetID: r.EventSecretSetID, EventSecretCreatedAtMs: codec.Millis(r.EventSecretCreatedAt),
		AddressDigest: r.AddressDigest, UpdatedAtMs: codec.Millis(r.UpdatedAt), Kind: string(r.Kind),
		IfVersion: ifVersion,
	})
	if err != nil {
		return fmt.Errorf("update registration: %w", err)
	}

	if n == 0 {
		return forge.ErrVersionConflict
	}

	return nil
}

// DeleteRegistration implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (g gitHost) DeleteRegistration(ctx context.Context, kind forge.Kind, ifVersion int64, e audit.Entry) error {
	return g.s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		current, err := readRegistration(ctx, q, kind)
		if err != nil {
			return err
		}

		if ifVersion > 0 && current.Version != ifVersion {
			return forge.ErrVersionConflict
		}

		assigned, countErr := q.CountGitHostInstallations(ctx)
		if countErr != nil {
			return fmt.Errorf("count assignments: %w", countErr)
		}

		if assigned > 0 {
			return forge.ErrRegistrationInUse
		}

		n, delErr := q.DeleteGitHostRegistration(ctx, db.DeleteGitHostRegistrationParams{
			Kind: string(kind), IfVersion: ifVersion,
		})
		if delErr != nil {
			return fmt.Errorf("delete registration: %w", delErr)
		}

		if n == 0 {
			return forge.ErrVersionConflict
		}

		return finishCompletion(ctx, q, current)
	})
}

func assignmentRow(r db.GetGitHostInstallationRow) githoststore.AssignmentRow {
	return githoststore.AssignmentRow{
		InstallationID: r.InstallationID, Namespace: r.Namespace, Version: r.Version, CreatedAtMs: r.CreatedAtMs,
		UpdatedAtMs: r.UpdatedAtMs,
	}
}

func readAssignment(ctx context.Context, q *db.Queries, installationID string) (forge.Assignment, error) {
	row, err := q.GetGitHostInstallation(ctx, installationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return forge.Assignment{}, forge.ErrAssignmentNotFound
	}

	if err != nil {
		return forge.Assignment{}, fmt.Errorf("select assignment: %w", err)
	}

	out, err := githoststore.Assignment(assignmentRow(row))
	if err != nil {
		return forge.Assignment{}, fmt.Errorf("read assignment: %w", err)
	}

	return out, nil
}

// GetAssignment implements forge.Store. A stored row that fails its checks is
// an error, and never an assignment.
func (g gitHost) GetAssignment(ctx context.Context, installationID string) (forge.Assignment, error) {
	return readAssignment(ctx, g.s.queries, installationID)
}

// ListAssignments implements forge.Store. A row that fails its checks is an
// error: the operator must see it.
func (g gitHost) ListAssignments(ctx context.Context) ([]forge.Assignment, error) {
	rows, err := g.s.queries.ListGitHostInstallations(ctx)
	if err != nil {
		return nil, fmt.Errorf("select assignments: %w", err)
	}

	out := make([]forge.Assignment, 0, len(rows))

	for i := range rows {
		a, aErr := githoststore.Assignment(assignmentRow(db.GetGitHostInstallationRow(rows[i])))
		if aErr != nil {
			return nil, fmt.Errorf("read assignment: %w", aErr)
		}

		out = append(out, a)
	}

	return out, nil
}

// PutAssignment implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (g gitHost) PutAssignment(
	ctx context.Context, a forge.Assignment, ifVersion int64, e audit.Entry,
) (forge.Assignment, error) {
	if err := a.Validate(); err != nil {
		return forge.Assignment{}, fmt.Errorf("put assignment: %w", err)
	}

	var out forge.Assignment

	err := g.s.inAuditedTxOf(ctx, &e, func(q *db.Queries) error {
		if _, idErr := q.GitHostNamespaceID(ctx, a.Namespace.String()); errors.Is(idErr, pgx.ErrNoRows) {
			return namespace.ErrNotFound
		} else if idErr != nil {
			return fmt.Errorf("select namespace: %w", idErr)
		}

		if err := writeAssignment(ctx, q, a, ifVersion); err != nil {
			return err
		}

		stored, readErr := readAssignment(ctx, q, a.InstallationID)
		if readErr != nil {
			return readErr
		}

		out = stored

		return finishCompletion(ctx, q, stored)
	})
	if err != nil {
		return forge.Assignment{}, err
	}

	return out, nil
}

func writeAssignment(ctx context.Context, q *db.Queries, a forge.Assignment, ifVersion int64) error {
	_, getErr := readAssignment(ctx, q, a.InstallationID)
	if getErr != nil && !errors.Is(getErr, forge.ErrAssignmentNotFound) {
		return getErr
	}

	if errors.Is(getErr, forge.ErrAssignmentNotFound) {
		if ifVersion > 0 {
			return forge.ErrAssignmentNotFound
		}

		n, err := q.InsertGitHostInstallation(ctx, db.InsertGitHostInstallationParams{
			InstallationID: a.InstallationID, CreatedAtMs: codec.Millis(a.CreatedAt),
			UpdatedAtMs: codec.Millis(a.UpdatedAt), Namespace: a.Namespace.String(),
		})
		if err != nil {
			return fmt.Errorf("insert assignment: %w", err)
		}

		if n == 0 {
			// Another write made the assignment between the read and the insert.
			return forge.ErrVersionConflict
		}

		return nil
	}

	n, err := q.UpdateGitHostInstallation(ctx, db.UpdateGitHostInstallationParams{
		Namespace: a.Namespace.String(), UpdatedAtMs: codec.Millis(a.UpdatedAt), InstallationID: a.InstallationID,
		IfVersion: ifVersion,
	})
	if err != nil {
		return fmt.Errorf("update assignment: %w", err)
	}

	if n == 0 {
		return forge.ErrVersionConflict
	}

	return nil
}

// DeleteAssignment implements forge.Store. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (g gitHost) DeleteAssignment(ctx context.Context, installationID string, ifVersion int64, e audit.Entry) error {
	return g.s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		current, err := readAssignment(ctx, q, installationID)
		if err != nil {
			return err
		}

		if ifVersion > 0 && current.Version != ifVersion {
			return forge.ErrVersionConflict
		}

		n, delErr := q.DeleteGitHostInstallation(ctx, db.DeleteGitHostInstallationParams{
			InstallationID: installationID, IfVersion: ifVersion,
		})
		if delErr != nil {
			return fmt.Errorf("delete assignment: %w", delErr)
		}

		if n == 0 {
			return forge.ErrVersionConflict
		}

		return finishCompletion(ctx, q, current)
	})
}
