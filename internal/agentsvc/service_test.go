package agentsvc_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/agentsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
)

// secretMark is the value of each secret item of the tests. No audit entry
// may hold it. It is clearly fake and holds no credential text.
const secretMark = "test-value-1"

// errPolicyStoreDown is the failure of the authorizer of the tests that
// refuses with an error instead of a denial.
var errPolicyStoreDown = errors.New("policy store down")

// testRef is a secret reference of the right form.
const testRef = agent.SecretRef("itm-abc123")

// fakeSecrets is an agent.SecretItems that records each call and each entry.
type fakeSecrets struct {
	mu      sync.Mutex
	calls   int
	entries []audit.Entry
	err     error
}

func (f *fakeSecrets) Create(_ context.Context, name string, _ []byte, e audit.Entry) (agent.SecretItem, error) {
	if err := f.record(&e); err != nil {
		return agent.SecretItem{}, err
	}

	return agent.SecretItem{Ref: testRef, Name: name, CreatedAt: 1, ChangedAt: 1, LastUsedAt: 0}, nil
}

func (f *fakeSecrets) List(context.Context) ([]agent.SecretItem, error) {
	if err := f.record(nil); err != nil {
		return nil, err
	}

	return []agent.SecretItem{{Ref: testRef, Name: "key", CreatedAt: 1, ChangedAt: 1, LastUsedAt: 0}}, nil
}

func (f *fakeSecrets) Get(context.Context, agent.SecretRef) (agent.SecretItem, error) {
	return agent.SecretItem{}, f.record(nil)
}

func (f *fakeSecrets) Replace(
	_ context.Context,
	ref agent.SecretRef,
	_ []byte,
	e audit.Entry,
) (agent.SecretItem, error) {
	if err := f.record(&e); err != nil {
		return agent.SecretItem{}, err
	}

	return agent.SecretItem{Ref: ref, Name: "key", CreatedAt: 1, ChangedAt: 2, LastUsedAt: 0}, nil
}

func (f *fakeSecrets) Delete(_ context.Context, _ agent.SecretRef, e audit.Entry) error {
	return f.record(&e)
}

func (f *fakeSecrets) Open(context.Context, agent.SecretRef) ([]byte, error) {
	return nil, f.record(nil)
}

func (f *fakeSecrets) record(e *audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if e != nil {
		f.entries = append(f.entries, *e)
	}

	return f.err
}

// fakeConnections is an agent.ConnectionStore that records each call.
type fakeConnections struct {
	mu      sync.Mutex
	calls   int
	entries []audit.Entry
	spec    agent.ConnectionSpec
	err     error
}

func (f *fakeConnections) Current(context.Context) (agent.ConnectionSpec, int64, error) {
	if err := f.record(nil); err != nil {
		return agent.ConnectionSpec{}, 0, err
	}

	return f.spec, 3, nil
}

func (f *fakeConnections) Pending(context.Context, agent.ProbeRef) (agent.ConnectionSpec, int64, error) {
	return agent.ConnectionSpec{}, 0, f.record(nil)
}

func (f *fakeConnections) PendingAll(context.Context) ([]agent.PendingDocument, error) {
	return nil, f.record(nil)
}

func (f *fakeConnections) PutPending(_ context.Context, _ agent.ProbeRef, _ agent.ConnectionSpec, e audit.Entry) error {
	return f.record(&e)
}

func (f *fakeConnections) Promote(_ context.Context, _ agent.ProbeRef, e audit.Entry) error {
	return f.record(&e)
}

func (f *fakeConnections) Delete(_ context.Context, e audit.Entry) error {
	return f.record(&e)
}

func (f *fakeConnections) record(e *audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	if e != nil {
		f.entries = append(f.entries, *e)
	}

	return f.err
}

// fakeAllowlist is an agent.EgressAllowlistStore that records each call.
type fakeAllowlist struct {
	mu      sync.Mutex
	calls   int
	entries []audit.Entry
	stored  egress.Allowlist
}

