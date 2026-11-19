package trustrules_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/trustrules"
	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// mtlsExchangeURL is the exchange route of the namespace acme.
const mtlsExchangeURL = rulesURL + "/exchange"

// spiffeGood is the SPIFFE ID that the rule of the tests names.
const spiffeGood = "spiffe://example.org/ns/acme/sa/worker"

// errDisk is the failure of the store in the 500 case.
var errDisk = errors.New("disk full")

// pkiCA is a CA of the tests: ECDSA, valid from 2020 to 2100.
type pkiCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  string
}

// newPKICA makes a self-signed CA.
func newPKICA(t *testing.T) *pkiCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = big.NewInt(1)
	tmpl.Subject.CommonName = "test ca"
	tmpl.NotBefore = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.NotAfter = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
	tmpl.IsCA = true
	tmpl.BasicConstraintsValid = true
	tmpl.KeyUsage = x509.KeyUsageCertSign

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse CA: %v", err)
	}

	block := &pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der}

	return &pkiCA{cert: cert, key: key, pem: string(pem.EncodeToMemory(block))}
}

// leaf returns a client certificate for the SPIFFE ID id that ca signs, valid
// in [notBefore, notAfter].
func (ca *pkiCA) leaf(t *testing.T, id string, notBefore, notAfter time.Time) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	u, err := url.Parse(id)
	if err != nil {
		t.Fatalf("parse URI: %v", err)
	}

	tmpl := new(x509.Certificate)
	tmpl.SerialNumber = big.NewInt(2)
	tmpl.Subject.CommonName = "workload"
	tmpl.NotBefore = notBefore
	tmpl.NotAfter = notAfter
	tmpl.URIs = []*url.URL{u}
	tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{der}, PrivateKey: key, Leaf: nil, OCSPStaple: nil, SignedCertificateTimestamps: nil,
		SupportedSignatureAlgorithms: nil,
	}
}

// goodLeaf is valid at testNow for 30 minutes.
func (ca *pkiCA) goodLeaf(t *testing.T) tls.Certificate {
	t.Helper()

	return ca.leaf(t, spiffeGood, testNow().Add(-time.Hour), testNow().Add(30*time.Minute))
}

// failingExchanges is an exchange store whose write fails.
type failingExchanges struct {
	trustrule.ExchangeStore
}

func (failingExchanges) RecordExchange(
	_ context.Context, _ trustrule.Exchange, _ namespace.Fence, _ audit.Entry,
) error {
	return errDisk
}

// mtlsOpts sets the server of a test.
type mtlsOpts struct {
	// revocation is the revocation source. Nil means no source.
	revocation func(*x509.Certificate) error
	// failWrite makes the store fail the exchange write.
	failWrite bool
	// limit is the calls of one key in one window.
	limit int
	// noMtls leaves the mTLS exchange out of the handler config.
	noMtls bool
	// canceled gives each request a context that is already canceled, like a
	// client that disconnects during the call.
	canceled bool
	// guardNow is the clock of the guard. Nil means the fixed test time.
	guardNow func() time.Time
	// onRefused is the OnMtlsRefused callback of the handler.
	onRefused func(reason string)
	// failServer, when not nil, makes the write in the audit log of the
	// installation fail while it holds true.
	failServer *atomic.Bool
}

// failingServerAudit is a server audit store that fails while fail holds
// true.
type failingServerAudit struct {
	audit.ServerStore

	fail *atomic.Bool
}

func (f failingServerAudit) AppendServer(ctx context.Context, e audit.Entry) (audit.Entry, error) {
	if f.fail.Load() {
		return audit.Entry{}, errDisk
	}

	return f.ServerStore.AppendServer(ctx, e)
}

// mtlsServer is the handler behind a TLS server that asks for a client
// certificate, over the store of e.
type mtlsServer struct {
	srv *httptest.Server
}

func noRevocation(*x509.Certificate) error { return nil }

// newMtlsServer serves the handler with the mTLS exchange on a TLS server.
func (e *env) newMtlsServer(o mtlsOpts) *mtlsServer {
	e.t.Helper()

	return e.newMtlsServerWith(o, nil)
}

