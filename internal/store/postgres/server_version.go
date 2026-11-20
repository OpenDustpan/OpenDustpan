package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// minServerMajor is the lowest PostgreSQL major version that the server starts on.
const minServerMajor = 18

// serverVersionDivisor turns server_version_num into the major version: 180006 is major version 18.
const serverVersionDivisor = 10000

// ErrServerVersion reports a PostgreSQL server that the server does not support. The major version is
// below the minimum, or the version value is not a number. The server does not start.
var ErrServerVersion = errors.New("postgres server version is not supported")

// checkConnection pings the pool and checks the server version. The caller closes the pool on an error.
func checkConnection(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}

	var raw string
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')").Scan(&raw); err != nil {
		return fmt.Errorf("read postgres server version: %w", err)
	}

	// Defense in depth: the server starts on a supported version only. This check runs before any migration.
	return checkServerVersion(raw)
}

// checkServerVersion checks the value of server_version_num, for example 180006 for PostgreSQL 18.6. It returns
// an error that wraps ErrServerVersion when the major version is below minServerMajor, or when the value is not
// a positive number.
func checkServerVersion(raw string) error {
	num, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || num <= 0 {
		// The value is not echoed: it comes from the server and is not trusted.
		return fmt.Errorf("%w: the server version value is not a version number", ErrServerVersion)
	}

	if major := num / serverVersionDivisor; major < minServerMajor {
		return fmt.Errorf("%w: found server_version_num %d, the minimum major version is %d",
			ErrServerVersion, num, minServerMajor)
	}

	return nil
}
