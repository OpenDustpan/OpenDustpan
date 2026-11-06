package casauth_test

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/casauth"
	"opendustpan.org/opendustpan/internal/castest"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// Values of the methods of the tests.
const (
	provider    = "corp"
	externalURL = "https://dustpan.example.com"
	subjectAttr = "employeeNumber"
	testTimeout = 500 * time.Millisecond
)

// options select the settings of a test method.
type options struct {
	// resolve, when valid, makes the CAS URL a host name that resolves to
	// it, with the egress rules that have no exception for the loopback.
	resolve netip.Addr
	// groups turns the groups attribute on.
	groups bool
	// subjectAttr reads the subject from an attribute.
	subjectAttr bool
}

// setup is a test CAS server and a method over it.
type setup struct {
	srv     *castest.Server
	method  *casauth.Method
	tickets *sessiontest.MemoryTickets
	off     *atomic.Bool
	now     func() time.Time
	opts    options
}

// newSetup starts a CAS server and makes a method over it.
func newSetup(t *testing.T, opts options) setup {
	t.Helper()

	start := time.Now().Truncate(time.Millisecond)
	now := func() time.Time { return start }
	srv := castest.New(t, castest.Config{Now: now, RedirectTo: "", SlowFor: 0})
	tickets := sessiontest.NewMemoryTickets(now)
	off := &atomic.Bool{}

	cfg := baseConfig(srv, tickets, off, now)
	if opts.groups {
		cfg.GroupsAttribute = "memberOf"
	}

	if opts.subjectAttr {
		cfg.SubjectAttribute = subjectAttr
	}

	if opts.resolve.IsValid() {
		cfg.ServerURL = "https://cas.example.com:" + srv.Port() + castest.BasePath
		cfg.Resolver = castest.Resolver(t, []netip.Addr{opts.resolve})
		cfg.Rules = egress.Rules{Allow: nil, LoopbackForTests: false}
	}

	m, err := casauth.New(cfg)
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	t.Cleanup(m.Close)

	return setup{srv: srv, method: m, tickets: tickets, off: off, now: now, opts: opts}
}

// baseConfig returns the configuration of a method over srv.
func baseConfig(srv *castest.Server, tickets session.UsedTickets, off *atomic.Bool, now func() time.Time,
) casauth.Config {
	return casauth.Config{
		Enabled: func(context.Context) (bool, error) { return !off.Load(), nil },
		Tickets: tickets, Now: now, RootCAs: srv.RootCAs(), Resolver: nil, ServerURL: srv.URL(),
		ExternalURL: externalURL, ProviderID: provider, SubjectAttribute: "", UserNameAttribute: "uid",
		NameAttribute: "displayName", EmailAttribute: "mail", GroupsAttribute: "",
		Rules: egress.Rules{Allow: nil, LoopbackForTests: true}, Timeout: testTimeout,
	}
}

// person returns the person of the CAS server for p.
func (s setup) person(p sessiontest.TestPerson) castest.Person {
	attrs := map[string][]string{"uid": {p.UserName}, "displayName": {p.Name}, "mail": {p.Email}}
	user := p.Subject

	if s.opts.subjectAttr {
		user = p.UserName
		attrs[subjectAttr] = []string{p.Subject}
	}

	if p.GroupsPresent {
		attrs["memberOf"] = p.Groups
		if len(p.Groups) == 0 {
			attrs["memberOf"] = []string{""}
		}
	}

	return castest.Person{AuthTime: p.AuthTime, Attributes: attrs, User: user}
}

// issue issues a ticket for the service and renew of r, with the answer a,
// and returns the callback that the CAS server redirects the browser to.
func (s setup) issue(t *testing.T, r session.Redirect, p sessiontest.TestPerson, a castest.Answer,
) session.Callback {
	t.Helper()

	q := loginQuery(t, r)

	return s.callback(t, r, q.Get("service"), s.srv.Issue(q.Get("service"), q.Get("renew") == "true", s.person(p), a))
}

// callback returns the callback of the service URL service and the ticket,
// as the HTTP adapter fills it.
func (s setup) callback(t *testing.T, r session.Redirect, service, ticket string) session.Callback {
	t.Helper()

	u, err := url.Parse(service)
	if err != nil {
		t.Fatalf("parse service: %v", err)
	}

	pending := r.Pending

	return session.Callback{
		Pending: &pending, State: u.Query().Get("state"), Code: "", Ticket: ticket, ProviderError: "",
		SAMLResponse: nil,
	}
}