// newMtlsServerWith is newMtlsServer, and the mTLS exchanger reads its
// namespaces through wrap when wrap is not nil.
func (e *env) newMtlsServerWith(
	o mtlsOpts, wrap func(trustrulesvc.Namespaces) trustrulesvc.Namespaces,
) *mtlsServer {
	e.t.Helper()

	namespaces, err := namespace.NewService(namespace.Config{Store: e.store, Now: nil, Installation: installationName})
	if err != nil {
		e.t.Fatalf("namespace service: %v", err)
	}

	authz := allActions()

	rules, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: e.store.TrustRules(), Namespaces: namespaces, Principals: e.store, Authorizer: authz,
		Reach: e.tokens, InstallationURLs: []string{publicURL}, OwnIssuer: nil, CheckURL: nil, Now: testNow,
		Bundles: e.store.TrustRules(), Sessions: nil,
	})
	if err != nil {
		e.t.Fatalf("trust rule service: %v", err)
	}

	exchanger, err := trustrulesvc.NewExchanger(trustrulesvc.ExchangeConfig{
		Rules: e.store.TrustRules(), Exchanges: e.store.TrustRules(), Namespaces: namespaces, Principals: e.store,
		Tokens: e.tokens, Audit: e.store, ServerAudit: e.store, Refusals: nil, Keys: nil,
		InstallationURLs: []string{publicURL}, OwnIssuer: nil, Now: testNow, Log: nil,
	})
	if err != nil {
		e.t.Fatalf("exchanger: %v", err)
	}

	var exchanges trustrule.ExchangeStore = e.store.TrustRules()
	if o.failWrite {
		exchanges = failingExchanges{ExchangeStore: exchanges}
	}

	var mtlsNamespaces trustrulesvc.Namespaces = namespaces
	if wrap != nil {
		mtlsNamespaces = wrap(namespaces)
	}

	serverAudit := o.serverAudit(e)

	mtls, err := trustrulesvc.NewMtlsExchanger(trustrulesvc.MtlsConfig{
		Rules: e.store.TrustRules(), Exchanges: exchanges, Bundles: e.store.TrustRules(), Namespaces: mtlsNamespaces,
		Principals: e.store, Tokens: e.tokens, Audit: e.store, ServerAudit: serverAudit, Refusals: nil,
		Revocation: o.revocation, Now: testNow, Log: nil,
	})
	if err != nil {
		e.t.Fatalf("mtls exchanger: %v", err)
	}

	guard := e.newGuard(o)

	cfgMtls, cfgGuard := mtls, guard
	if o.noMtls {
		cfgMtls, cfgGuard = nil, nil
	}

	h, err := trustrules.NewHandler(trustrules.Config{
		Exchanger: exchanger, MtlsExchanger: cfgMtls, MtlsGuard: cfgGuard,
		Namespaces: namespaces, Rules: rules, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit: e.audit, OnAuditFailure: nil, OnMtlsRefused: o.onRefused, DenialLimiter: nil, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	})
	if err != nil {
		e.t.Fatalf("handler: %v", err)
	}

	var served http.Handler = h

	if o.canceled {
		served = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithCancel(r.Context())
			cancel()
			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	srv := httptest.NewUnstartedServer(served)
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	e.t.Cleanup(srv.Close)

	return &mtlsServer{srv: srv}
}

// serverAudit returns the server audit store of the exchanger. It fails
// while o.failServer holds true, and never when o.failServer is nil.
func (o mtlsOpts) serverAudit(e *env) failingServerAudit {
	fail := o.failServer
	if fail == nil {
		fail = &atomic.Bool{}
	}

	return failingServerAudit{ServerStore: e.store, fail: fail}
}

// newGuard makes the guard of a test server.
func (e *env) newGuard(o mtlsOpts) *trustrulesvc.MtlsGuard {
	e.t.Helper()

	limit := o.limit
	if limit == 0 {
		limit = 1000
	}

	guardNow := testNow
	if o.guardNow != nil {
		guardNow = o.guardNow
	}

	guard, err := trustrulesvc.NewMtlsGuard(trustrulesvc.MtlsGuardConfig{
		MaxBodyBytes: 1024, Limit: limit, GlobalLimit: 1000, Window: time.Minute, MaxKeys: 100,
		Now: guardNow,
	})
	if err != nil {
		e.t.Fatalf("mtls guard: %v", err)
	}

	return guard
}

