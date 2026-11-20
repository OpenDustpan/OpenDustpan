package dustpan

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

// stateRouteTarget is the masked read of the stack web/dev in the namespace
// acme, with each slash of the unit reference escaped.
const stateRouteTarget = "/api/dustpan/v1/units/pulumi:acme%2Fweb%2Fdev/state"

// TestStateRouteIsMounted proves the server mounts the masked read of the
// state of a unit: a request with no credential reaches the adapter and gets
// its 401, and a request with an access token reaches the read and gets the
// 404 of a unit that does not exist. A route that the server does not mount
// answers the 404 of an unknown path for both.
func TestStateRouteIsMounted(t *testing.T) {
	t.Parallel()

	h := newMuxServer(t, AllComponents()).ungated
	target := "http://example.invalid" + stateRouteTarget

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no credential: status %d (%s), want 401 from the adapter", rec.Code, rec.Body.Bytes())
	}

	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	req.Header.Set("Authorization", "Bearer "+muxTestToken)

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var body struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.Bytes(), err)
	}

	if rec.Code != http.StatusNotFound || body.ErrorCode != errcode.UnitNotFound {
		t.Fatalf("access token: answer %d %q, want 404 %q from the read",
			rec.Code, body.ErrorCode, errcode.UnitNotFound)
	}
}
