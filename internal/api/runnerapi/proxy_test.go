package runnerapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	// proxyUnitID is the stable unit ID of the unit of the job of the tests.
	proxyUnitID = "u-network"
	// The upstream proxy of the pool and its credential, which no error body
	// may hold.
	proxyUpstreamURL = "https://upstream.example.com:3128"
	proxyUser        = "upstream-user"
	proxyPassword    = "upstream-password-0123456789"
)

// proxyEnv is a session environment whose handler serves the paths of the
// job proxy, with the proxy settings of the pool saved.
type proxyEnv struct {
	*sessionEnv

	audit *testAudit
}

// unitJobs gives the job of the tests the stable unit ID proxyUnitID.
type unitJobs struct {
	inner runnerapi.JobReader
}

func (u unitJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	j, err := u.inner.Get(ctx, id)
	if err != nil {
		return job.Job{}, err
	}

	j.Spec.Unit = j.Spec.Unit.WithUnitID(proxyUnitID)

	return j, nil
}

// proxyUnits knows the unit of the job of the tests.
type proxyUnits struct{}

func (proxyUnits) GetUnit(_ context.Context, unitID string) (inventory.Entry, error) {
	var e inventory.Entry

	if unitID != proxyUnitID {
		return e, inventory.ErrUnitNotFound
	}

	e.Namespace, e.Tool, e.Name, e.UnitID, e.Labels = "acme", "opentofu", "network", proxyUnitID, map[string]string{}

	return e, nil
}

// newProxyEnv opens a SQLite store, claims the phase for a runner, and saves
// the proxy settings of the pool with an upstream proxy and its credential.
func newProxyEnv(t *testing.T) *proxyEnv {
	t.Helper()

	var (
		raw *sqlite.Store
		svc *proxysessionsvc.Service
		log *testAudit
	)

	open := func(t *testing.T) stores {
		t.Helper()

		s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if closeErr := s.Close(); closeErr != nil {
				t.Errorf("close store: %v", closeErr)
			}
		})

		raw = s

		return stores{
			namespaces: s, operations: s.DurableOperations(), jobs: s.Jobs(), jobtokens: s.JobTokens(), units: s,
			runners: s.Runners(), audit: s,
		}
	}

	s := newSessionEnvWith(t, backend{name: "sqlite", open: open}, func(cfg *runnerapi.Config) {
		svc = newProxyService(t, raw, cfg)
		cfg.Proxies = svc

		var ok bool
		if log, ok = cfg.Audit.(*testAudit); !ok {
			t.Fatal("the audit of the handler is not the test audit")
		}
	})

	if raw == nil || svc == nil {
		t.Fatal("the session env did not build the store and the proxy service")
	}

	pool, err := raw.Runners().Pool(t.Context(), s.pool)
	if err != nil {
		t.Fatalf("read pool: %v", err)
	}

	_, err = svc.PutSettings(t.Context(), &proxysessionsvc.PutRequest{
		Pool: pool, AllowedRanges: []string{"10.20.0.0/16"}, UpstreamURL: proxyUpstreamURL,
		Credential: proxysessionsvc.CredentialChange{
			Present: true, Value: &proxysessionsvc.Credential{Username: proxyUser, Password: proxyPassword},
		},
		IfMatch: "", Entry: settingsEntry(),
	})
	if err != nil {
		t.Fatalf("save proxy settings: %v", err)
	}

	return &proxyEnv{sessionEnv: s, audit: log}
}

// newProxyService builds the proxy session service over the raw store and the
// dependencies of cfg.
func newProxyService(t *testing.T, raw *sqlite.Store, cfg *runnerapi.Config) *proxysessionsvc.Service {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("wrapper: %v", err)
	}

	secrets, err := secret.NewService(secret.Config{
		Keys: raw, Wrapper: wrapper, Namespaces: cfg.Namespaces, Now: nil, MaxBatchBytes: 0,
	})
	if err != nil {
		t.Fatalf("secret service: %v", err)
	}

	svc, err := proxysessionsvc.New(proxysessionsvc.Config{
		Settings: raw.Runners(), Rules: raw.ProxyRules(), Secrets: secrets, Namespaces: cfg.Namespaces,
		Operations: raw.DurableOperations(), Claims: raw.Runners(), Jobs: unitJobs{inner: cfg.Jobs},
		Units: proxyUnits{}, Audit: cfg.Audit, Logger: cfg.Logger, Now: cfg.Now,
	})
	if err != nil {
		t.Fatalf("proxy session service: %v", err)
	}

	return svc
}

