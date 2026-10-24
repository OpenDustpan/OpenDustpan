package postgres //nolint:testpackage // The pool is unexported, so the exporter must stay in that package.

import (
	"context"
	"fmt"
)

// ExecForTest runs one statement on the database with the given arguments. A
// test uses it to plant a row that no port writes, as a row of another version
// or a restore would bring in.
func (s *Store) ExecForTest(ctx context.Context, query string, args ...any) error {
	if _, err := s.pool.Exec(ctx, query, args...); err != nil {
		return fmt.Errorf("exec for test: %w", err)
	}

	return nil
}
