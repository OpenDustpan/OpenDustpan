package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/preferences/preferencestest"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// rawPreferencesStore wraps a Store with its path, so the suite can write a
// row of preferences with direct SQL.
type rawPreferencesStore struct {
	*sqlite.Store

	path string
}

var _ preferencestest.RawWriter = (*rawPreferencesStore)(nil)

// WriteRawPreferences implements preferencestest.RawWriter.
func (s *rawPreferencesStore) WriteRawPreferences(
	ctx context.Context, personID, document string, updatedAtMs int64,
) (err error) {
	conn, err := openRawDB(s.path)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	_, err = conn.ExecContext(ctx,
		`INSERT INTO person_preferences (principal_id, document, updated_at_ms) VALUES (?, ?, ?)
ON CONFLICT (principal_id) DO UPDATE SET document = excluded.document, updated_at_ms = excluded.updated_at_ms`,
		personID, document, updatedAtMs)
	if err != nil {
		return fmt.Errorf("write the row: %w", err)
	}

	return nil
}

func TestPreferencesConformance(t *testing.T) {
	t.Parallel()

	preferencestest.Run(t, func(t *testing.T) preferencestest.Store {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return &rawPreferencesStore{Store: s, path: path}
	})
}
