package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/preferences/preferencestest"
	"opendustpan.org/opendustpan/pkg/store/postgres"
)

// rawPreferencesStore wraps a Store with its schema URL, so the suite can write
// a row of preferences with direct SQL.
type rawPreferencesStore struct {
	*postgres.Store

	url string
}

var _ preferencestest.RawWriter = (*rawPreferencesStore)(nil)

// WriteRawPreferences implements preferencestest.RawWriter.
func (s *rawPreferencesStore) WriteRawPreferences(
	ctx context.Context, personID, document string, updatedAtMs int64,
) (err error) {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	_, err = conn.Exec(ctx,
		`INSERT INTO person_preferences (principal_id, document, updated_at_ms) VALUES ($1, $2, $3)
ON CONFLICT (principal_id) DO UPDATE SET document = excluded.document, updated_at_ms = excluded.updated_at_ms`,
		personID, document, updatedAtMs)
	if err != nil {
		return fmt.Errorf("write the row: %w", err)
	}

	return nil
}

func TestPreferencesConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	preferencestest.Run(t, func(t *testing.T) preferencestest.Store {
		t.Helper()

		url := isolatedURL(t, base)

		return &rawPreferencesStore{Store: openURL(t, url), url: url}
	})
}
