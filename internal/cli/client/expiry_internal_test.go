package client

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// TestExpiryWarningCountsStartedDays proves that the warning counts started
// days left: 23 hours left prints "in 1 days", and 24 hours and 1 ms left
// prints "in 2 days".
func TestExpiryWarningCountsStartedDays(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		left  time.Duration
		count string
	}{
		{"23h_left_warns_1", 23 * time.Hour, "in 1 days"},
		{"24h_and_1ms_left_warns_2", 24*time.Hour + time.Millisecond, "in 2 days"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			out := warnOnce(t, now, tc.left)
			if !strings.Contains(out, tc.count) {
				t.Fatalf("warning = %q, want it to hold %q", out, tc.count)
			}
		})
	}
}

// warnOnce runs one request through the expiry warner with the clock fixed
// at now and answers what the warner wrote.
func warnOnce(t *testing.T, now time.Time, left time.Duration) string {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(accesstoken.ExpiryHeader, now.Add(left).Format(time.RFC3339Nano))
	}))
	t.Cleanup(server.Close)

	var out bytes.Buffer
	c := warnOnExpiry(server.Client(), &out)
	warner, ok := c.Transport.(*expiryWarner)
	if !ok {
		t.Fatalf("transport is %T, want *expiryWarner", c.Transport)
	}
	warner.now = func() time.Time { return now }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	return out.String()
}
