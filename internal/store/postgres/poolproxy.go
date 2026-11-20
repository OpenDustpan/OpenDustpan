package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
)

var _ runner.PoolProxyStore = (*Runners)(nil)

// ProxySettings implements runner.PoolProxyStore.
func (r *Runners) ProxySettings(ctx context.Context, poolID string) (runner.ProxySettingsRecord, error) {
	return loadPoolProxy(ctx, r.store.queries, poolID)
}

// loadPoolProxy returns the proxy settings of the pool poolID, or
// runner.ErrNotFound for a pool with no row. In a transaction it holds the row
// until the transaction ends.
func loadPoolProxy(ctx context.Context, q *db.Queries, poolID string) (runner.ProxySettingsRecord, error) {
	row, err := q.GetRunnerPoolProxySettings(ctx, poolID)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.ProxySettingsRecord{}, runner.ErrNotFound
	}

	if err != nil {
		return runner.ProxySettingsRecord{}, fmt.Errorf("select pool proxy settings: %w", err)
	}

	return poolProxyFromRow(&row)
}

// poolProxyFromRow decodes a stored row. A row from another version that the
// constructor refuses gives an error, never settings that it did not check.
func poolProxyFromRow(row *db.RunnerPoolProxySetting) (runner.ProxySettingsRecord, error) {
	var ranges []string
	if err := json.Unmarshal([]byte(row.AllowedRanges), &ranges); err != nil {
		return runner.ProxySettingsRecord{}, fmt.Errorf("decode allowed ranges of pool %s: %w", row.PoolID, err)
	}

	// Defense in depth: the save checks the settings too; this check stays so
	// that a row that a restore or another version wrote is checked again.
	settings, err := runner.NewProxySettings(ranges, row.UpstreamUrl)
	if err != nil {
		return runner.ProxySettingsRecord{}, fmt.Errorf("decode proxy settings of pool %s: %w", row.PoolID, err)
	}

	var credential []byte
	if len(row.Credential) != 0 {
		credential = append([]byte(nil), row.Credential...)
	}

	return runner.ProxySettingsRecord{
		Settings: settings, Credential: credential, Version: row.Version,
		UpdatedBy: row.UpdatedBy, UpdatedAt: codec.RunnerTime(row.UpdatedAtMs),
	}, nil
}

// poolProxyRanges encodes the ranges of s as a JSON array.
func poolProxyRanges(s runner.ProxySettings) (string, error) {
	b, err := json.Marshal(append([]string{}, s.AllowedRanges()...))
	if err != nil {
		return "", fmt.Errorf("encode allowed ranges: %w", err)
	}

	return string(b), nil
}

// SetProxySettings implements runner.PoolProxyStore.
func (r *Runners) SetProxySettings(
	ctx context.Context, fence namespace.Fence, poolID string, rec runner.ProxySettingsRecord, e audit.Entry,
) (runner.ProxySettingsRecord, error) {
	// Defense in depth: the service builds the settings with
	// runner.NewProxySettings; this check stays so that a caller that skips it
	// never stores a range or a URL that the constructor refuses.
	checked, err := runner.NewProxySettings(rec.Settings.AllowedRanges(), rec.Settings.UpstreamURL())
	if err != nil {
		return runner.ProxySettingsRecord{}, fmt.Errorf("check proxy settings: %w", err)
	}

	ranges, err := poolProxyRanges(checked)
	if err != nil {
		return runner.ProxySettingsRecord{}, err
	}

	var stored runner.ProxySettingsRecord

	err = r.inRunnerTx(ctx, fence, []audit.Entry{e}, func(q *db.Queries, nsID string) error {
		_, poolNS, loadErr := loadPool(ctx, q, poolID)
		if loadErr != nil {
			return loadErr
		}

		if poolNS != nsID {
			return runner.ErrNotFound
		}

		if writeErr := writePoolProxy(ctx, q, poolID, &poolProxyWrite{rec: rec, ranges: ranges}); writeErr != nil {
			return writeErr
		}

		var readErr error

		stored, readErr = loadPoolProxy(ctx, q, poolID)

		return readErr
	})
	if err != nil {
		return runner.ProxySettingsRecord{}, err
	}

	return stored, nil
}

// poolProxyWrite is the encoded value of one save.
type poolProxyWrite struct {
	rec    runner.ProxySettingsRecord
	ranges string
}

// writePoolProxy inserts the row of the pool poolID when w.rec.Version is 0 and
// the pool has no row, or moves the row to the next version when w.rec.Version
// is the stored one. Any other version is runner.ErrConflict.
func writePoolProxy(ctx context.Context, q *db.Queries, poolID string, w *poolProxyWrite) error {
	version := w.rec.Version

	current, err := q.GetRunnerPoolProxySettings(ctx, poolID)
	if errors.Is(err, pgx.ErrNoRows) {
		if version != 0 {
			return runner.ErrConflict
		}

		err = q.InsertRunnerPoolProxySettings(ctx, db.InsertRunnerPoolProxySettingsParams{
			PoolID: poolID, AllowedRanges: w.ranges, UpstreamUrl: w.rec.Settings.UpstreamURL(),
			Credential: nonNilBytes(w.rec.Credential), UpdatedBy: w.rec.UpdatedBy,
			UpdatedAtMs: codec.RunnerMillis(w.rec.UpdatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert pool proxy settings: %w", err)
		}

		return nil
	}

	if err != nil {
		return fmt.Errorf("select pool proxy settings: %w", err)
	}

	if current.Version != version {
		return runner.ErrConflict
	}

	// Defense in depth: the service clears the credential of a save that moves
	// the upstream proxy, and the handler never sends a stored credential back.
	// This check stays so that a caller that skips them never sends a stored
	// credential to another endpoint. It does not rely on either layer.
	if len(current.Credential) != 0 && bytes.Equal(current.Credential, w.rec.Credential) &&
		runner.UpstreamEndpointChanged(current.UpstreamUrl, w.rec.Settings.UpstreamURL()) {
		return fmt.Errorf("%w: the credential is for another upstream endpoint", runner.ErrInvalid)
	}

	n, err := q.UpdateRunnerPoolProxySettings(ctx, db.UpdateRunnerPoolProxySettingsParams{
		PoolID: poolID, Version: version, AllowedRanges: w.ranges, UpstreamUrl: w.rec.Settings.UpstreamURL(),
		Credential: nonNilBytes(w.rec.Credential), UpdatedBy: w.rec.UpdatedBy,
		UpdatedAtMs: codec.RunnerMillis(w.rec.UpdatedAt),
	})
	if err != nil {
		return fmt.Errorf("update pool proxy settings: %w", err)
	}

	if n != 1 {
		return runner.ErrConflict
	}

	return nil
}
