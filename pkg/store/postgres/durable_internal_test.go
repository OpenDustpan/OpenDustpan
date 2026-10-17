package postgres

// Test-only domain side of the durable operations suite. This file compiles
// into the test binary only. The suite needs domain writes that share a
// transaction with the store of durable operations, as a real domain store
// method that takes a StepFence or a StartRequest does.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/durable/durabletest"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// durableDomain is the durabletest.Domain of a Store. It keeps its values in
// the test table durable_test_values, and checks namespace fences on the
// real namespaces table.
type durableDomain struct {
	store *Store
}

// DurableDomainForTest creates the test table of the domain values in the
// schema of s, and returns the domain side of the durable operations suite.
//
//nolint:ireturn // The suite takes the Domain port.
func DurableDomainForTest(ctx context.Context, s *Store) (durabletest.Domain, error) {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE durable_test_values (value TEXT NOT NULL)`); err != nil {
		return nil, fmt.Errorf("create the domain test table: %w", err)
	}

	return durableDomain{store: s}, nil
}

func (d durableDomain) CreateNamespace(ctx context.Context, name namespace.Name) (namespace.Fence, error) {
	ns := namespace.Namespace{
		CreatedAt: time.Now(), Name: name, HomeRegion: "local",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: 1,
	}

	if err := d.store.CreateNamespace(ctx, ns); err != nil {
		return namespace.Fence{}, err
	}

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

func (d durableDomain) BumpFences(ctx context.Context) error {
	if _, err := d.store.pool.Exec(ctx, `UPDATE namespaces SET failover_version = failover_version + 1`); err != nil {
		return fmt.Errorf("bump fences: %w", err)
	}

	return nil
}

func (d durableDomain) WriteStep(
	ctx context.Context,
	f durable.StepFence,
	ns namespace.Fence,
	value string,
	hooks durabletest.Hooks,
) error {
	return d.inTx(ctx, func(tx pgx.Tx, q *db.Queries) error {
		if err := hooks.Run(hooks.Before); err != nil {
			return err
		}

		version, err := q.GetFailoverVersionForShare(ctx, ns.Namespace.String())
		if err != nil {
			return fmt.Errorf("read failover version: %w", err)
		}

		if version != ns.FailoverVersion {
			return namespace.ErrFenced
		}

		if err := recordStep(ctx, q, f, nil); err != nil {
			return err
		}

		if err := insertTestValue(ctx, tx, value); err != nil {
			return err
		}

		return hooks.Run(hooks.After)
	})
}

func (d durableDomain) StartWith(
	ctx context.Context, req durable.StartRequest, value string, hooks durabletest.Hooks,
) (durable.Operation, error) {
	var op durable.Operation

	err := d.inTx(ctx, func(tx pgx.Tx, q *db.Queries) error {
		if err := insertTestValue(ctx, tx, value); err != nil {
			return err
		}

		var startErr error

		op, _, startErr = startOperation(ctx, q, req)
		if startErr != nil {
			return startErr
		}

		return hooks.Run(hooks.After)
	})
	if err != nil {
		return durable.Operation{}, err
	}

	return op, nil
}

func (d durableDomain) Values(ctx context.Context) ([]string, error) {
	rows, err := d.store.pool.Query(ctx, `SELECT value FROM durable_test_values`)
	if err != nil {
		return nil, fmt.Errorf("read values: %w", err)
	}

	defer rows.Close()

	var out []string

	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan value: %w", err)
		}

		out = append(out, v)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read values: %w", err)
	}

	return out, nil
}

// inTx runs fn in one transaction, with the transaction for the test table
// and its queries for the store. An error of fn rolls the transaction back.
func (d durableDomain) inTx(ctx context.Context, fn func(tx pgx.Tx, q *db.Queries) error) error {
	err := pgx.BeginFunc(ctx, d.store.pool, func(tx pgx.Tx) error {
		return fn(tx, d.store.queries.WithTx(tx))
	})
	if err != nil {
		return fmt.Errorf("domain transaction: %w", err)
	}

	return nil
}

// insertTestValue writes value in the test table in tx. sqlc knows only the
// tables of the migrations, so the test table has a hand-written statement.
func insertTestValue(ctx context.Context, tx pgx.Tx, value string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO durable_test_values (value) VALUES ($1)`, value); err != nil {
		return fmt.Errorf("insert value: %w", err)
	}

	return nil
}
