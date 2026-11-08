package dustpan_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/setting"
)

// Values of the LDAP sign-in tests of the server.
const (
	ldapAlicePassword = "right-password-of-alice"
	ldapWrongPassword = "wrong-password-of-alice"
	ldapAliceUUID     = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
)

// ldapServer is a server with two LDAP providers over one test directory:
// tls-ldap over TLS, and plain-ldap with no TLS to the name localhost.
type ldapServer struct {
	handler  http.Handler
	settings setting.Store
	dir      *ldaptest.Server
	logs     *syncBuffer
}

// newLDAPServer starts a test directory with the person alice and a server
// with its two LDAP providers. The logger of the server writes each record,
// debug included, to logs.
func newLDAPServer(t *testing.T) ldapServer {
	t.Helper()

	dir := ldaptest.Start(t)
	dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"entryUUID": {ldapAliceUUID}}, UserName: "alice", Password: ldapAlicePassword,
	})

	provider := func(id, url string) dustpan.LDAPProvider {
		p := ldapProvider()
		p.ID, p.URL, p.RootCAs = id, url, dir.CertPool()
		p.BindDN, p.BaseDN, p.BindPassword = ldaptest.ServiceDN, ldaptest.BaseDN, session.NewPassword(
			dir.ServicePassword(),
		)
		p.EmailAttribute, p.NameAttribute = "", ""

		return p
	}

	logs := &syncBuffer{mu: sync.Mutex{}, buf: strings.Builder{}}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{
		AddSource: false, Level: slog.LevelDebug, ReplaceAttr: nil,
	}))

	st := openAuditStore(t)
	opts := append(auditSinkServerOptions(t, st),
		dustpan.WithSettings(st.SettingStore(), plainconn.SettingDeclarations(), nil),
		dustpan.WithPublicURL("https://dustpan.example"), dustpan.WithLogger(logger),
		dustpan.WithLDAPLoopbackForTests(),
		dustpan.WithLDAPProvider(provider("tls-ldap", dir.LDAPSURL())),
		dustpan.WithLDAPProvider(provider("plain-ldap", dir.PlainNameURL())))

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	if err := srv.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	return ldapServer{handler: srv.Handler(), settings: st.SettingStore(), dir: dir, logs: logs}
}

// serve sends req to the server and returns the answer.
func (s ldapServer) serve(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)

	return rec
}

// signIn sends the password sign-in of alice with password to provider.
func (s ldapServer) signIn(t *testing.T, provider, password string) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(map[string]string{"userName": "alice", "password": password})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/dustpan/v1/sign-in/providers/"+provider+"/password", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")

	return s.serve(req)
}

// setPlain sets the installation setting egress.allowPlainConnections to value
// in the database layer that the server reads, with a fresh step, as an
// administrator with a recent sign-in does through the settings route.
func (s ldapServer) setPlain(t *testing.T, value string) {
	t.Helper()

	reg, err := setting.NewRegistry(plainconn.SettingDeclarations())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	stored, err := s.settings.Load(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}

	svc := setting.NewService(s.settings, reg, nil, nil, time.Now)
	patch := setting.Patch{{Key: plainconn.SettingAllowPlainConnections, Value: value, Delete: false}}

	_, err = svc.Patch(t.Context(), setting.InstallationScope(), patch, setting.PatchRequest{
		Entry: audit.Entry{
			At: time.Time{}, Namespace: "", Subject: "admin", IDPSubject: "", TokenID: "", Action: "",
			Object: "@server", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1",
			UserAgent: "", Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "",
			PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
		},
		IfMatch: stored.Revision, Fresh: true,
	})
	if err != nil {
		t.Fatalf("set %s to %s: %v", plainconn.SettingAllowPlainConnections, value, err)
	}
}

// TestLDAPSignInThroughTheServer signs alice in through the route of the
// server over TLS, and proves that the server reads the setting
// egress.allowPlainConnections at each sign-in: a connection with no TLS is
// refused, allowed after the setting is on, and refused again after it is
// off. No answer and no log line holds a password.
func TestLDAPSignInThroughTheServer(t *testing.T) {
	t.Parallel()

	s := newLDAPServer(t)

	steps := []struct {
		name, provider, password string
		plain                    string
		want                     int
	}{
		{name: "TLS", provider: "tls-ldap", password: ldapAlicePassword, plain: "", want: http.StatusNoContent},
		{
			name:     "TLS, wrong password",
			provider: "tls-ldap",
			password: ldapWrongPassword,
			plain:    "",
			want:     http.StatusUnauthorized,
		},
		{
			name:     "plain, setting off",
			provider: "plain-ldap",
			password: ldapAlicePassword,
			plain:    "",
			want:     http.StatusServiceUnavailable,
		},
		{
			name:     "plain, setting on",
			provider: "plain-ldap",
			password: ldapAlicePassword,
			plain:    "true",
			want:     http.StatusNoContent,
		},
		{
			name:     "plain, setting off again",
			provider: "plain-ldap",
			password: ldapAlicePassword,
			plain:    "false",
			want:     http.StatusServiceUnavailable,
		},
	}

	for _, step := range steps {
		if step.plain != "" {
			s.setPlain(t, step.plain)
		}

		rec := s.signIn(t, step.provider, step.password)
		if rec.Code != step.want {
			t.Fatalf("%s: sign-in = %d %s, want %d", step.name, rec.Code, rec.Body.String(), step.want)
		}

		checkNoLDAPSecret(t, s, step.name+" answer", rec.Body.String())
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/server/sign-in-providers", http.NoBody)
	req.Header.Set("Authorization", "Bearer "+adminToken)

	list := s.serve(req)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"tls-ldap"`) ||
		!strings.Contains(list.Body.String(), `"bindPasswordSet":true`) {
		t.Fatalf("provider list = %d %s, want the LDAP providers", list.Code, list.Body.String())
	}

	checkNoLDAPSecret(t, s, "provider list", list.Body.String())
	checkNoLDAPSecret(t, s, "logs", s.logs.String())
}

// checkNoLDAPSecret fails when text holds a password of the test.
func checkNoLDAPSecret(t *testing.T, s ldapServer, name, text string) {
	t.Helper()

	for _, secret := range []string{ldapAlicePassword, ldapWrongPassword, s.dir.ServicePassword()} {
		if strings.Contains(text, secret) {
			t.Fatalf("the %s holds a password", name)
		}
	}
}
