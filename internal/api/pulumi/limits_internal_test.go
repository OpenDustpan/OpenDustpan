package pulumi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReservation checks that a body reserves its declared length, scaled for
// gzip and capped by the limit, and the whole limit when the length is
// unknown.
func TestReservation(t *testing.T) {
	t.Parallel()

	const limit = 1 << 20

	tests := []struct {
		name     string
		length   int64
		encoding string
		want     int64
	}{
		{name: "small plain body", length: 1024, encoding: "", want: 1024},
		{name: "small gzip body", length: 1024, encoding: "gzip", want: 1024 * gzipExpansion},
		{name: "gzip header in upper case", length: 1024, encoding: "GZIP", want: 1024 * gzipExpansion},
		{name: "large plain body", length: 4 << 20, encoding: "", want: limit},
		{name: "large gzip body", length: 512 << 10, encoding: "gzip", want: limit},
		{name: "unknown length", length: -1, encoding: "", want: limit},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(""))
			r.ContentLength = tc.length

			if tc.encoding != "" {
				r.Header.Set("Content-Encoding", tc.encoding)
			}

			if got := reservation(r, limit); got != tc.want {
				t.Fatalf("reservation = %d, want %d", got, tc.want)
			}
		})
	}
}