// call posts an empty body to the exchange route of path, with the client
// certificate cert (nil for none) and the headers.
func (m *mtlsServer) call(
	t *testing.T, path string, cert *tls.Certificate, header map[string]string,
) (int, http.Header, []byte) {
	t.Helper()

	roots := x509.NewCertPool()
	roots.AddCert(m.srv.Certificate())

	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}
	if cert != nil {
		cfg.Certificates = []tls.Certificate{*cert}
	}

	tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, m.srv.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	return doRequest(t, &http.Client{Transport: tr}, req)
}

// doRequest sends req and reads the answer.
func doRequest(t *testing.T, c *http.Client, req *http.Request) (int, http.Header, []byte) {
	t.Helper()

	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			t.Errorf("close body: %v", closeErr)
		}
	}()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	return resp.StatusCode, resp.Header, out
}

// mtlsFixture is an env with a bundle of the CA ca and one rule for spiffeGood.
func mtlsFixture(t *testing.T) (*env, *pkiCA, string) {
	t.Helper()

	e := newEnv(t, allActions())
	ca := newPKICA(t)

	b, err := json.Marshal(map[string]string{"pem": ca.pem})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	status, out := e.admin(http.MethodPost, bundlesURL, string(b))
	if status != http.StatusCreated {
		t.Fatalf("save bundle = %d %s, want 201", status, out)
	}

	var doc struct {
		ID string `json:"id"`
	}

	decode(t, out, &doc)

	status, out = e.admin(http.MethodPost, rulesURL, withMTLS(ruleBody("worker", e.worker), spiffeGood, doc.ID))
	if status != http.StatusCreated {
		t.Fatalf("create rule = %d %s, want 201", status, out)
	}

	var r rule

	decode(t, out, &r)

	return e, ca, r.ID
}

// exchangeEntries returns the exchange audit entries of acme.
func (e *env) exchangeEntries() []audit.Entry {
	e.t.Helper()

	listing, err := e.store.List(e.t.Context(), "acme", audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		e.t.Fatalf("list audit: %v", err)
	}

	var out []audit.Entry

	for i := range listing.Entries {
		if listing.Entries[i].Action == "trust-rule:exchange" {
			out = append(out, listing.Entries[i])
		}
	}

	return out
}

// TestExchangeMtlsIssuesAToken makes a real TLS handshake with a client
// certificate that the active CA of the bundle signed, and checks the 201
// answer and the audit entry.
func TestExchangeMtlsIssuesAToken(t *testing.T) {
	t.Parallel()

	e, ca, ruleID := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      0,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)

	status, header, body := m.call(t, mtlsExchangeURL, &cert, nil)
	if status != http.StatusCreated {
		t.Fatalf("exchange = %d %s, want 201", status, body)
	}

	if got := header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}

	var doc struct {
		ExpiresAt time.Time `json:"expiresAt"`
		Token     string    `json:"token"`
		ID        string    `json:"id"`
		Principal string    `json:"principal"`
		Rule      string    `json:"rule"`
	}

	decode(t, body, &doc)

	if doc.Token == "" || doc.ID == "" {
		t.Errorf("token %q and id %q must not be empty", doc.Token, doc.ID)
	}

	if doc.Principal != e.worker || doc.Rule != ruleID {
		t.Errorf("principal %q rule %q, want %q %q", doc.Principal, doc.Rule, e.worker, ruleID)
	}

	if want := testNow().Add(30 * time.Minute); doc.ExpiresAt.After(want) || !doc.ExpiresAt.After(testNow()) {
		t.Errorf("expiresAt = %v, want after %v and not after %v", doc.ExpiresAt, testNow(), want)
	}

	entries := e.exchangeEntries()
	if len(entries) != 1 || entries[0].Outcome != audit.OutcomeSuccess || entries[0].Action != "trust-rule:exchange" {
		t.Fatalf("exchange entries = %+v, want one success entry", entries)
	}
}

// hostileRow is one hostile call.
type hostileRow struct {
	name string
	// cert makes the client certificate. Nil sends none.
	cert func(t *testing.T, ca *pkiCA) *tls.Certificate
	// header holds more request headers, built from the good certificate.
	header func(t *testing.T, ca *pkiCA) map[string]string
	// path is the route. Empty means mtlsExchangeURL.
	path       string
	noBundle   bool
	revocation func(*x509.Certificate) error
	failWrite  bool
	status     int
	code       string
	// denied is the number of denial entries that the service writes. Zero
	// where the service writes none: an unknown namespace has no audit log,
	// and a failed store writes no entry.
	denied int
}

