package blob_test

import (
	"testing"

	"opendustpan.org/opendustpan/pkg/blob"
)

func TestStoreCheckKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  blob.Key
		want bool
	}{
		{"store-checks/chk-a/x", true},
		{"store-checks/x", true},
		{"store-checks", false},
		{"store-checksx/a", false},
		{"namespaces/ns-a/store-checks/x", false},
		{"backups/store-checks/x", false},
	}

	for _, tc := range tests {
		t.Run(tc.key.String(), func(t *testing.T) {
			t.Parallel()

			if got := blob.StoreCheckKey(tc.key); got != tc.want {
				t.Fatalf("StoreCheckKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}
