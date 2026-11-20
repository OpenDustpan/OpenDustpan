package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestRestoreSwapRetryAfterKeep runs the retry case of the store suite: a
// swap fails after its keeps and the retry decides another way.
func TestRestoreSwapRetryAfterKeep(t *testing.T) {
	t.Parallel()

	for _, tc := range storetest.SwapRetryCases() {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			s, err := Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
			if err != nil {
				t.Fatalf("open store: %v", err)
			}

			t.Cleanup(func() {
				if closeErr := s.Close(); closeErr != nil {
					t.Errorf("close store: %v", closeErr)
				}
			})

			storetest.CheckSwapRetryAfterKeep(t, s, s.SettingStore(), func(hook func(context.Context) error) {
				s.swapAfterKeepHook = hook
			}, tc)
		})
	}
}