func (f *fakeAllowlist) Get(context.Context) (egress.Allowlist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	return f.stored, nil
}

func (f *fakeAllowlist) Set(_ context.Context, a egress.Allowlist, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	f.stored = a
	f.entries = append(f.entries, e)

	return nil
}

// authorizer allows the actions in allow and refuses the rest. A non-nil
// fail is returned for each call instead.
type authorizer struct {
	allow map[identity.Action]bool
	fail  error
}

func (a authorizer) Authorize(
	_ context.Context,
	p identity.Principal,
	action identity.Action,
	obj identity.Object,
) error {
	if a.fail != nil {
		return a.fail
	}

	if !obj.IsServer() || !a.allow[action] {
		return fmt.Errorf("%w: %s may not %s on %q", identity.ErrDenied, p.Subject, action, obj.String())
	}

	return nil
}

func (authorizer) ListObjects(_ context.Context, p identity.Principal, _ identity.Action) (identity.Scope, error) {
	return identity.AdminScope(p), nil
}

// allowAll allows both actions of the service.
func allowAll() authorizer {
	return authorizer{allow: map[identity.Action]bool{
		identity.ActionServerManageAgent: true, identity.ActionServerManageSettings: true,
	}, fail: nil}
}

// env is one service with its fakes.
type env struct {
	svc       *agentsvc.Service
	secrets   *fakeSecrets
	conns     *fakeConnections
	allowlist *fakeAllowlist
}

func newEnv(t *testing.T, authz identity.Authorizer) env {
	t.Helper()

	spec, err := agent.ParseConnectionSpec([]byte(`{"version":1,"format":"messages",` +
		`"baseURL":"https://models.example.com/v1","model":"m-1",` +
		`"auth":{"header":"x-api-key","prefix":"","secret":"itm-abc123"},` +
		`"outputTokens":{"field":"max_tokens","max":1024},"tools":{"choice":"auto","parallel":"omit"}}`))
	if err != nil {
		t.Fatalf("parse document: %v", err)
	}

	e := env{
		svc:     nil,
		secrets: &fakeSecrets{mu: sync.Mutex{}, calls: 0, entries: nil, err: nil},
		conns:   &fakeConnections{mu: sync.Mutex{}, calls: 0, entries: nil, spec: spec, err: nil},
		allowlist: &fakeAllowlist{
			mu:      sync.Mutex{},
			calls:   0,
			entries: nil,
			stored:  egress.Allowlist{Hosts: nil, CIDRs: nil},
		},
	}

	svc, err := agentsvc.New(agentsvc.Config{
		Secrets: e.secrets, Connection: e.conns, Allowlist: e.allowlist, Authorizer: authz,
		Now: func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	e.svc = svc

	return e
}

// storeCalls returns the count of the calls of the three fakes.
func (e env) storeCalls() int {
	e.secrets.mu.Lock()
	defer e.secrets.mu.Unlock()

	e.conns.mu.Lock()
	defer e.conns.mu.Unlock()

	e.allowlist.mu.Lock()
	defer e.allowlist.mu.Unlock()

	return e.secrets.calls + e.conns.calls + e.allowlist.calls
}

// allEntries returns the audit entries that the fakes got.
func (e env) allEntries() []audit.Entry {
	e.secrets.mu.Lock()
	defer e.secrets.mu.Unlock()

	e.conns.mu.Lock()
	defer e.conns.mu.Unlock()

	e.allowlist.mu.Lock()
	defer e.allowlist.mu.Unlock()

	out := append([]audit.Entry(nil), e.secrets.entries...)
	out = append(out, e.conns.entries...)

	return append(out, e.allowlist.entries...)
}

func principal() identity.Principal {
	return identity.Principal{
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Subject: "admin", DisplayName: "Admin",
		Admin: false, TokenID: "", TokenExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}
}

// baseEntry is the entry of the caller that a handler gives the service.
func baseEntry() audit.Entry {
	return audit.Entry{
		At:            time.Time{},
		Namespace:     "",
		Subject:       "admin",
		IDPSubject:    "",
		TokenID:       "",
		Action:        "",
		Object:        "",
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          "",
		Outcome:       "",
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
		Origin:        nil,
	}
}

func publicRule() egress.Allowlist {
	return egress.Allowlist{
		Hosts: []string{"models.example.com"},
		CIDRs: []egress.CIDRRule{{CIDR: netip.MustParsePrefix("10.20.0.0/16"), Port: 443, HTTP: false}},
	}
}

// call is one method of the service with valid input.
type call struct {
	name string
	run  func(ctx context.Context, s *agentsvc.Service) error
}

func calls() []call {
	p, base := principal(), baseEntry()

	return []call{
		{"create secret", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.CreateSecret(ctx, p, base, "key", []byte(secretMark))

			return err
		}},
		{"list secrets", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.ListSecrets(ctx, p)

			return err
		}},
		{"replace secret", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.ReplaceSecret(ctx, p, base, testRef, []byte(secretMark))

			return err
		}},
		{"delete secret", func(ctx context.Context, s *agentsvc.Service) error {
			return s.DeleteSecret(ctx, p, base, testRef)
		}},
		{"read connection", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.Connection(ctx, p)

			return err
		}},
		{"disconnect", func(ctx context.Context, s *agentsvc.Service) error {
			return s.Disconnect(ctx, p, base)
		}},
		{"formats", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.Formats(ctx, p)

			return err
		}},
		{"read allowlist", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.Allowlist(ctx, p)

			return err
		}},
		{"set allowlist", func(ctx context.Context, s *agentsvc.Service) error {
			return s.SetAllowlist(ctx, p, base, publicRule())
		}},
	}
}

