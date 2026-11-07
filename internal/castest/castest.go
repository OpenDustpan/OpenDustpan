// Package castest is an in-process CAS 3.0 server for tests. It serves the
// login endpoint and the service ticket validation endpoint over TLS. A
// test can issue a ticket directly, set the form of the validation answer
// of each ticket, and count the validation requests. The binary never
// links it.
package castest

import (
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BasePath is the path of the CAS server under its host.
const BasePath = "/cas"

// Sizes of the hostile answers and the delay of a slow answer.
const (
	// defaultSlowFor is the delay of AnswerSlow when Config.SlowFor is
	// zero.
	defaultSlowFor = 30 * time.Second
	// deepNesting is the nesting of AnswerDeep.
	deepNesting = 100
	// oversizedBytes is the padding of AnswerOversized.
	oversizedBytes = 70 << 10
	// entityLevels and entityFanOut shape the entities of
	// AnswerEntityExpansion.
	entityLevels = 9
	entityFanOut = 10
	// rootOpen and rootClose enclose each answer. The namespace is the one
	// of the CAS protocol.
	rootOpen  = `<cas:serviceResponse xmlns:cas="http://www.yale.edu/tp/cas">`
	rootClose = `</cas:serviceResponse>`
)

// Answer is the form of the validation answer of a ticket.
type Answer int

// Forms of a validation answer.
const (
	// AnswerSuccess is a valid success answer with the attributes of the
	// person and the attribute authenticationDate.
	AnswerSuccess Answer = iota
	// AnswerNoAuthenticationDate is a success answer with no attribute
	// authenticationDate.
	AnswerNoAuthenticationDate
	// AnswerNotFromNewLogin is a success answer with the attribute
	// isFromNewLogin set to false.
	AnswerNotFromNewLogin
	// AnswerDTD is a success answer with a document type declaration and
	// an entity that holds the user.
	AnswerDTD
	// AnswerEntityExpansion is a success answer with nested entities that
	// expand to a very large text.
	AnswerEntityExpansion
	// AnswerDeep is a success answer whose attributes nest deeper than 64
	// elements.
	AnswerDeep
	// AnswerOversized is a success answer of more than 64 KiB.
	AnswerOversized
	// AnswerProxy is a success answer of a proxy ticket: it holds a list of
	// proxies and a proxy-granting ticket.
	AnswerProxy
	// AnswerSlow is a success answer that the server sends after the time
	// of Config.SlowFor, or never when the request ends first.
	AnswerSlow
	// AnswerBroken is an answer that is not a CAS service response.
	AnswerBroken
	// AnswerRedirect is a redirect to Config.RedirectTo.
	AnswerRedirect
	// AnswerTwoUsers is a success answer with two user elements.
	AnswerTwoUsers
	// AnswerInternalError is a failure answer with the code INTERNAL_ERROR.
	AnswerInternalError
)

// Person is a person at the CAS server.
type Person struct {
	// AuthTime is the time of the authentication that the server sends in
	// the attribute authenticationDate. The zero time uses the clock of the
	// server.
	AuthTime time.Time
	// Attributes are the attributes of the person, by name.
	Attributes map[string][]string
	// User is the CAS user.
	User string
}

// Config configures a Server.
type Config struct {
	// Now is the clock of the server. Nil uses time.Now.
	Now func() time.Time
	// RedirectTo is the Location of the answer AnswerRedirect.
	RedirectTo string
	// SlowFor is the delay of the answer AnswerSlow. Zero uses 30 seconds.
	SlowFor time.Duration
}

// ticket is an issued service ticket.
type ticket struct {
	person  Person
	service string
	answer  Answer
	renew   bool
}

// Server is a CAS 3.0 server. It is safe for concurrent use.
type Server struct {
	srv         *httptest.Server
	now         func() time.Time
	tickets     map[string]ticket
	login       Person
	redirectTo  string
	services    []string
	slowFor     time.Duration
	validations atomic.Int64
	pgtURLs     atomic.Int64
	seq         atomic.Int64
	mu          sync.Mutex
	loginAnswer Answer
}

// New starts a Server over TLS. The test cleanup closes it.
func New(t *testing.T, cfg Config) *Server {
	t.Helper()

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	slow := cfg.SlowFor
	if slow == 0 {
		slow = defaultSlowFor
	}

	s := &Server{
		srv:         nil,
		now:         now,
		tickets:     map[string]ticket{},
		login:       Person{AuthTime: time.Time{}, Attributes: nil, User: ""},
		redirectTo:  cfg.RedirectTo,
		services:    nil,
		slowFor:     slow,
		validations: atomic.Int64{},
		pgtURLs:     atomic.Int64{},
		seq:         atomic.Int64{},
		mu:          sync.Mutex{},
		loginAnswer: AnswerSuccess,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+BasePath+"/login", s.serveLogin)
	mux.HandleFunc("GET "+BasePath+"/p3/serviceValidate", s.serveValidate)

	s.srv = httptest.NewTLSServer(mux)
	t.Cleanup(s.Close)

	return s
}

// URL returns the base URL of the CAS server, for example
// https://127.0.0.1:1234/cas.
func (s *Server) URL() string {
	return s.srv.URL + BasePath
}

// Port returns the TCP port of the server.
func (s *Server) Port() string {
	u, err := url.Parse(s.srv.URL)
	if err != nil {
		return ""
	}

	return u.Port()
}

// RootCAs returns a pool that holds the certificate of the server. The
// certificate is valid for 127.0.0.1, ::1, example.com and *.example.com.
func (s *Server) RootCAs() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(s.srv.Certificate())

	return pool
}

