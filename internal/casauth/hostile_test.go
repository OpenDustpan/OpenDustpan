package casauth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/casauth"
	"opendustpan.org/opendustpan/internal/castest"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// The tests in this file send hostile input to one layer of the method
// each, as if every other layer had failed.

// Errors of the stubs of the tests.
var (
	errStoreDown   = errors.New("the store is down")
	errSettingsBad = errors.New("the settings cannot be read")
)

// alice is the person of the tests in this file.
func alice() sessiontest.TestPerson {
	return sessiontest.TestPerson{
		AuthTime: time.Time{}, Subject: "alice-0001", UserName: "alice", Name: "Alice Example",
		Email: "alice@example.com", Groups: nil, GroupsPresent: false, Disabled: false,
	}
}

// begin starts a sign-in of s and fails the test on an error.
func (s setup) begin(t *testing.T, fresh bool) session.Redirect {
	t.Helper()

	r, err := s.method.Begin(t.Context(), session.BeginInput{ProviderID: provider, State: "c3RhdGU", Fresh: fresh})
	if err != nil {
		t.Fatalf("Begin() = %v", err)
	}

	return r
}

// refused fails unless err is want and a is the zero Assertion.
func refused(t *testing.T, a session.Assertion, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("Finish() = %v, want %v", err, want)
	}

	if a.Subject != "" || a.ProviderID != "" || a.Method != "" || a.AuthTimeKnown || a.GroupsPresent {
		t.Fatalf("the Assertion of an error is %+v, want the zero value", a)
	}
}

// TestNewRefusesAConfigThatIsNotValid is layer 1 of the settings: New
// refuses each setting outside its allowlist, an http:// CAS URL among
// them.
func TestNewRefusesAConfigThatIsNotValid(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})

	cases := []struct {
		change func(c *casauth.Config)
		name   string
	}{
		{name: "an http CAS URL", change: func(c *casauth.Config) { c.ServerURL = "http://cas.example.com/cas" }},
		{name: "a CAS URL with a query", change: func(c *casauth.Config) { c.ServerURL += "?x=1" }},
		{name: "a CAS URL with user information", change: func(c *casauth.Config) {
			c.ServerURL = "https://u:p@cas.example.com/cas"
		}},
		{name: "a CAS URL with a fragment", change: func(c *casauth.Config) { c.ServerURL += "#f" }},
		{name: "a relative CAS URL", change: func(c *casauth.Config) { c.ServerURL = "/cas" }},
		{name: "a CAS URL of a private address", change: func(c *casauth.Config) {
			c.ServerURL, c.Rules = "https://10.0.0.1/cas", egress.Rules{Allow: nil, LoopbackForTests: false}
		}},
		{name: "a CAS URL of localhost", change: func(c *casauth.Config) {
			c.ServerURL, c.Rules = "https://localhost/cas", egress.Rules{Allow: nil, LoopbackForTests: false}
		}},
		{
			name:   "an http external URL",
			change: func(c *casauth.Config) { c.ExternalURL = "http://dustpan.example.com" },
		},
		{name: "an external URL with a query", change: func(c *casauth.Config) { c.ExternalURL += "?a=b" }},
		{name: "a provider ID that is not valid", change: func(c *casauth.Config) { c.ProviderID = "Corp/../x" }},
		{name: "an attribute with a space", change: func(c *casauth.Config) { c.SubjectAttribute = "a b" }},
		{name: "an attribute with markup", change: func(c *casauth.Config) { c.GroupsAttribute = "x><y" }},
		{name: "no ticket store", change: func(c *casauth.Config) { c.Tickets = nil }},
		{name: "no switch", change: func(c *casauth.Config) { c.Enabled = nil }},
		{name: "a negative timeout", change: func(c *casauth.Config) { c.Timeout = -time.Second }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := baseConfig(s.srv, s.tickets, s.off, s.now)
			tc.change(&cfg)

			if _, err := casauth.New(cfg); !errors.Is(err, casauth.ErrConfig) {
				t.Fatalf("New() = %v, want ErrConfig", err)
			}
		})
	}
}

