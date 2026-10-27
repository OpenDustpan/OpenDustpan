package namespace_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestNewTruncatesTheCreateTime proves that a new namespace holds its create
// time in UTC at millisecond precision, the precision that the store keeps.
// So the answer of a create equals the time that a later read gives.
func TestNewTruncatesTheCreateTime(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "nanoseconds",
			now:  time.Date(2026, 10, 23, 17, 10, 6, 761696938, time.UTC),
			want: time.Date(2026, 10, 23, 17, 10, 6, 761000000, time.UTC),
		},
		{
			name: "another zone",
			now:  time.Date(2026, 10, 23, 19, 10, 6, 999999999, time.FixedZone("plus2", 2*60*60)),
			want: time.Date(2026, 10, 23, 17, 10, 6, 999000000, time.UTC),
		},
		{
			name: "whole milliseconds",
			now:  time.Date(2026, 10, 23, 17, 10, 6, 144000000, time.UTC),
			want: time.Date(2026, 10, 23, 17, 10, 6, 144000000, time.UTC),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, err := namespace.NewService(namespace.Config{
				Store:        &storeOnly{},
				Now:          func() time.Time { return tc.now },
				Installation: namespace.InstallationName("eu-west"),
			})
			if err != nil {
				t.Fatalf("new service: %v", err)
			}

			got := svc.New(namespace.Name("acme")).CreatedAt
			if !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Fatalf("CreatedAt = %s, want %s in UTC",
					got.Format(time.RFC3339Nano), tc.want.Format(time.RFC3339Nano))
			}
		})
	}
}