// Client returns a client that trusts the server, as a browser would.
func (s *Server) Client() *http.Client {
	return s.srv.Client()
}

// Close stops the server. A request after Close fails.
func (s *Server) Close() {
	s.srv.Close()
}

// SetLogin sets the person who signs in at the login endpoint, and the
// form of the validation answer of the tickets that it issues.
func (s *Server) SetLogin(p Person, a Answer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.login, s.loginAnswer = p, a
}

// Issue returns a new service ticket of p for the service URL service, as
// the login endpoint issues it. renew marks a ticket of a login that asked
// for a new authentication. Its validation answer has the form a.
func (s *Server) Issue(service string, renew bool, p Person, a Answer) string {
	var b [24]byte

	// crypto/rand.Read never returns an error.
	_, _ = rand.Read(b[:])
	id := "ST-" + strconv.FormatInt(s.seq.Add(1), 10) + "-" + base64.RawURLEncoding.EncodeToString(b[:])

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tickets[id] = ticket{person: p, service: service, answer: a, renew: renew}

	return id
}

// Validations returns the count of the validation requests that reached
// the server.
func (s *Server) Validations() int64 {
	return s.validations.Load()
}

// PGTURLs returns the count of the validation requests that held a
// pgtUrl parameter.
func (s *Server) PGTURLs() int64 {
	return s.pgtURLs.Load()
}

// Services returns the service URLs of the validation requests, in order.
func (s *Server) Services() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.services)
}

// serveLogin issues a ticket to the person of SetLogin for the service of
// the request. A CAS server redirects the browser to the service with the
// ticket. The test server writes only the ticket in the body of a 200
// answer instead, so that it never sends a browser to, and never echoes, a
// value that a request gives. A test adds the ticket to the service URL of
// its login URL, as a browser follows the redirect.
func (s *Server) serveLogin(w http.ResponseWriter, r *http.Request) {
	service := r.URL.Query().Get("service")

	back, err := url.Parse(service)
	if service == "" || err != nil || !back.IsAbs() {
		http.Error(w, "the service is not valid", http.StatusBadRequest)

		return
	}

	s.mu.Lock()
	p, a := s.login, s.loginAnswer
	s.mu.Unlock()

	ticket := s.Issue(service, r.URL.Query().Get("renew") == "true", p, a)

	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	write(w, ticket)
}

// serveValidate answers a service ticket validation of CAS 3.0. A ticket
// is valid once, for its service, and with renew only for a ticket of a
// login with renew.
func (s *Server) serveValidate(w http.ResponseWriter, r *http.Request) {
	s.validations.Add(1)

	q := r.URL.Query()
	if q.Has("pgtUrl") {
		s.pgtURLs.Add(1)
	}

	s.mu.Lock()
	s.services = append(s.services, q.Get("service"))
	tk, ok := s.tickets[q.Get("ticket")]
	delete(s.tickets, q.Get("ticket"))
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml; charset=UTF-8")

	switch {
	case q.Get("service") == "" || q.Get("ticket") == "":
		writeFailure(w, "INVALID_REQUEST")
	case !ok:
		writeFailure(w, "INVALID_TICKET")
	case tk.service != q.Get("service"):
		writeFailure(w, "INVALID_SERVICE")
	case q.Get("renew") == "true" && !tk.renew:
		writeFailure(w, "INVALID_TICKET")
	default:
		s.answer(w, r, tk)
	}
}

// answer writes the answer of the form of tk.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, tk ticket) {
	switch tk.answer {
	case AnswerSlow:
		select {
		case <-r.Context().Done():
			return
		case <-time.After(s.slowFor):
		}

		write(w, s.successXML(tk))
	case AnswerRedirect:
		w.Header().Set("Location", s.redirectTo)
		w.WriteHeader(http.StatusFound)
	case AnswerBroken:
		write(w, `{"serviceResponse": "not XML"}`)
	case AnswerInternalError:
		writeFailure(w, "INTERNAL_ERROR")
	case AnswerSuccess, AnswerNoAuthenticationDate, AnswerNotFromNewLogin, AnswerDTD, AnswerEntityExpansion,
		AnswerDeep, AnswerOversized, AnswerProxy, AnswerTwoUsers:
		write(w, s.successXML(tk))
	default:
		writeFailure(w, "UNKNOWN_FORM")
	}
}