func TestNew_MissingDependency(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allowAll())

	tests := []struct {
		name string
		cfg  agentsvc.Config
	}{
		{
			"no secrets",
			agentsvc.Config{
				Secrets:    nil,
				Connection: e.conns,
				Allowlist:  e.allowlist,
				Authorizer: allowAll(),
				Now:        nil,
			},
		},
		{
			"no connection",
			agentsvc.Config{
				Secrets:    e.secrets,
				Connection: nil,
				Allowlist:  e.allowlist,
				Authorizer: allowAll(),
				Now:        nil,
			},
		},
		{
			"no allowlist",
			agentsvc.Config{Secrets: e.secrets, Connection: e.conns, Allowlist: nil, Authorizer: allowAll(), Now: nil},
		},
		{
			"no authorizer",
			agentsvc.Config{Secrets: e.secrets, Connection: e.conns, Allowlist: e.allowlist, Authorizer: nil, Now: nil},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := agentsvc.New(tc.cfg); !errors.Is(err, agentsvc.ErrMissingDependency) {
				t.Fatalf("New = %v, want ErrMissingDependency", err)
			}
		})
	}
}

// TestService_Allowed runs each method with both actions allowed.
func TestService_Allowed(t *testing.T) {
	t.Parallel()

	for _, c := range calls() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, allowAll())
			if err := c.run(t.Context(), e.svc); err != nil {
				t.Fatalf("%s = %v, want nil", c.name, err)
			}
		})
	}
}

// TestService_DeniedNeverReachesStore is the hostile table of the
// authorization layer of the service: a refused or failed authorization
// stops each method before a store call.
func TestService_DeniedNeverReachesStore(t *testing.T) {
	t.Parallel()

	authorizers := []struct {
		name  string
		authz authorizer
		want  error
	}{
		{"no grant", authorizer{allow: nil, fail: nil}, identity.ErrDenied},
		{"authorizer error", authorizer{allow: nil, fail: errPolicyStoreDown}, nil},
		{"only the other action", authorizer{allow: map[identity.Action]bool{
			identity.ActionServerReadSettings: true, identity.ActionServerManageSignIn: true,
		}, fail: nil}, identity.ErrDenied},
	}

	for _, a := range authorizers {
		for _, c := range calls() {
			t.Run(a.name+"/"+c.name, func(t *testing.T) {
				t.Parallel()

				checkDeniedCall(t, a.authz, c, a.want)
			})
		}
	}
}

