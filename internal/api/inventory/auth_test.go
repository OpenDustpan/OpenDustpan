package inventory_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUnitListAcceptsBothSchemes proves the inventory API reads a token under
// the scheme of the console ("token") and under the scheme of the TFE-compatible
// and installation APIs ("Bearer"). Any other scheme is unauthenticated.
func TestUnitListAcceptsBothSchemes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{name: "token scheme", header: "token test-token", want: http.StatusOK},
		{name: "Bearer scheme", header: "Bearer test-token", want: http.StatusOK},
		{name: "wrong token", header: "Bearer other-token", want: http.StatusUnauthorized},
		{name: "basic scheme", header: "Basic dGVzdA==", want: http.StatusUnauthorized},
		{name: "no header", header: "", want: http.StatusUnauthorized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := newSeededHandler(t, selectiveAuthorizer{deny: nil})

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
				"/api/dustpan/v1/namespaces/acme/units", http.NoBody)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
