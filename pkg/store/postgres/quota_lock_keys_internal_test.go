package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// lockRecorder is a db.DBTX that records the scope of each quota key row that
// lockQuotaKeys makes and locks, in call order.
type lockRecorder struct {
	made   []string
	locked []string
}

// Exec records the scope of an EnsureQuotaKey call. The scope is argument 4.
func (r *lockRecorder) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	r.made = append(r.made, scopeArg(args))

	return pgconn.CommandTag{}, nil
}

// Query is not used by lockQuotaKeys.
func (r *lockRecorder) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	panic("lockQuotaKeys must not call Query")
}

// QueryRow records the scope of a LockQuotaKey call. The scope is argument 4.
func (r *lockRecorder) QueryRow(_ context.Context, _ string, args ...any) pgx.Row {
	r.locked = append(r.locked, scopeArg(args))

	return zeroRow{}
}

// scopeArg returns the scope argument of a quota key query, or an empty string.
func scopeArg(args []any) string {
	if len(args) < 4 {
		return ""
	}

	scope, ok := args[3].(string)
	if !ok {
		return ""
	}

	return scope
}

// zeroRow scans a settled count of zero.
type zeroRow struct{}

// Scan implements pgx.Row.
func (zeroRow) Scan(dest ...any) error {
	if p, ok := dest[0].(*int64); ok {
		*p = 0
	}

	return nil
}

// TestLockQuotaKeysLocksInByteOrder proves that lockQuotaKeys makes and locks
// the key rows in the byte order of Key.String whatever the order of the
// checks, so that two transactions with the same keys lock them in one order
// and cannot deadlock. The test fails when lockQuotaKeys uses the order of the
// checks.
func TestLockQuotaKeysLocksInByteOrder(t *testing.T) {
	t.Parallel()

	key := func(scope string) quota.Key {
		return quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: scope}
	}

	tests := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name:   "reverse",
			scopes: []string{"requester:bob", "namespace", "group:a"},
			want:   []string{"group:a", "namespace", "requester:bob"},
		},
		{name: "upper case first", scopes: []string{"group:a", "group:B"}, want: []string{"group:B", "group:a"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checks := make([]quota.Check, 0, len(tc.scopes))
			for _, s := range tc.scopes {
				checks = append(checks, quota.Check{Key: key(s), Window: 0, Max: 1})
			}

			rec := &lockRecorder{made: nil, locked: nil}

			if err := lockQuotaKeys(t.Context(), db.New(rec), checks); err != nil {
				t.Fatalf("lockQuotaKeys() = %v", err)
			}

			if !slices.Equal(rec.made, tc.want) {
				t.Fatalf("lockQuotaKeys made rows in order %v, want %v", rec.made, tc.want)
			}

			if !slices.Equal(rec.locked, tc.want) {
				t.Fatalf("lockQuotaKeys locked rows in order %v, want %v", rec.locked, tc.want)
			}
		})
	}
}
