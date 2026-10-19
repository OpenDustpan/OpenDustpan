package tofuhttp_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestLockRefusedByAManualLock proves a LOCK that a manual lock refuses answers
// 423 with lock info that names the holder in Who and the reason in Info, and
// that it does so for a unit with no tool lock of its own. A unit that the
// service cannot lock for another reason keeps its answer.
func TestLockRefusedByAManualLock(t *testing.T) {
	t.Parallel()

	held := manuallock.Lock{
		Since:  time.Date(2026, time.October, 19, 10, 0, 0, 0, time.UTC),
		Target: manuallock.CollectionTarget(collection.Ref{Namespace: "acme", Name: "payments"}),
		Holder: "alice",
		Reason: "change window closed",
	}

	manual := fmt.Errorf("%w: %w", workspace.ErrLocked, &manuallock.LockedError{Lock: held})

	cases := []struct {
		name     string
		lockErr  error
		want     int
		wantWho  string
		wantInfo string
	}{
		{
			name: "manual lock", lockErr: manual, want: http.StatusLocked,
			wantWho: "alice", wantInfo: held.String(),
		},
		{
			name: "lock of a tool without a lock to show", lockErr: workspace.ErrLocked, want: http.StatusLocked,
			wantWho: "", wantInfo: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			units := newFakeUnits(true)
			units.lockErr = tc.lockErr
			h := newTestHandler(t, units)

			rec := do(h, "LOCK", unitPath, lockBody(lockOne, "ci"), bearer(adminToken))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}

			if tc.wantWho == "" {
				return
			}

			var info struct {
				Who  string `json:"Who"`  //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
				Info string `json:"Info"` //nolint:tagliatelle // The OpenTofu http backend reads PascalCase keys.
			}

			if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
				t.Fatalf("lock body %q: %v", rec.Body.String(), err)
			}

			if info.Who != tc.wantWho || info.Info != tc.wantInfo {
				t.Errorf(
					"lock info = Who %q Info %q, want Who %q Info %q",
					info.Who,
					info.Info,
					tc.wantWho,
					tc.wantInfo,
				)
			}
		})
	}
}