// browse sends the browser to the login URL of r and returns the callback
// of the redirect back to the server.
func (s setup) browse(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
	t.Helper()

	s.srv.SetLogin(s.person(p), castest.AnswerSuccess)

	browser := *s.srv.Client()
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, r.URL, http.NoBody)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}

	resp, err := browser.Do(req)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}

	back, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || resp.StatusCode != http.StatusFound {
		t.Fatalf("login answer = %d, %v", resp.StatusCode, err)
	}

	ticket := back.Query().Get("ticket")
	back.RawQuery = url.Values{"state": {back.Query().Get("state")}}.Encode()

	return s.callback(t, r, back.String(), ticket)
}

// loginQuery returns the query of the login URL of r.
func loginQuery(t *testing.T, r session.Redirect) url.Values {
	t.Helper()

	u, err := url.Parse(r.URL)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}

	return u.Query()
}

// serverAnswer returns the answer of the server for the answer a of the
// suite.
func serverAnswer(t *testing.T, a sessiontest.CASAnswer) castest.Answer {
	t.Helper()

	switch a {
	case sessiontest.CASAnswerNoAuthenticationDate:
		return castest.AnswerNoAuthenticationDate
	case sessiontest.CASAnswerDTD:
		return castest.AnswerDTD
	case sessiontest.CASAnswerDeep:
		return castest.AnswerDeep
	case sessiontest.CASAnswerSlow:
		return castest.AnswerSlow
	default:
		t.Fatalf("unknown answer %d", a)

		return castest.AnswerBroken
	}
}

// fixture returns the fixture of the suite over s.
func (s setup) fixture() sessiontest.RedirectFixture {
	answer := s.browse
	if s.opts.resolve.IsValid() {
		// The browser of the test cannot resolve the host name of the
		// method, so the server issues the ticket directly.
		answer = func(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
			t.Helper()

			return s.issue(t, r, p, castest.AnswerSuccess)
		}
	}

	return sessiontest.RedirectFixture{
		Method: s.method,
		Answer: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
			t.Helper()

			return answer(t, r, p)
		},
		Broken: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson) session.Callback {
			t.Helper()

			return s.issue(t, r, p, castest.AnswerBroken)
		},
		AsksFresh: func(t *testing.T, r session.Redirect) bool {
			t.Helper()

			return loginQuery(t, r).Get("renew") == "true"
		},
		Stop:       func(*testing.T) { s.srv.Close() },
		Disable:    func(*testing.T) { s.off.Store(true) },
		Now:        s.now,
		ProviderID: provider, AuthTime: true, Groups: s.opts.groups, ReportsDisabled: false,
		CAS: &sessiontest.CASFixture{
			AnswerWith: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson,
				a sessiontest.CASAnswer,
			) session.Callback {
				t.Helper()

				return s.issue(t, r, p, serverAnswer(t, a))
			},
			AnswerForService: func(t *testing.T, r session.Redirect, p sessiontest.TestPerson,
				service string,
			) session.Callback {
				t.Helper()

				q := loginQuery(t, r)
				ticket := s.srv.Issue(service, q.Get("renew") == "true", s.person(p), castest.AnswerSuccess)

				return s.callback(t, r, q.Get("service"), ticket)
			},
			WithSubjectAttribute: func(t *testing.T) sessiontest.RedirectFixture {
				t.Helper()

				o := s.opts
				o.subjectAttr = true

				return newSetup(t, o).fixture()
			},
			WithResolvedAddress: func(t *testing.T, addr netip.Addr) sessiontest.RedirectFixture {
				t.Helper()

				o := s.opts
				o.resolve = addr

				return newSetup(t, o).fixture()
			},
			Validations: s.srv.Validations,
			ExternalURL: externalURL,
		},
	}
}

// TestMethodConformance runs the suite of session.RedirectMethod on the
// CAS method, with groups on and with groups off.
func TestMethodConformance(t *testing.T) {
	t.Parallel()

	for name, groups := range map[string]bool{"groups": true, "no groups": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sessiontest.RunRedirectMethod(t, func(t *testing.T) sessiontest.RedirectFixture {
				t.Helper()

				return newSetup(t, options{resolve: netip.Addr{}, groups: groups, subjectAttr: false}).fixture()
			})
		})
	}
}
