package fs_test

import (
	"testing"

	"opendustpan.org/opendustpan/blob"
	"opendustpan.org/opendustpan/blob/blobtest"
	"opendustpan.org/opendustpan/blob/fs"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	blobtest.Run(t, func(t *testing.T) blob.Store {
		t.Helper()

		s, err := fs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return s
	})
}