// settingsEntry returns the audit entry of a save by the administrator.
func settingsEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "ada", IDPSubject: "", TokenID: "", Action: "",
		Object: identity.NamespaceObject("acme").String(), Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
	}
}

// proxyHandoff asks for the proxy handoff token of the claim and returns its
// secret.
func (p *proxyEnv) proxyHandoff() string {
	p.t.Helper()

	status, body := p.runner.call(opPath(p.work.Operation, "proxy-handoff"), nil)

	var resp protocol.HandoffResponse

	decode(p.t, body, &resp)

	if status != http.StatusOK || !strings.HasPrefix(resp.HandoffToken, runner.HandoffTokenPrefix) {
		p.t.Fatalf("proxy handoff = %d %s, want 200 with a dph_ token", status, body)
	}

	return resp.HandoffToken
}

// proxyExchange sends the exchange x to the path of the proxy session.
func (p *proxyEnv) proxyExchange(x exchange) (int, []byte) {
	p.t.Helper()

	req := protocol.SessionRequest{
		HandoffToken: x.token, PublicKey: x.pub, Release: x.release,
		Proof: ed25519.Sign(x.priv,
			runner.SessionChallenge(serverURL, x.token, runner.Key{Public: x.pub, CreatedAt: time.Time{}})),
	}
	raw := encodeBody(p.t, req)

	return p.sendSigned(http.MethodPost, protocol.PathProxySession, raw, sum(raw), signing{
		subject: sha256Hex(x.token), key: x.signer, at: x.at, nonce: x.nonce, release: x.release,
		contentHash: "", server: "",
	})
}

// openProxy gets a proxy handoff token, opens the proxy session and returns
// it as a launcher of the session key, with the answer.
func (p *proxyEnv) openProxy() (*launcher, protocol.ProxySessionResponse) {
	p.t.Helper()

	x := p.newExchange(p.proxyHandoff())

	status, body := p.proxyExchange(x)
	if status != http.StatusOK {
		p.t.Fatalf("proxy session exchange = %d %s, want 200", status, body)
	}

	var resp protocol.ProxySessionResponse

	decode(p.t, body, &resp)

	return &launcher{e: p.sessionEnv, id: resp.Session, pub: x.pub, priv: x.priv}, resp
}

// auditEntries returns the entries that the handler wrote.
func (p *proxyEnv) auditEntries() []audit.Entry {
	p.t.Helper()

	listing, err := p.audit.List(p.t.Context(), "acme", audit.Page{Size: audit.MaxPageSize})
	if err != nil {
		p.t.Fatalf("list audit: %v", err)
	}

	return listing.Entries
}

// codeOf returns the code of a protocol error body.
func codeOf(t *testing.T, body []byte) string {
	t.Helper()

	var e protocol.Error

	decode(t, body, &e)

	return e.Code
}

// TestProxyPathsFlow opens a proxy session, reads the assignment and the
// credential, and checks the audit entries.
func TestProxyPathsFlow(t *testing.T) {
	t.Parallel()

	p := newProxyEnv(t)
	proxy, resp := p.openProxy()

	a := resp.Assignment
	if a.Namespace != "acme" || a.Operation != "plan" || a.ServerHost != "dustpan.test" ||
		!slices.Equal(a.AllowedRanges, []string{"10.20.0.0/16"}) || a.UpstreamURL != proxyUpstreamURL ||
		!strings.HasPrefix(a.RulesDigest, "sha256:") || a.Rules == nil {
		t.Errorf("proxy assignment = %+v, want the settings of the pool and no rule", a)
	}

	status, body := proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, nil))
	if status != http.StatusOK {
		t.Fatalf("upstream credential = %d %s, want 200", status, body)
	}

	var cred protocol.UpstreamCredentialResponse

	decode(t, body, &cred)

	if cred.Username != proxyUser || cred.Password != proxyPassword {
		t.Errorf("upstream credential user = %q, want the saved credential", cred.Username)
	}

	p.checkProxyEvents()
}

// checkProxyEvents checks that the audit log holds the opened session and
// the reveal of the credential.
func (p *proxyEnv) checkProxyEvents() {
	p.t.Helper()

	events := map[string]audit.Kind{}

	entries := p.auditEntries()

	for i := range entries {
		for _, c := range entries[i].Changes {
			if c.Key == "event" {
				events[c.New] = entries[i].Kind
			}
		}
	}

	if events[string(runner.EventProxySessionOpened)] != audit.KindChange ||
		events[string(runner.EventProxyCredentialIssued)] != audit.KindReveal {
		p.t.Errorf("audit events = %v, want proxy-session-opened and a reveal of proxy-credential-issued", events)
	}
}

