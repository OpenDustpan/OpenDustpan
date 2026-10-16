package sqlite

// The domain side of the durable operations suite. It writes a test table
// in the transactions of the store, through the same step transaction and
// step record that a store method of another port uses. This file compiles
// into the test binary only: nothing here ships in production.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// ErrStepTransactionBoundForTest is errStepTransactionBound, for the stall
// test of the step transaction bound.
var ErrStepTransactionBoundForTest = errStepTransactionBound

// durableTestRegion is the home region of the namespaces of the domain.
const durableTestRegion = namespace.Region("test-region")

// createDurableValues creates the domain table of the suite.
const createDurableValues = `CREATE TABLE IF NOT EXISTS durabletest_values (value TEXT NOT NULL) STRICT`

// durableDomain is the durabletest.Domain of one store.
type durableDomain struct {
	s *Store
}

// DurableDomainForTest returns the domain side of the suite on s. It creates
// its test table in the database of s.
//
//nolint:ireturn // The suite takes the Domain port.
func DurableDomainForTest(ctx context.Context, s *Store) (durabletest.Domain, error) {
	if _, err := s.conn.ExecContext(ctx, createDurableValues); err != nil {
		return nil, fmt.Errorf("create the domain table: %w", err)
	}

	return durableDomain{s: s}, nil
}

func (d durableDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	err := d.s.CreateNamespace(ctx, namespace.Namespace{
		CreatedAt:       time.Now(),
		Name:            name,
		HomeRegion:      durableTestRegion,
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		return namespace.Fence{}, err
	}

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

func (d durableDomain) BumpFences(ctx context.Context) error {
	d.s.mu.RLock()
	defer d.s.mu.RUnlock()

	return d.s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.BumpFailoverVersions(ctx); err != nil {
			return fmt.Errorf("bump failover versions: %w", err)
		}

		return nil
	})
}

func (d durableDomain) WriteStep(
	ctx context.Context, f durable.StepFence, ns namespace.Fence, value string, hooks durabletest.Hooks,
) error {
	d.s.mu.RLock()
	defer d.s.mu.RUnlock()

	return d.s.inStepTx(ctx, func(tx *sql.Tx, q *db.Queries) error {
		if err := hooks.Run(hooks.Before); err != nil {
			return err
		}

		version, err := q.GetFailoverVersion(ctx, ns.Namespace.String())
		if errors.Is(err, sql.ErrNoRows) {
			return namespace.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("read failover version: %w", err)
		}

		if version != ns.FailoverVersion {
			return namespace.ErrFenced
		}

		if err := recordStep(ctx, q, f, nil); err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx, `INSERT INTO durabletest_values (value) VALUES (?)`, value); err != nil {
			return fmt.Errorf("insert domain value: %w", err)
		}

		return hooks.Run(hooks.After)
	})
}

func (d durableDomain) StartWith(
	ctx context.Context, req durable.StartRequest, value string, hooks durabletest.Hooks,
) (durable.Operation, error) {
	d.s.mu.RLock()
	defer d.s.mu.RUnlock()

	var op durable.Operation

	err := d.s.inStepTx(ctx, func(tx *sql.Tx, q *db.Queries) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO durabletest_values (value) VALUES (?)`, value); err != nil {
			return fmt.Errorf("insert domain value: %w", err)
		}

		var err error

		op, _, err = startDurable(ctx, q, req)
		if err != nil {
			return err
		}

		return hooks.Run(hooks.After)
	})
	if err != nil {
		return durable.Operation{}, err
	}

	return op, nil
}

func (d durableDomain) Values(ctx context.Context) ([]string, error) {
	rows, err := d.s.conn.QueryContext(ctx, `SELECT value FROM durabletest_values`)
	if err != nil {
		return nil, fmt.Errorf("read domain values: %w", err)
	}

	defer func() {
		_ = rows.Close() //nolint:errcheck // rows.Err below reports a read failure.
	}()

	var out []string

	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("read domain value: %w", err)
		}

		out = append(out, value)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read domain values: %w", err)
	}

	return out, nil
}