// checkDeniedCall runs one service method with an authorizer that refuses or
// fails, and checks that the call fails before a store call.
func checkDeniedCall(t *testing.T, authz authorizer, c call, want error) {
	t.Helper()

	e := newEnv(t, authz)

	err := c.run(t.Context(), e.svc)
	if err == nil {
		t.Fatalf("%s = nil, want a refusal", c.name)
	}

	if want != nil && !errors.Is(err, want) {
		t.Fatalf("%s = %v, want %v", c.name, err, want)
	}

	if n := e.storeCalls(); n != 0 {
		t.Fatalf("%d store calls after a refusal, want 0", n)
	}
}

func TestAuthorize_UnknownAction(t *testing.T) {
	t.Parallel()

	e := newEnv(t, authorizer{allow: map[identity.Action]bool{identity.ActionServerManageSignIn: true}, fail: nil})

	err := e.svc.Authorize(t.Context(), principal(), identity.ActionServerManageSignIn)
	if !errors.Is(err, agentsvc.ErrUnknownAction) {
		t.Fatalf("Authorize = %v, want ErrUnknownAction", err)
	}
}

// TestService_HostileInput is the hostile table of the input checks of the
// service: each bad input fails before a store call, as if the handler let
// it pass.
func TestService_HostileInput(t *testing.T) {
	t.Parallel()

	p, base := principal(), baseEntry()

	tests := []struct {
		name string
		run  func(ctx context.Context, s *agentsvc.Service) error
	}{
		{"create with empty name", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.CreateSecret(ctx, p, base, "", []byte(secretMark))

			return err
		}},
		{"create with control character in name", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.CreateSecret(ctx, p, base, "key\n", []byte(secretMark))

			return err
		}},
		{"create with empty value", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.CreateSecret(ctx, p, base, "key", nil)

			return err
		}},
		{"create with oversize value", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.CreateSecret(ctx, p, base, "key", []byte(strings.Repeat("a", 16*1024+1)))

			return err
		}},
		{"replace with bad reference", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.ReplaceSecret(ctx, p, base, "../itm-1", []byte(secretMark))

			return err
		}},
		{"replace with long reference", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.ReplaceSecret(ctx, p, base, agent.SecretRef("itm-"+strings.Repeat("a", 61)), []byte(secretMark))

			return err
		}},
		{"replace with empty value", func(ctx context.Context, s *agentsvc.Service) error {
			_, err := s.ReplaceSecret(ctx, p, base, testRef, nil)

			return err
		}},
		{"delete with bad reference", func(ctx context.Context, s *agentsvc.Service) error {
			return s.DeleteSecret(ctx, p, base, "itm-")
		}},
		{"allowlist with loopback rule", func(ctx context.Context, s *agentsvc.Service) error {
			return s.SetAllowlist(ctx, p, base, egress.Allowlist{Hosts: nil, CIDRs: []egress.CIDRRule{
				{CIDR: netip.MustParsePrefix("127.0.0.0/8"), Port: 443, HTTP: false},
			}})
		}},
		{"allowlist with metadata rule", func(ctx context.Context, s *agentsvc.Service) error {
			return s.SetAllowlist(ctx, p, base, egress.Allowlist{Hosts: nil, CIDRs: []egress.CIDRRule{
				{CIDR: netip.MustParsePrefix("169.254.169.254/32"), Port: 80, HTTP: true},
			}})
		}},
		{"allowlist with too many hosts", func(ctx context.Context, s *agentsvc.Service) error {
			hosts := make([]string, 65)
			for i := range hosts {
				hosts[i] = fmt.Sprintf("h%d.example.com", i)
			}

			return s.SetAllowlist(ctx, p, base, egress.Allowlist{Hosts: hosts, CIDRs: nil})
		}},
		{"allowlist with port 0", func(ctx context.Context, s *agentsvc.Service) error {
			return s.SetAllowlist(ctx, p, base, egress.Allowlist{Hosts: nil, CIDRs: []egress.CIDRRule{
				{CIDR: netip.MustParsePrefix("10.0.0.0/16"), Port: 0, HTTP: false},
			}})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, allowAll())
			if err := tc.run(t.Context(), e.svc); err == nil {
				t.Fatalf("%s = nil, want an error", tc.name)
			}

			if n := e.storeCalls(); n != 0 {
				t.Fatalf("%d store calls for hostile input, want 0", n)
			}
		})
	}
}

