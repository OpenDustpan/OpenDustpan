package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
)

// TestBreakGlassDisableInMaintenanceMode proves that a disable works while the
// store is in maintenance mode, as a revocation does, when another enabled
// break-glass account stays. A disable that leaves no enabled break-glass
// account is still refused with 409, keeps the account enabled and writes one
// denied entry.
func TestBreakGlassDisableInMaintenanceMode(t *testing.T) {
	t.Parallel()

	cases := []floorCase{
		{
			name:       "X disables A while B is enabled",
			method:     http.MethodPost,
			caller:     "x-token",
			second:     true,
			auditFails: false,
			wantRefuse: false,
		},
		{
			name:       "X disables the last enabled account",
			method:     http.MethodPost,
			caller:     "x-token",
			second:     false,
			auditFails: false,
			wantRefuse: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc, st := floorStore(t)
			seedFloorCase(t, st, tc)

			if err := st.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
				t.Fatalf("enter maintenance mode: %v", err)
			}

			h, _, _ := floorHandler(t, svc, st, tc)

			status, body := floorCall(t, h, tc.method, floorPath(tc.method, floorIDA), tc.caller)

			if tc.wantRefuse {
				// assertFloorRefused also checks the one denied entry.
				assertFloorRefused(t, h, st, tc, floorResult{body: body, status: status})

				return
			}

			assertDisabledInMaintenance(t, h, tc, status, body)
		})
	}
}

// assertDisabledInMaintenance fails unless the disable answered 200 and A
// is disabled.
func assertDisabledInMaintenance(t *testing.T, h testHandler, tc floorCase, status int, body string) {
	t.Helper()

	if status != http.StatusOK {
		t.Fatalf("%s answers %d %q in maintenance mode, want 200", tc.name, status, body)
	}

	if got := floorGet(t, h, floorIDA, tc.caller); got.DisabledAt == nil {
		t.Fatalf("%s left A enabled, want A disabled", tc.name)
	}
}