// lastHeader returns the header of the last answer that the env read.
func (e *env) lastHeader() http.Header {
	e.t.Helper()

	h := e.last.Load()
	if h == nil {
		e.t.Fatal("no answer read yet")
	}

	return *h
}

// TestUpstreamCredentialNoStore is the hostile table of the answer layer: the
// answer of the upstream credential path carries a credential, so each answer
// of the path, the 200 and each refusal, must forbid a cache. Each row fails
// when the header is missing.
func TestUpstreamCredentialNoStore(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		run    func(p *proxyEnv) (int, []byte)
		status int
	}{
		{
			name: "credential answer",
			run: func(p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()

				return proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, nil))
			},
			status: http.StatusOK,
		},
		{
			name: "launcher session proof",
			run: func(p *proxyEnv) (int, []byte) {
				l := p.launch()

				return l.do(l.post(protocol.PathProxyUpstreamCredential, nil))
			},
			status: http.StatusForbidden,
		},
		{
			name: "runner key proof",
			run: func(p *proxyEnv) (int, []byte) {
				return p.runner.call(protocol.PathProxyUpstreamCredential, nil)
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "no proof",
			run: func(p *proxyEnv) (int, []byte) {
				return p.runner.raw(protocol.PathProxyUpstreamCredential, nil, "0.4.0")
			},
			status: http.StatusUnauthorized,
		},
		{
			// This row does not show that the header is set before the checks:
			// the refusal comes after enforce, from the handler. The rows
			// above that refuse in enforce show that.
			name: "body on a path with no body",
			run: func(p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()

				return proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, map[string]string{"pool": "x"}))
			},
			status: http.StatusBadRequest,
		},
		{
			name: "after the takeover",
			run: func(p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()
				p.takeOver()

				return proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, nil))
			},
			status: http.StatusConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newProxyEnv(t)
			status, body := tc.run(p)

			if status != tc.status {
				t.Fatalf("answer = %d %s, want %d", status, body, tc.status)
			}

			if got := p.lastHeader().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

// TestNoStoreOnlyOnCredentialPath is the negative table of the answer layer:
// a refusal of a path that does not carry a credential, made in enforce, must
// not set the no-store header. Each row fails when the credential test of a
// path answers true for every path.
func TestNoStoreOnlyOnCredentialPath(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		run    func(p *proxyEnv) (int, []byte)
		status int
	}{
		{
			name: "poll with no proof",
			run: func(p *proxyEnv) (int, []byte) {
				return p.runner.raw(protocol.PathPoll, nil, "0.4.0")
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "proxy handoff with no proof",
			run: func(p *proxyEnv) (int, []byte) {
				return p.runner.raw(protocol.PathClaimProxyHandoff, nil, "0.4.0")
			},
			status: http.StatusUnauthorized,
		},
		{
			name: "poll with a launcher session proof",
			run: func(p *proxyEnv) (int, []byte) {
				l := p.launch()

				return l.do(l.post(protocol.PathPoll, nil))
			},
			status: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newProxyEnv(t)
			status, body := tc.run(p)

			if status != tc.status {
				t.Fatalf("answer = %d %s, want %d", status, body, tc.status)
			}

			if got := p.lastHeader().Get("Cache-Control"); got != "" {
				t.Errorf("Cache-Control = %q, want none", got)
			}
		})
	}
}

