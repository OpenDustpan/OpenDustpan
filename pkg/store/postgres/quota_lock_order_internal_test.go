package postgres

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/quota"
)

// TestQuotaLockOrder proves that quotaLockOrder sorts keys in the byte order
// of Key.String, so that a reserve and a settle lock the rows of shared keys in
// one order. A collation that ignores case would put "group:a" before
// "group:B"; the byte order puts "group:B" first.
func TestQuotaLockOrder(t *testing.T) {
	t.Parallel()

	key := func(scope string) quota.Key {
		return quota.Key{Namespace: "payments", Blueprint: "postgres", Kind: quota.KindUnits, Scope: scope}
	}

	tests := []struct {
		name string
		in   []quota.Key
		want []quota.Key
	}{
		{name: "none", in: nil, want: []quota.Key{}},
		{name: "one", in: []quota.Key{key("namespace")}, want: []quota.Key{key("namespace")}},
		{
			name: "upper case before lower case",
			in:   []quota.Key{key("group:a"), key("group:B")},
			want: []quota.Key{key("group:B"), key("group:a")},
		},
		{
			name: "sorted stays sorted",
			in:   []quota.Key{key("group:B"), key("group:a"), key("namespace")},
			want: []quota.Key{key("group:B"), key("group:a"), key("namespace")},
		},
		{
			name: "reverse",
			in:   []quota.Key{key("requester:bob"), key("namespace"), key("group:a")},
			want: []quota.Key{key("group:a"), key("namespace"), key("requester:bob")},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			in := slices.Clone(tc.in)

			got := quotaLockOrder(in)
			if len(got) != len(tc.want) || !slices.Equal(got, tc.want) {
				t.Fatalf("quotaLockOrder(%v) = %v, want %v", tc.in, got, tc.want)
			}

			if !slices.Equal(in, tc.in) {
				t.Fatalf("quotaLockOrder changed its input to %v, want %v", in, tc.in)
			}
		})
	}
}