// TestFinishRefusesAHostileTicket is layer 1 of the callback: a ticket
// outside the allowlist never reaches the store or the CAS server.
func TestFinishRefusesAHostileTicket(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		ticket string
	}{
		{name: "257 characters", ticket: "ST-" + strings.Repeat("a", 254)},
		{name: "a percent sign", ticket: "ST-1-a%41"},
		{name: "a proxy ticket", ticket: "PT-1-abc"},
		{name: "a query", ticket: "ST-1&pgtUrl=https://evil.example.com"},
		{name: "a NUL byte", ticket: "ST-1-a\x00"},
		{name: "no prefix", ticket: "1-abc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
			cb := s.issue(t, s.begin(t, false), alice(), castest.AnswerSuccess)
			cb.Ticket = tc.ticket

			a, err := s.method.Finish(t.Context(), cb)
			refused(t, a, err, session.ErrMethodInput)

			if s.tickets.Calls() != 0 || s.srv.Validations() != 0 {
				t.Fatalf("store calls %d and validations %d, want 0 and 0", s.tickets.Calls(), s.srv.Validations())
			}
		})
	}

	if !casauth.ValidTicket("ST-" + strings.Repeat("a", 253)) {
		t.Fatal("a ticket of 256 characters is refused, want it valid")
	}
}

// TestFinishRefusesAHostileAnswer is layer 2 of the answer: the parser
// refuses each hostile form of the answer of the CAS server.
func TestFinishRefusesAHostileAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		want   error
		name   string
		answer castest.Answer
		fresh  bool
	}{
		{name: "a DTD", answer: castest.AnswerDTD, want: session.ErrMethodAnswer, fresh: false},
		{
			name:   "an entity expansion",
			answer: castest.AnswerEntityExpansion,
			want:   session.ErrMethodAnswer,
			fresh:  false,
		},
		{
			name:   "an answer deeper than the limit",
			answer: castest.AnswerDeep,
			want:   session.ErrMethodAnswer,
			fresh:  false,
		},
		{name: "an answer over 64 KiB", answer: castest.AnswerOversized, want: session.ErrMethodAnswer, fresh: false},
		{name: "a proxy ticket", answer: castest.AnswerProxy, want: session.ErrMethodAnswer, fresh: false},
		{name: "two users", answer: castest.AnswerTwoUsers, want: session.ErrMethodAnswer, fresh: false},
		{name: "not XML", answer: castest.AnswerBroken, want: session.ErrMethodAnswer, fresh: false},
		{
			name:   "an internal error",
			answer: castest.AnswerInternalError,
			want:   session.ErrMethodUnavailable,
			fresh:  false,
		},
		{
			name: "not from a new login in a fresh sign-in", answer: castest.AnswerNotFromNewLogin,
			want: session.ErrMethodAnswer, fresh: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})

			a, err := s.method.Finish(t.Context(), s.issue(t, s.begin(t, tc.fresh), alice(), tc.answer))
			refused(t, a, err, tc.want)
		})
	}
}

// TestFinishRefusesAServiceMismatch is layer 4 of the binding: a ticket
// that the CAS server issued for another service URL, or for the service
// URL of another state, does not validate.
func TestFinishRefusesAServiceMismatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		service func(s setup) string
		name    string
	}{
		{name: "another host", service: func(setup) string { return "https://evil.example.com/callback" }},
		{name: "another state", service: func(s setup) string { return s.method.ServiceURL("b3RoZXI") }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
			r := s.begin(t, false)
			ticket := s.srv.Issue(tc.service(s), false, s.person(alice()), castest.AnswerSuccess)

			a, err := s.method.Finish(t.Context(), s.callback(t, r, loginQuery(t, r).Get("service"), ticket))
			refused(t, a, err, session.ErrMethodRejected)

			services := s.srv.Services()
			if len(services) != 1 || services[0] != s.method.ServiceURL("c3RhdGU") {
				t.Fatalf("validated services = %q, want the service URL of the method", services)
			}
		})
	}
}

// TestFinishSendsNoPGTURL is layer 3 of the request: the method never asks
// for a proxy-granting ticket.
func TestFinishSendsNoPGTURL(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
	r := s.begin(t, true)

	if _, err := s.method.Finish(t.Context(), s.browse(t, r, alice())); err != nil {
		t.Fatalf("Finish() = %v", err)
	}

	if s.srv.Validations() != 1 || s.srv.PGTURLs() != 0 {
		t.Fatalf("validations %d with pgtUrl %d, want 1 and 0", s.srv.Validations(), s.srv.PGTURLs())
	}
}