// TestProxyPaths is the table of hostile cases of the handler layer: each
// proxy path with a launcher proof, a runner key and no proof, each launcher
// path with a proxy proof, a replayed nonce, a body past the limit, a session
// after the takeover and the operation of another claim.
func TestProxyPaths(t *testing.T) {
	t.Parallel()

	type result struct {
		status int
		code   string
	}

	cases := []struct {
		name string
		run  func(t *testing.T, p *proxyEnv) (int, []byte)
		want []result
	}{
		{
			name: "upstream credential with a launcher session proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				l := p.launch()

				return l.do(l.post(protocol.PathProxyUpstreamCredential, nil))
			},
			want: []result{{http.StatusForbidden, protocol.ErrorWrongSessionKind}},
		},
		{
			name: "upstream credential with a runner key proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.runner.call(protocol.PathProxyUpstreamCredential, nil)
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "upstream credential with no proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.runner.raw(protocol.PathProxyUpstreamCredential, nil, "0.4.0")
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "proxy session with a launcher handoff token",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.proxyExchange(p.newExchange(p.runner.handoff(p.work.Operation)))
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "proxy session with a runner key proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.runner.call(protocol.PathProxySession, nil)
			},
			want: []result{
				{http.StatusUnauthorized, protocol.ErrorBadProof},
				{http.StatusBadRequest, protocol.ErrorInvalid},
			},
		},
		{
			name: "proxy handoff with a proxy session proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()
				c := proxy.post(opPath(p.work.Operation, "proxy-handoff"), nil)

				return proxy.do(c)
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "proxy handoff with no proof",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.runner.raw(opPath(p.work.Operation, "proxy-handoff"), nil, "0.4.0")
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "launcher session with a proxy handoff token",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				return p.sendExchange(p.newExchange(p.proxyHandoff()))
			},
			want: []result{{http.StatusNotFound, protocol.ErrorNotFound}},
		},
		{
			name: "replayed nonce on the proxy session",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				x := p.newExchange(p.proxyHandoff())
				if status, body := p.proxyExchange(x); status != http.StatusOK {
					p.t.Fatalf("first exchange = %d %s, want 200", status, body)
				}

				return p.proxyExchange(x)
			},
			want: []result{{http.StatusUnauthorized, protocol.ErrorBadProof}},
		},
		{
			name: "proxy session body past the limit",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				raw := bytes.Repeat([]byte(" "), protocol.MaxBody+1)
				_, priv := newKeyPair(p.t)

				return p.sendSigned(http.MethodPost, protocol.PathProxySession, raw, sum(raw), signing{
					subject: sha256Hex(p.proxyHandoff()), key: priv, at: p.clock.now(), nonce: newNonce(p.t),
					release: "0.4.0", contentHash: "", server: "",
				})
			},
			want: []result{{http.StatusBadRequest, protocol.ErrorInvalid}},
		},
		{
			name: "upstream credential with a body",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()

				return proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, map[string]string{"pool": "x"}))
			},
			want: []result{{http.StatusBadRequest, protocol.ErrorInvalid}},
		},
		{
			name: "upstream credential after the takeover",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				proxy, _ := p.openProxy()
				p.takeOver()

				return proxy.do(proxy.post(protocol.PathProxyUpstreamCredential, nil))
			},
			want: []result{{http.StatusConflict, protocol.ErrorClaimLost}},
		},
		{
			name: "proxy handoff of the operation of another claim",
			run: func(_ *testing.T, p *proxyEnv) (int, []byte) {
				p.takeOver()

				return p.runner.call(opPath(p.work.Operation, "proxy-handoff"), nil)
			},
			want: []result{
				{http.StatusConflict, protocol.ErrorClaimLost},
				{http.StatusForbidden, protocol.ErrorRetired},
				{http.StatusNotFound, protocol.ErrorNotFound},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := newProxyEnv(t)
			status, body := tc.run(t, p)

			got := result{status: status, code: codeOf(t, body)}
			if !slices.Contains(tc.want, got) {
				t.Errorf("answer = %d %s, want one of %v", status, body, tc.want)
			}

			for _, secretText := range []string{proxyPassword, proxyUser, proxyUpstreamURL} {
				if strings.Contains(string(body), secretText) {
					t.Errorf("answer %s holds a part of the upstream proxy settings", body)
				}
			}
		})
	}
}

// TestProxyPathsLauncherRoutesRefuseAProxySession sends each launcher route a
// request with the proof of a proxy session.
func TestProxyPathsLauncherRoutesRefuseAProxySession(t *testing.T) {
	t.Parallel()

	p := newProxyEnv(t)
	proxy, _ := p.openProxy()

	for _, rt := range p.handler.Routes() {
		if rt.Scope != runnerapi.ScopeSessionKey {
			continue
		}

		path := strings.NewReplacer("{step}", "init", "{seq}", "0", "{part}", "plan-apply").Replace(rt.Path)

		c := proxy.post(path, map[string]string{})
		c.method = rt.Method

		if rt.Method == http.MethodGet {
			c.body = nil
		}

		if rt.Method == http.MethodPut {
			c.content = []byte("chunk")
			c.body = c.content
		}

		status, body := proxy.do(c)
		if status != http.StatusForbidden || codeOf(t, body) != protocol.ErrorWrongSessionKind {
			t.Errorf("%s %s with a proxy session = %d %s, want 403 %s", rt.Method, rt.Path, status, body,
				protocol.ErrorWrongSessionKind)
		}
	}

	refused := 0

	for _, e := range p.auditEntries() {
		if e.Outcome == audit.OutcomeDenied && e.Reason == protocol.ErrorWrongSessionKind {
			refused++
		}
	}

	if refused == 0 {
		t.Error("audit entries of the refused session kind = 0, want one for each refusal")
	}
}