// forged returns the headers of a forwarded chain that hold a good leaf.
func forged(t *testing.T, ca *pkiCA) map[string]string {
	t.Helper()

	c := ca.goodLeaf(t)
	der := c.Certificate[0]
	p := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: nil, Bytes: der}))

	return map[string]string{
		"X-Forwarded-Client-Cert": `Cert="` + url.QueryEscape(p) + `"`,
		"X-Client-Cert":           url.QueryEscape(p),
		"X-SSL-Client-Cert":       base64.StdEncoding.EncodeToString(der),
		"X-Client-Verify":         "SUCCESS",
	}
}

// TestExchangeMtlsHostile sends each hostile input to the HTTP layer as if
// every other layer failed. Each answer has no token and names only the code.
func TestExchangeMtlsHostile(t *testing.T) {
	t.Parallel()

	rows := []hostileRow{
		{
			name: "no handshake certificate", cert: nil, header: nil, path: "", noBundle: false,
			revocation: noRevocation, failWrite: false, status: 401, code: "mtls.no-client-certificate", denied: 1,
		},
		{
			name: "forged forwarded chain and no handshake certificate", cert: nil, header: forged, path: "",
			noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.no-client-certificate", denied: 1,
		},
		{
			name: "certificate of an unknown CA",
			cert: func(t *testing.T, _ *pkiCA) *tls.Certificate {
				t.Helper()

				return new(newPKICA(t).goodLeaf(t))
			},
			header: nil, path: "", noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.unknown-issuer", denied: 1,
		},
		{
			name: "expired leaf",
			cert: func(t *testing.T, ca *pkiCA) *tls.Certificate {
				t.Helper()

				return new(ca.leaf(t, spiffeGood, testNow().Add(-3*time.Hour), testNow().Add(-time.Hour)))
			},
			header: nil, path: "", noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.expired", denied: 1,
		},
		{
			name: "leaf not yet valid",
			cert: func(t *testing.T, ca *pkiCA) *tls.Certificate {
				t.Helper()

				return new(ca.leaf(t, spiffeGood, testNow().Add(time.Hour), testNow().Add(2*time.Hour)))
			},
			header: nil, path: "", noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.not-yet-valid", denied: 1,
		},
		{
			name: "revoked leaf", cert: goodCert, header: nil, path: "", noBundle: false,
			revocation: func(*x509.Certificate) error { return trustrulesvc.ErrRevoked }, failWrite: false,
			status: 401, code: "mtls.revoked", denied: 1,
		},
		{
			name: "no revocation source", cert: goodCert, header: nil, path: "", noBundle: false, revocation: nil,
			failWrite: false, status: 401, code: "mtls.revocation-unknown", denied: 1,
		},
		{
			name: "good chain and no rule names the SPIFFE ID",
			cert: func(t *testing.T, ca *pkiCA) *tls.Certificate {
				t.Helper()

				return new(ca.leaf(t, "spiffe://example.org/ns/acme/sa/other", testNow().Add(-time.Hour),
					testNow().Add(30*time.Minute)))
			},
			header: nil, path: "", noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.no-matching-rule", denied: 1,
		},
		{
			name: "namespace with no bundle", cert: goodCert, header: nil, path: "", noBundle: true,
			revocation: noRevocation, failWrite: false, status: 401, code: "mtls.bundle-disabled", denied: 1,
		},
		{
			// The namespace does not exist: it has no audit log, so the
			// service writes no entry.
			name: "unknown namespace", cert: goodCert, header: nil,
			path:     "/api/dustpan/v1/namespaces/nowhere/trust-rules/exchange",
			noBundle: false, revocation: noRevocation, failWrite: false, status: 401,
			code: "mtls.bundle-disabled", denied: 0,
		},
		{
			// The store fails after the checks passed: no token and no
			// entry, by the contract of the service.
			name: "store write failure", cert: goodCert, header: nil, path: "", noBundle: false,
			revocation: noRevocation, failWrite: true, status: 500, code: "token.issue-failed", denied: 0,
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			runHostile(t, &row)
		})
	}
}

