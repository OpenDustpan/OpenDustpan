package backup_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup"
)

func TestCheckUnverifiedAccepts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		staged []string
		listed []string
	}{
		{"nothing staged", nil, nil},
		{"every staged key is listed", []string{"a", "b"}, []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := backup.CheckUnverified(tt.staged, tt.listed); err != nil {
				t.Fatalf("CheckUnverified = %v, want nil", err)
			}
		})
	}
}

func TestCheckUnverifiedRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		staged    []string
		listed    []string
		wantCount int
		wantFirst string
	}{
		{"a key that the manifest does not list", []string{"a", "b"}, []string{"a"}, 1, "b"},
		{"no list in the manifest", []string{"x", "y", "z"}, nil, 3, "x"},
		{"the first missing key in order", []string{"a", "c", "d"}, []string{"a", "d"}, 1, "c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := backup.CheckUnverified(tt.staged, tt.listed)

			notBacked, ok := errors.AsType[*backup.BlobsNotBackedError](err)
			if !ok || notBacked.Count != tt.wantCount || notBacked.First != tt.wantFirst {
				t.Fatalf("CheckUnverified = %v, want %d keys, first %q", err, tt.wantCount, tt.wantFirst)
			}

			if !errors.Is(err, backup.ErrBlobsNotBacked) || !errors.Is(err, backup.ErrCorrupt) {
				t.Errorf("CheckUnverified = %v, want it to match ErrBlobsNotBacked and ErrCorrupt", err)
			}

			if !strings.HasPrefix(err.Error(), backup.BlobsNotBackedText) {
				t.Errorf("error text %q does not start with %q", err.Error(), backup.BlobsNotBackedText)
			}
		})
	}
}
