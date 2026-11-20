package postgres

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestRestoreSwapRetryAfterKeep runs the retry case of the store suite: a
// swap fails after its keeps and the retry decides another way. The swap is
// one transaction, so the failure rolls back the keeps of the first attempt.
func TestRestoreSwapRetryAfterKeep(t *testing.T) {
	t.Parallel()

	for _, tc := range storetest.SwapRetryCases() {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()

			s := openAt(t, membershipURL(t), "")

			storetest.CheckSwapRetryAfterKeep(t, s, s.SettingStore(), func(hook func(context.Context) error) {
				s.swapAfterKeepHook = hook
			}, tc)
		})
	}
}