// runHostile sends the call of row and checks the answer and the audit log.
func runHostile(t *testing.T, row *hostileRow) {
	t.Helper()

	var (
		e  *env
		ca *pkiCA
	)

	if row.noBundle {
		e, ca = newEnv(t, allActions()), newPKICA(t)
	} else {
		e, ca, _ = mtlsFixture(t)
	}

	m := e.newMtlsServer(
		mtlsOpts{
			revocation: row.revocation,
			failWrite:  row.failWrite,
			limit:      0,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)

	var cert *tls.Certificate
	if row.cert != nil {
		cert = row.cert(t, ca)
	}

	var header map[string]string
	if row.header != nil {
		header = row.header(t, ca)
	}

	path := row.path
	if path == "" {
		path = mtlsExchangeURL
	}

	status, _, body := m.call(t, path, cert, header)
	if status != row.status || errorCode(t, body) != row.code {
		t.Fatalf("answer = %d %s, want %d %s", status, body, row.status, row.code)
	}

	assertNoToken(t, body)

	entries := e.exchangeEntries()
	if len(entries) != row.denied {
		t.Fatalf("exchange entries = %+v, want %d", entries, row.denied)
	}

	for i := range entries {
		if entries[i].Outcome != audit.OutcomeDenied || entries[i].TokenID != "" {
			t.Errorf("entry %+v, want a denial with no token", entries[i])
		}
	}
}

// goodCert is the client certificate that the CA of the bundle signed.
func goodCert(t *testing.T, ca *pkiCA) *tls.Certificate {
	t.Helper()

	return new(ca.goodLeaf(t))
}

// assertNoToken fails the test when the answer body holds a token key, or the
// text of a check that failed beyond the code.
func assertNoToken(t *testing.T, body []byte) {
	t.Helper()

	var doc map[string]any

	decode(t, body, &doc)

	if _, ok := doc["token"]; ok {
		t.Errorf("answer %s holds a token", body)
	}
}

// TestExchangeMtlsBodyTooLarge sends a body over the cap to the route. A
// body is never an mTLS call: the call goes to the JWT path, which answers
// 400 request.body-invalid as before (it has never answered 413). The guard
// answer 413 request.body-too-large is not reachable from the route, because
// the guard sees a content length of 0; classifyMtls maps it, and
// TestClassifyMtlsRows checks that row. The decode writes no audit entry.
func TestExchangeMtlsBodyTooLarge(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      0,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)
	body := `{"token":"` + strings.Repeat("a", trustrule.MaxTokenBytes+4096) + `"}`

	roots := x509.NewCertPool()
	roots.AddCert(m.srv.Certificate())

	cfg := &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}}
	tr := &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
	t.Cleanup(tr.CloseIdleConnections)

	target := m.srv.URL + mtlsExchangeURL

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	status, _, out := doRequest(t, &http.Client{Transport: tr}, req)
	if status != http.StatusBadRequest || errorCode(t, out) != "request.body-invalid" {
		t.Fatalf("answer = %d %s, want 400 request.body-invalid", status, out)
	}

	assertNoToken(t, out)

	if got := e.exchangeEntries(); len(got) != 0 {
		t.Errorf("exchange entries = %+v, want none: the body decode writes none", got)
	}
}

// TestExchangeMtlsRateLimit makes four calls with no certificate in one
// window of a guard with a limit of 3: the fourth gets 429. The guard writes
// no audit entry for a rate limited call, so the log holds three denials.
func TestExchangeMtlsRateLimit(t *testing.T) {
	t.Parallel()

	e, _, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      3,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)

	for i := 1; i <= 3; i++ {
		status, _, body := m.call(t, mtlsExchangeURL, nil, nil)
		if status != http.StatusUnauthorized || errorCode(t, body) != "mtls.no-client-certificate" {
			t.Fatalf("call %d = %d %s, want 401 mtls.no-client-certificate", i, status, body)
		}
	}

	status, _, body := m.call(t, mtlsExchangeURL, nil, nil)
	if status != http.StatusTooManyRequests || errorCode(t, body) != "mtls.rate-limited" {
		t.Fatalf("call 4 = %d %s, want 429 mtls.rate-limited", status, body)
	}

	assertNoToken(t, body)
}

// TestExchangeMtlsNotConfigured sends a good certificate to a server with no
// mTLS exchange: the call fails closed with 401
// mtls.no-client-certificate, and gets no token. The service is not there to
// write an entry.
func TestExchangeMtlsNotConfigured(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      0,
			noMtls:     true,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)

	status, _, body := m.call(t, mtlsExchangeURL, &cert, nil)
	if status != http.StatusUnauthorized || errorCode(t, body) != "mtls.no-client-certificate" {
		t.Fatalf("answer = %d %s, want 401 mtls.no-client-certificate", status, body)
	}

	assertNoToken(t, body)

	if got := e.exchangeEntries(); len(got) != 0 {
		t.Errorf("exchange entries = %+v, want none", got)
	}
}