// successXML returns the success answer of tk in its form.
func (s *Server) successXML(tk ticket) string {
	parts := userXML(tk)

	return parts.prolog + rootOpen + "<cas:authenticationSuccess>" + parts.user + "<cas:attributes>" +
		attributesXML(s.attributes(tk)) + extraAttributesXML(tk.answer) + "</cas:attributes>" +
		proxyXML(tk.answer) + "</cas:authenticationSuccess>" + rootClose
}

// attributes returns the attributes of the answer of tk: the attributes of
// the person, the time of the authentication unless the form omits it, and
// whether the ticket comes from a new login.
func (s *Server) attributes(tk ticket) map[string][]string {
	attrs := map[string][]string{}
	for k, v := range tk.person.Attributes {
		attrs[k] = slices.Clone(v)
	}

	authTime := tk.person.AuthTime
	if authTime.IsZero() {
		authTime = s.now()
	}

	if tk.answer != AnswerNoAuthenticationDate {
		attrs["authenticationDate"] = []string{authTime.UTC().Format(time.RFC3339Nano)}
	}

	attrs["isFromNewLogin"] = []string{strconv.FormatBool(tk.renew && tk.answer != AnswerNotFromNewLogin)}

	return attrs
}

// userParts are the prolog and the user elements of an answer.
type userParts struct {
	prolog string
	user   string
}

// userXML returns the prolog and the user elements of the answer of tk.
// An unknown form has no user.
func userXML(tk ticket) userParts {
	plain := "<cas:user>" + escape(tk.person.User) + "</cas:user>"

	switch tk.answer {
	case AnswerDTD:
		return userParts{
			prolog: `<?xml version="1.0"?><!DOCTYPE cas:serviceResponse [<!ENTITY who "` +
				escape(tk.person.User) + `">]>`,
			user: "<cas:user>&who;</cas:user>",
		}
	case AnswerEntityExpansion:
		var b strings.Builder

		b.WriteString(`<?xml version="1.0"?><!DOCTYPE lolz [<!ENTITY lol "lol">`)

		prev := "lol"
		for i := range entityLevels {
			name := "lol" + strconv.Itoa(i)
			b.WriteString(`<!ENTITY ` + name + ` "` + strings.Repeat("&"+prev+";", entityFanOut) + `">`)
			prev = name
		}

		b.WriteString(`]>`)

		return userParts{prolog: b.String(), user: "<cas:user>&" + prev + ";</cas:user>"}
	case AnswerTwoUsers:
		return userParts{prolog: "", user: plain + "<cas:user>mallory</cas:user>"}
	case AnswerSuccess, AnswerNoAuthenticationDate, AnswerNotFromNewLogin, AnswerDeep, AnswerOversized,
		AnswerProxy, AnswerSlow, AnswerBroken, AnswerRedirect, AnswerInternalError:
		return userParts{prolog: "", user: plain}
	default:
		return userParts{prolog: "", user: ""}
	}
}

// attributesXML returns the elements of attrs, in the order of the names.
func attributesXML(attrs map[string][]string) string {
	names := make([]string, 0, len(attrs))
	for k := range attrs {
		names = append(names, k)
	}

	slices.Sort(names)

	var b strings.Builder

	for _, k := range names {
		for _, v := range attrs[k] {
			b.WriteString("<cas:" + k + ">" + escape(v) + "</cas:" + k + ">")
		}
	}

	return b.String()
}

// extraAttributesXML returns the hostile content of the attributes of the
// form a, or nothing.
func extraAttributesXML(a Answer) string {
	if a == AnswerDeep {
		return strings.Repeat("<cas:deep>", deepNesting) + strings.Repeat("</cas:deep>", deepNesting)
	}

	if a == AnswerOversized {
		return "<cas:padding>" + strings.Repeat("p", oversizedBytes) + "</cas:padding>"
	}

	return ""
}

// proxyXML returns the elements of a proxy ticket for the form
// AnswerProxy, or nothing.
func proxyXML(a Answer) string {
	if a != AnswerProxy {
		return ""
	}

	return "<cas:proxyGrantingTicket>PGTIOU-1-proxy</cas:proxyGrantingTicket>" +
		"<cas:proxies><cas:proxy>https://proxy.example.com/pgt</cas:proxy></cas:proxies>"
}

// writeFailure writes a failure answer with the code.
func writeFailure(w http.ResponseWriter, code string) {
	write(w, rootOpen+`<cas:authenticationFailure code="`+code+`">the ticket was refused`+
		`</cas:authenticationFailure>`+rootClose)
}

// write writes body. A write fails only when the client went away, and
// the test server then has no one to tell.
func write(w http.ResponseWriter, body string) {
	if _, err := io.WriteString(w, body); err != nil {
		return
	}
}

// escape returns s with the special characters of XML escaped.
func escape(s string) string {
	var b strings.Builder

	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		// strings.Builder never fails a write.
		return ""
	}

	return b.String()
}