// TestFinishRefusesAReplay is layer 3 of the store: a ticket gives one
// sign-in, also when the CAS server would validate it again, and a store
// that cannot write refuses the sign-in.
func TestFinishRefusesAReplay(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
	r := s.begin(t, false)
	ticket := s.srv.Issue(loginQuery(t, r).Get("service"), false, s.person(alice()), castest.AnswerSuccess)

	// The store holds the ticket already, as after a sign-in on another
	// instance. The CAS server still holds it as unused.
	if err := s.tickets.Use(t.Context(), ticket, s.now().Add(time.Hour)); err != nil {
		t.Fatalf("Use() = %v", err)
	}

	a, err := s.method.Finish(t.Context(), s.callback(t, r, loginQuery(t, r).Get("service"), ticket))
	refused(t, a, err, session.ErrReplay)

	if n := s.srv.Validations(); n != 0 {
		t.Fatalf("%d validations of a replayed ticket, want 0", n)
	}

	s.tickets.Fail(errStoreDown)

	a, err = s.method.Finish(t.Context(), s.issue(t, r, alice(), castest.AnswerSuccess))
	refused(t, a, err, session.ErrMethodUnavailable)
}

// TestFinishRefusesARedirectToAPrivateAddress is layer 3 of the egress
// rules: the method follows no redirect of the CAS server, so a redirect
// to another address never opens a connection.
func TestFinishRefusesARedirectToAPrivateAddress(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64

	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	t.Cleanup(target.Close)

	for _, location := range []string{target.URL + "/internal", "https://10.0.0.1/", "https://169.254.169.254/"} {
		start := time.Now().Truncate(time.Millisecond)
		now := func() time.Time { return start }
		srv := castest.New(t, castest.Config{Now: now, RedirectTo: location, SlowFor: 0})
		s := setup{
			srv: srv, method: nil, tickets: sessiontest.NewMemoryTickets(now), off: &atomic.Bool{}, now: now,
			opts: options{resolve: netip.Addr{}, groups: false, subjectAttr: false},
		}

		m, err := casauth.New(baseConfig(srv, s.tickets, s.off, now))
		if err != nil {
			t.Fatalf("New() = %v", err)
		}

		t.Cleanup(m.Close)
		s.method = m

		a, err := m.Finish(t.Context(), s.issue(t, s.begin(t, false), alice(), castest.AnswerRedirect))
		refused(t, a, err, session.ErrMethodUnavailable)
	}

	if n := hits.Load(); n != 0 {
		t.Fatalf("the target of the redirect got %d requests, want 0", n)
	}
}

// TestFinishRefusesAPlainHTTPValidationURL is layer 2 of the request: a
// CAS URL that is not https, as if New had not refused it, sends no
// ticket.
func TestFinishRefusesAPlainHTTPValidationURL(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
	r := s.begin(t, false)
	cb := s.issue(t, r, alice(), castest.AnswerSuccess)

	u, err := url.Parse(strings.Replace(s.srv.URL(), "https://", "http://", 1))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	casauth.SetServerURLForTest(s.method, u)

	a, err := s.method.Finish(t.Context(), cb)
	refused(t, a, err, session.ErrMethodUnavailable)

	if n := s.srv.Validations(); n != 0 {
		t.Fatalf("%d validations over plain HTTP, want 0", n)
	}
}

// TestFinishFailsClosed is layer 5: a switch that cannot be read refuses
// the sign-in.
func TestFinishFailsClosed(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})
	cfg := baseConfig(s.srv, s.tickets, s.off, s.now)
	cfg.Enabled = func(context.Context) (bool, error) { return false, errSettingsBad }

	m, err := casauth.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	t.Cleanup(m.Close)

	_, err = m.Begin(t.Context(), session.BeginInput{ProviderID: provider, State: "c3RhdGU", Fresh: false})
	if !errors.Is(err, session.ErrMethodUnavailable) {
		t.Fatalf("Begin() = %v, want ErrMethodUnavailable", err)
	}

	a, err := m.Finish(t.Context(), s.issue(t, s.begin(t, false), alice(), castest.AnswerSuccess))
	refused(t, a, err, session.ErrMethodUnavailable)

	if n := s.srv.Validations(); n != 0 {
		t.Fatalf("%d validations, want 0", n)
	}
}

// TestServiceURL checks the form of the service URL: the callback of the
// provider under the external URL, with the state as its only parameter.
func TestServiceURL(t *testing.T) {
	t.Parallel()

	s := newSetup(t, options{resolve: netip.Addr{}, groups: false, subjectAttr: false})

	cases := []struct {
		state string
		want  string
	}{
		{state: "c3RhdGU", want: externalURL + "/api/server/sign-in/corp/callback?state=c3RhdGU"},
		{state: "a&ticket=ST-1", want: externalURL + "/api/server/sign-in/corp/callback?state=a%26ticket%3DST-1"},
	}

	for _, tc := range cases {
		if got := s.method.ServiceURL(tc.state); got != tc.want {
			t.Errorf("ServiceURL(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
}
