package castest_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/castest"
)

// validate sends a validation request with the query q and returns the
// answer.
func validate(t *testing.T, s *castest.Server, q url.Values) string {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		s.URL()+"/p3/serviceValidate?"+q.Encode(), http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	b, err := io.ReadAll(resp.Body)
	if cerr := resp.Body.Close(); cerr != nil {
		t.Errorf("close body: %v", cerr)
	}

	if err != nil {
		t.Fatalf("read: %v", err)
	}

	return string(b)
}

// TestServerValidatesATicketOnce checks the rules of the test server: a
// ticket is valid once, for its service, and with renew only for a login
// with renew.
func TestServerValidatesATicketOnce(t *testing.T) {
	t.Parallel()

	const service = "https://dustpan.example.com/callback?state=s"

	cases := []struct {
		query func(ticket string) url.Values
		name  string
		want  string
		renew bool
	}{
		{
			name: "a valid ticket", want: "<cas:user>alice</cas:user>", renew: false,
			query: func(tk string) url.Values { return url.Values{"service": {service}, "ticket": {tk}} },
		},
		{
			name: "another service", want: `code="INVALID_SERVICE"`, renew: false,
			query: func(tk string) url.Values {
				return url.Values{"service": {"https://evil.example.com/"}, "ticket": {tk}}
			},
		},
		{
			name: "renew for a login without renew", want: `code="INVALID_TICKET"`, renew: false,
			query: func(tk string) url.Values {
				return url.Values{"service": {service}, "ticket": {tk}, "renew": {"true"}}
			},
		},
		{
			name: "an unknown ticket", want: `code="INVALID_TICKET"`, renew: true,
			query: func(string) url.Values { return url.Values{"service": {service}, "ticket": {"ST-0-none"}} },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := castest.New(t, castest.Config{Now: nil, RedirectTo: "", SlowFor: 0})
			p := castest.Person{AuthTime: time.Time{}, User: "alice", Attributes: nil}
			tk := s.Issue(service, tc.renew, p, castest.AnswerSuccess)

			if got := validate(t, s, tc.query(tk)); !strings.Contains(got, tc.want) {
				t.Fatalf("answer = %s, want %s", got, tc.want)
			}

			// The first validation used the ticket, so the same query
			// never succeeds again.
			if got := validate(t, s, tc.query(tk)); strings.Contains(got, "authenticationSuccess") {
				t.Fatalf("second answer = %s, want a failure", got)
			}
		})
	}
}
