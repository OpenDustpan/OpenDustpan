package dustpan_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"go.uber.org/goleak"
)

// groupSchemaPrefixEnv names the variable that holds the start of the name of
// each schema that the group directory tests make.
const groupSchemaPrefixEnv = "DUSTPAN_TEST_GROUP_SCHEMA_PREFIX"

// TestMain fails the package when a test leaves a goroutine running, or a
// group directory schema on the PostgreSQL server.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(schemaCheck{m: m})
}

// schemaCheck runs the tests and then lists the schemas that the group
// directory tests made and did not drop. The name of each such schema starts
// with a prefix that is unique for the test binary, so a run of another
// binary on the same server does not change the result.
type schemaCheck struct {
	m *testing.M
}

// Run implements goleak.TestingM.
func (c schemaCheck) Run() int {
	base := os.Getenv("DUSTPAN_TEST_POSTGRES_URL")
	if base == "" {
		return c.m.Run()
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		fmt.Fprintf(os.Stderr, "random schema prefix: %v\n", err)

		return 1
	}

	prefix := "grp_" + hex.EncodeToString(suffix) + "_"
	if err := os.Setenv(groupSchemaPrefixEnv, prefix); err != nil {
		fmt.Fprintf(os.Stderr, "set %s: %v\n", groupSchemaPrefixEnv, err)

		return 1
	}

	code := c.m.Run()

	left, err := schemasWithPrefix(context.Background(), base, prefix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list the schemas that stay: %v\n", err)

		return 1
	}

	if len(left) > 0 {
		fmt.Fprintf(os.Stderr, "schemas stay after the tests: %v\n", left)

		return 1
	}

	return code
}

// schemasWithPrefix lists the schemas of the database at base whose name
// starts with prefix.
func schemasWithPrefix(ctx context.Context, base, prefix string) ([]string, error) {
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	rows, err := conn.Query(
		ctx,
		"SELECT nspname FROM pg_namespace WHERE starts_with(nspname, $1) ORDER BY nspname",
		prefix,
	)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list schemas: %w", err), conn.Close(ctx))
	}

	left, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, errors.Join(fmt.Errorf("list schemas: %w", err), conn.Close(ctx))
	}

	if closeErr := conn.Close(ctx); closeErr != nil {
		return nil, fmt.Errorf("close: %w", closeErr)
	}

	return left, nil
}