// TestExchangeMtlsRateLimitAudit makes the calls of one window with a good
// certificate. The call over the limit answers 429 and writes one denial
// entry. Each later call of the window is counted and writes no entry.
func TestExchangeMtlsRateLimitAudit(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      2,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)

	for i := 1; i <= 2; i++ {
		if status, _, body := m.call(t, mtlsExchangeURL, &cert, nil); status != http.StatusCreated {
			t.Fatalf("call %d = %d %s, want 201", i, status, body)
		}
	}

	denied := func() int {
		n := 0

		for _, entry := range e.exchangeEntries() {
			if entry.Outcome == audit.OutcomeDenied {
				n++
			}
		}

		return n
	}

	for i := 3; i <= 4; i++ {
		status, _, body := m.call(t, mtlsExchangeURL, &cert, nil)
		if status != http.StatusTooManyRequests || errorCode(t, body) != "mtls.rate-limited" {
			t.Fatalf("call %d = %d %s, want 429 mtls.rate-limited", i, status, body)
		}

		assertNoToken(t, body)

		if got := denied(); got != 1 {
			t.Fatalf("after call %d: %d denial entries, want 1", i, got)
		}
	}
}

// countingNamespaces counts the namespace reads of the mTLS exchanger and
// fails the ones of WritableHere when fail is not nil.
type countingNamespaces struct {
	trustrulesvc.Namespaces

	writable atomic.Int64
	fail     error
	// failing, when not nil, makes fail apply only while it holds true.
	failing *atomic.Bool
}

func (c *countingNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	c.writable.Add(1)

	if c.fail != nil && (c.failing == nil || c.failing.Load()) {
		return namespace.Namespace{}, c.fail
	}

	return c.Namespaces.WritableHere(ctx, name)
}

// TestExchangeMtlsRefusedFloodReads makes many calls over the limit. The
// answer of each is 429, and the namespace is read for the first refusal of
// the window only, so a flood costs no store read for each call.
func TestExchangeMtlsRefusedFloodReads(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)

	counter := &countingNamespaces{Namespaces: nil, writable: atomic.Int64{}, fail: nil, failing: nil}

	m := e.newMtlsServerWith(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      1,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
		func(n trustrulesvc.Namespaces) trustrulesvc.Namespaces {
			counter.Namespaces = n

			return counter
		},
	)
	cert := ca.goodLeaf(t)

	if status, _, body := m.call(t, mtlsExchangeURL, &cert, nil); status != http.StatusCreated {
		t.Fatalf("first call = %d %s, want 201", status, body)
	}

	before := counter.writable.Load()

	for i := 2; i <= 12; i++ {
		status, _, body := m.call(t, mtlsExchangeURL, &cert, nil)
		if status != http.StatusTooManyRequests || errorCode(t, body) != "mtls.rate-limited" {
			t.Fatalf("call %d = %d %s, want 429 mtls.rate-limited", i, status, body)
		}
	}

	if got := counter.writable.Load() - before; got != 1 {
		t.Fatalf("%d namespace reads for 11 refused calls, want 1", got)
	}
}

