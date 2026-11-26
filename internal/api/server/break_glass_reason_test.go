package server_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestBreakGlassAddCapsTransportInReason sends a transport of 300 characters
// to the add. The denied entry keeps at most 32 characters of the value, not
// the request text, so a long value cannot fill the audit reason.
func TestBreakGlassAddCapsTransportInReason(t *testing.T) {
	t.Parallel()

	f := newAccessFixture(t)
	f.seedSessionAdmin(t, "admin")

	long := strings.Repeat("x", 300)
	body := breakGlassBody(t, map[string]any{
		"subject": "break-glass:recovery", "transports": []string{long},
	})
	rec := f.freshPasskeyClient(t, "admin").do(http.MethodPost, breakGlassPath, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("add = %d %s, want 400", rec.Code, rec.Body.String())
	}

	denied := floorEntries(t, f.store, identity.ActionServerManagePrincipals, audit.OutcomeDenied, "break-glass")
	if len(denied) == 0 {
		t.Fatal("denied entries on break-glass = 0, want one with a reason")
	}

	reason := denied[len(denied)-1].Reason
	if strings.Contains(reason, strings.Repeat("x", 33)) {
		t.Fatalf("reason = %q, want at most 32 characters of the transport value", reason)
	}
}
