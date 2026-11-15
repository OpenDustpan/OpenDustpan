package dustpan

import (
	"context"
	"errors"
	"testing"
)

// TestOpenStoreConfigDispatchesByForm proves that the dispatch layer sends
// each database value to the right open function: a SQLite path to the SQLite
// open, a PostgreSQL DSN to the PostgreSQL open, and a value of another form
// to neither open function. The stubs record each call, so a dispatch that
// sends a value of another form to the PostgreSQL open fails the test.
func TestOpenStoreConfigDispatchesByForm(t *testing.T) {
	t.Parallel()

	refused := []struct {
		name     string
		database string
	}{
		{name: "empty value", database: ""},
		{name: "spaces", database: " "},
		{name: "other scheme", database: "mysql://x"},
		{name: "other scheme with host", database: "mysql://h/db"},
		{name: "keyword form", database: "host=h dbname=d"},
		{name: "missing slash", database: "postgres:/h/db"},
		{name: "wrong case", database: "POSTGRES://h/db"},
		{name: "leading space", database: " postgres://h/db"},
		{name: "SQLite without a colon", database: "sqlite"},
		{name: "opaque form", database: "postgresql:db.example"},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkOpenStoreConfigRefused(t, tc.database)
		})
	}

	t.Run("SQLite path reaches the SQLite open", func(t *testing.T) {
		t.Parallel()

		checkOpenStoreConfigSQLite(t)
	})

	for _, database := range []string{"postgres://h/db", "postgresql://h/db"} {
		t.Run("PostgreSQL DSN "+database+" reaches the PostgreSQL open", func(t *testing.T) {
			t.Parallel()

			checkOpenStoreConfigPostgres(t, database)
		})
	}
}

// checkOpenStoreConfigRefused proves that database reaches neither open
// function and fails with the form error.
func checkOpenStoreConfigRefused(t *testing.T, database string) {
	t.Helper()

	var sqliteCalled, postgresCalled bool

	_, err := openStoreConfigWithOpeners(t.Context(), database, defaultStoreConfig(),
		func(_ context.Context, _ string, _ storeConfig) (*BuiltinStore, error) {
			sqliteCalled = true

			return new(BuiltinStore), nil
		},
		func(_ context.Context, _ string, _ storeConfig) (*BuiltinStore, error) {
			postgresCalled = true

			return new(BuiltinStore), nil
		},
	)
	if !errors.Is(err, errStoreDatabaseForm) {
		t.Fatalf("openStoreConfig(%q) = %v, want the form error", database, err)
	}

	if sqliteCalled || postgresCalled {
		t.Fatalf("openStoreConfig(%q) called sqlite=%t postgres=%t, want neither open",
			database, sqliteCalled, postgresCalled)
	}
}

// checkOpenStoreConfigSQLite proves that a SQLite path reaches the SQLite open
// with the path after the colon, and never the PostgreSQL open.
func checkOpenStoreConfigSQLite(t *testing.T) {
	t.Helper()

	var sqliteCalled, postgresCalled bool

	stub := new(BuiltinStore)

	_, err := openStoreConfigWithOpeners(t.Context(), "sqlite:/tmp/dustpan.db", defaultStoreConfig(),
		func(_ context.Context, path string, _ storeConfig) (*BuiltinStore, error) {
			sqliteCalled = true
			if path != "/tmp/dustpan.db" {
				t.Errorf("SQLite path = %q, want %q", path, "/tmp/dustpan.db")
			}

			return stub, nil
		},
		func(_ context.Context, _ string, _ storeConfig) (*BuiltinStore, error) {
			postgresCalled = true

			return stub, nil
		},
	)
	if err != nil {
		t.Fatalf("openStoreConfig(SQLite path) = %v, want the stub store", err)
	}

	if !sqliteCalled || postgresCalled {
		t.Fatalf("openStoreConfig(SQLite path) called sqlite=%t postgres=%t, want only the SQLite open",
			sqliteCalled, postgresCalled)
	}
}

// checkOpenStoreConfigPostgres proves that database reaches the PostgreSQL
// open with the whole value, and never the SQLite open.
func checkOpenStoreConfigPostgres(t *testing.T, database string) {
	t.Helper()

	var sqliteCalled, postgresCalled bool

	stub := new(BuiltinStore)

	got, err := openStoreConfigWithOpeners(t.Context(), database, defaultStoreConfig(),
		func(_ context.Context, _ string, _ storeConfig) (*BuiltinStore, error) {
			sqliteCalled = true

			return stub, nil
		},
		func(_ context.Context, dsn string, _ storeConfig) (*BuiltinStore, error) {
			postgresCalled = true
			if dsn != database {
				t.Errorf("PostgreSQL DSN = %q, want %q", dsn, database)
			}

			return stub, nil
		},
	)
	if err != nil {
		t.Fatalf("openStoreConfig(%q) = %v, want the stub store", database, err)
	}

	if got != stub {
		t.Fatal("openStoreConfig did not return the store of the PostgreSQL open")
	}

	if sqliteCalled || !postgresCalled {
		t.Fatalf("openStoreConfig(%q) called sqlite=%t postgres=%t, want only the PostgreSQL open",
			database, sqliteCalled, postgresCalled)
	}
}