// TestExchangeMtlsRefusedAnswer checks that the answer to a call that the
// guard refuses is the answer of the guard, also when the denial entry
// cannot be written.
func TestExchangeMtlsRefusedAnswer(t *testing.T) {
	t.Parallel()

	t.Run("a store error of the namespace read still answers 429", func(t *testing.T) {
		t.Parallel()

		e, ca, _ := mtlsFixture(t)
		m := e.newMtlsServerWith(
			mtlsOpts{
				revocation: noRevocation,
				failWrite:  false,
				limit:      1,
				noMtls:     false,
				canceled:   false,
				guardNow:   nil,
				onRefused:  nil,
				failServer: nil,
			},
			func(n trustrulesvc.Namespaces) trustrulesvc.Namespaces {
				return &countingNamespaces{Namespaces: n, writable: atomic.Int64{}, fail: errDisk, failing: nil}
			},
		)
		cert := ca.goodLeaf(t)

		// The first call passes the guard and then fails at the namespace
		// read of Verify. The second is over the limit.
		m.call(t, mtlsExchangeURL, &cert, nil)

		status, _, body := m.call(t, mtlsExchangeURL, &cert, nil)
		if status != http.StatusTooManyRequests || errorCode(t, body) != "mtls.rate-limited" {
			t.Fatalf("answer = %d %s, want 429 mtls.rate-limited", status, body)
		}
	})

	t.Run("an unknown namespace still answers 429", func(t *testing.T) {
		t.Parallel()

		e, ca, _ := mtlsFixture(t)
		m := e.newMtlsServer(
			mtlsOpts{
				revocation: noRevocation,
				failWrite:  false,
				limit:      1,
				noMtls:     false,
				canceled:   false,
				guardNow:   nil,
				onRefused:  nil,
				failServer: nil,
			},
		)
		cert := ca.goodLeaf(t)
		path := "/api/dustpan/v1/namespaces/nowhere/trust-rules/exchange"

		m.call(t, path, &cert, nil)

		status, _, body := m.call(t, path, &cert, nil)
		if status != http.StatusTooManyRequests || errorCode(t, body) != "mtls.rate-limited" {
			t.Fatalf("answer = %d %s, want 429 mtls.rate-limited", status, body)
		}
	})
}

// deniedExchangeEntries returns the exchange audit entries of acme with the
// outcome denied.
func (e *env) deniedExchangeEntries() []audit.Entry {
	e.t.Helper()

	var out []audit.Entry

	all := e.exchangeEntries()
	for i := range all {
		if all[i].Outcome == audit.OutcomeDenied {
			out = append(out, all[i])
		}
	}

	return out
}

// TestExchangeMtlsRefusalNotLost checks that an attacker cannot make the
// guard lose the audited refusal of a flood to acme. Each case ends with at
// least one denial entry in acme.
func TestExchangeMtlsRefusalNotLost(t *testing.T) {
	t.Parallel()

	t.Run("a refused call to a namespace that does not exist first", testRefusalUnknownFirst)
	t.Run("a store error at the first refusal", testRefusalStoreError)
	t.Run("a client that disconnects during the first refusal", testRefusalCanceled)
}

func testRefusalUnknownFirst(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      1,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)
	ghost := "/api/dustpan/v1/namespaces/nowhere/trust-rules/exchange"

	m.call(t, ghost, &cert, nil)
	m.call(t, ghost, &cert, nil)

	for range 6 {
		if status, _, _ := m.call(t, mtlsExchangeURL, &cert, nil); status != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", status)
		}
	}

	if got := len(e.deniedExchangeEntries()); got < 1 {
		t.Fatalf("%d denial entries in acme after 6 refused calls, want at least 1", got)
	}
}

func testRefusalStoreError(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	failing := &atomic.Bool{}
	counter := &countingNamespaces{Namespaces: nil, writable: atomic.Int64{}, fail: errDisk, failing: failing}
	m := e.newMtlsServerWith(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      1,
			noMtls:     false,
			canceled:   false,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
		func(n trustrulesvc.Namespaces) trustrulesvc.Namespaces {
			counter.Namespaces = n

			return counter
		},
	)
	cert := ca.goodLeaf(t)

	m.call(t, mtlsExchangeURL, &cert, nil)

	failing.Store(true)
	m.call(t, mtlsExchangeURL, &cert, nil)
	failing.Store(false)

	for range 6 {
		if status, _, _ := m.call(t, mtlsExchangeURL, &cert, nil); status != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", status)
		}
	}

	if got := len(e.deniedExchangeEntries()); got < 1 {
		t.Fatalf("%d denial entries in acme after the store recovered, want at least 1", got)
	}
}

func testRefusalCanceled(t *testing.T) {
	t.Parallel()

	e, ca, _ := mtlsFixture(t)
	m := e.newMtlsServer(
		mtlsOpts{
			revocation: noRevocation,
			failWrite:  false,
			limit:      1,
			noMtls:     false,
			canceled:   true,
			guardNow:   nil,
			onRefused:  nil,
			failServer: nil,
		},
	)
	cert := ca.goodLeaf(t)

	for range 7 {
		m.call(t, mtlsExchangeURL, &cert, nil)
	}

	if got := len(e.deniedExchangeEntries()); got < 1 {
		t.Fatalf("%d denial entries in acme after refused calls with a canceled context, want at least 1", got)
	}
}