// TestService_Entries checks the audit entry of each change: the kind change
// on @server, the action, the object, and no secret value.
func TestService_Entries(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allowAll())
	ctx := t.Context()

	for _, c := range calls() {
		if err := c.run(ctx, e.svc); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
	}

	want := map[string]identity.Action{
		agentsvc.ObjectSecretItems:         identity.ActionServerManageAgent,
		agentsvc.SecretItemObject(testRef): identity.ActionServerManageAgent,
		agentsvc.ObjectConnection:          identity.ActionServerManageAgent,
		agentsvc.ObjectEgressAllowlist:     identity.ActionServerManageSettings,
	}

	entries := e.allEntries()
	if len(entries) != 5 {
		t.Fatalf("%d entries, want 5: create, replace, delete, disconnect, allowlist", len(entries))
	}

	for i := range entries {
		checkServiceEntry(t, i, entries[i], want)
	}
}

// checkServiceEntry checks one audit entry of a change: the kind change on
// @server, the action, the object, and no secret value.
func checkServiceEntry(t *testing.T, i int, got audit.Entry, want map[string]identity.Action) {
	t.Helper()

	action, ok := want[got.Object]
	if !ok || got.Action != string(action) {
		t.Errorf("entry %d object %q action %q, want one of %v", i, got.Object, got.Action, want)
	}

	if got.Namespace != audit.ServerScope || got.Kind != audit.KindChange || got.Outcome != audit.OutcomeSuccess {
		t.Errorf("entry %d = %s %s %s, want @server change success", i, got.Namespace, got.Kind, got.Outcome)
	}

	if got.At.IsZero() || got.Subject != "admin" {
		t.Errorf("entry %d has no time or no subject: %+v", i, got)
	}

	if err := got.Validate(); err != nil {
		t.Errorf("entry %d is not valid: %v", i, err)
	}

	if strings.Contains(fmt.Sprintf("%+v", got), secretMark) {
		t.Errorf("entry %d holds the secret value: %+v", i, got)
	}
}

// TestService_StoreErrorsPass checks that a store error reaches the caller
// with its sentinel, so the handler maps it and fails closed.
func TestService_StoreErrorsPass(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allowAll())
	e.secrets.err = agent.ErrSecretInUse
	e.conns.err = agent.ErrDisabled

	err := e.svc.DeleteSecret(t.Context(), principal(), baseEntry(), testRef)
	if !errors.Is(err, agent.ErrSecretInUse) {
		t.Fatalf("DeleteSecret = %v, want ErrSecretInUse", err)
	}

	if _, err := e.svc.Connection(t.Context(), principal()); !errors.Is(err, agent.ErrDisabled) {
		t.Fatalf("Connection = %v, want ErrDisabled", err)
	}

	if err := e.svc.Disconnect(t.Context(), principal(), baseEntry()); !errors.Is(err, agent.ErrDisabled) {
		t.Fatalf("Disconnect = %v, want ErrDisabled", err)
	}
}

func TestFormats(t *testing.T) {
	t.Parallel()

	e := newEnv(t, allowAll())

	got, err := e.svc.Formats(t.Context(), principal())
	if err != nil {
		t.Fatalf("Formats: %v", err)
	}

	if strings.Join(got, ",") != "chat-completions,messages" {
		t.Fatalf("Formats = %v, want chat-completions and messages", got)
	}
}
