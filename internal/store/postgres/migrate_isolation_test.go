package postgres_test

import (
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/pkg/accesstoken/accesstokentest"
)

// TestConcurrentOpenOnRepeatableReadServer proves that instances that start at
// the same time on one empty database all start, when the server default is
// REPEATABLE READ. The migration transaction takes the migration lock first
// and then reads the recorded migrations. Under READ COMMITTED each statement
// takes a new snapshot after the lock wait, so an instance that waited sees the
// migrations of the first one and applies none. Under REPEATABLE READ the
// snapshot is taken before the wait, and the instance may plan the same
// migrations again and fail with a duplicate key or "already exists" error.
//
// Mutation: change migrate back to pgx.BeginFunc. Some of the opens then fail.
func TestConcurrentOpenOnRepeatableReadServer(t *testing.T) {
	t.Parallel()

	u, err := url.Parse(emptySchemaURL(t, baseURL(t)))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	q := u.Query()
	q.Set("default_transaction_isolation", "repeatable read")
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")

	const instances = 6

	errs := make([]error, instances)

	var wg sync.WaitGroup

	for i := range instances {
		wg.Add(1)

		go func() {
			defer wg.Done()

			s, openErr := postgres.Open(t.Context(), u.String())
			if openErr == nil {
				s.Close()
			}

			errs[i] = openErr
		}()
	}

	wg.Wait()

	for i, openErr := range errs {
		if openErr != nil {
			t.Errorf("instance %d: open: %v", i, openErr)
		}
	}
}

// TestHostRecoveryRunsAtReadCommitted proves that CreateHostRecovery pins READ
// COMMITTED when the server default is REPEATABLE READ. A trigger on the table
// temp_admins refuses the insert of the recovery when its transaction runs at
// another level, so the access token suite, which creates host recoveries,
// fails when the pin is missing.
//
// Mutation: make CreateHostRecovery begin with pgx.BeginFunc. The insert into
// temp_admins is then refused, and the host recovery cases fail.
func TestHostRecoveryRunsAtReadCommitted(t *testing.T) {
	t.Parallel()

	base := baseURL(t)

	accesstokentest.Run(t, func(t *testing.T) accesstokentest.Store {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		conn, err := pgx.Connect(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}

		defer func() {
			if closeErr := conn.Close(t.Context()); closeErr != nil {
				t.Errorf("close: %v", closeErr)
			}
		}()

		const probe = `
CREATE FUNCTION require_read_committed() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF current_setting('transaction_isolation') <> 'read committed' THEN
		RAISE EXCEPTION 'temp_admins written at %', current_setting('transaction_isolation');
	END IF;
	RETURN NEW;
END $$;
CREATE TRIGGER temp_admins_require_read_committed BEFORE INSERT ON temp_admins
	FOR EACH ROW EXECUTE FUNCTION require_read_committed()`

		if _, execErr := conn.Exec(t.Context(), probe); execErr != nil {
			t.Fatalf("create the probe trigger: %v", execErr)
		}

		u, parseErr := url.Parse(schemaURL)
		if parseErr != nil {
			t.Fatalf("parse url: %v", parseErr)
		}

		q := u.Query()
		q.Set("default_transaction_isolation", "repeatable read")
		u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")

		return &rawTokenStore{BaseStore: openURL(t, u.String()), url: schemaURL}
	})
}
