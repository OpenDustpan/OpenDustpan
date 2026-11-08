package sqlite

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/registry"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

// registryLimitRecorder wraps the database of a store and records the limit
// argument of every registry address list query, so a test can prove that
// the store reads one row more than the page.
type registryLimitRecorder struct {
	db.DBTX

	limits []any
}

// QueryContext runs query and records the limit argument of a registry
// address list query.
func (r *registryLimitRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if strings.Contains(query, "ListRegistryAddresses") && len(args) > 0 {
		r.limits = append(r.limits, args[len(args)-1])
	}

	return r.DBTX.QueryContext(ctx, query, args...)
}

// TestRegistryLimitReadsOneMore proves that the address list asks the query
// for one row more than the page, so that the extra row tells whether a next
// page exists.
func TestRegistryLimitReadsOneMore(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		limit int
	}{
		{"a page of one", 1},
		{"a page of two", 2},
		{"the largest page", registry.MaxAddressPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := openInternalTemplated(t)
			recorder := &registryLimitRecorder{DBTX: s.conn, limits: nil}
			s.queries = db.New(recorder)

			_, _, err := s.Registry().Addresses(t.Context(), "acme", registry.KindProvider, "", tc.limit)
			if err != nil {
				t.Fatalf("list addresses: %v", err)
			}

			if len(recorder.limits) != 1 {
				t.Fatalf("registry address list queries = %d, want one", len(recorder.limits))
			}

			limit, ok := recorder.limits[0].(int64)
			if !ok {
				t.Fatalf("registry address list limit is %T, want int64", recorder.limits[0])
			}

			if limit != int64(tc.limit)+1 {
				t.Errorf("registry address list limit = %d, want %d", limit, tc.limit+1)
			}
		})
	}
}
