package issuersvc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

const (
	issuerHost = "https://dustpan.example.com"
	alice      = "alice"
)

// scopedSealer binds each ciphertext to its scope, as the real sealer does.
type scopedSealer struct{}

func (scopedSealer) Encrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))
	for i, p := range in {
		out[i] = append([]byte(sc.String()+"|"), p...)
	}

	return out, nil
}

func (scopedSealer) Decrypt(_ context.Context, sc secret.Scope, in [][]byte) ([][]byte, error) {
	out := make([][]byte, len(in))

	for i, c := range in {
		plain, ok := strings.CutPrefix(string(c), sc.String()+"|")
		if !ok {
			return nil, secret.ErrDecrypt
		}

		out[i] = []byte(plain)
	}

	return out, nil
}

// grants allows each subject the listed actions.
type grants map[string][]identity.Action

func (g grants) Authorize(_ context.Context, p identity.Principal, a identity.Action, o identity.Object) error {
	if slices.Contains(g[p.Subject], a) {
		return nil
	}

	return fmt.Errorf("%w: %s on %s", identity.ErrDenied, a, o)
}

func (grants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

// clock is a time source that a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// outbox collects the notices of the service.
type outbox struct {
	mu    sync.Mutex
	notes []notify.Notification
}

func (o *outbox) Append(_ context.Context, _ namespace.Fence, ns []notify.Notification) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	for i := range ns {
		if err := ns[i].Validate(); err != nil {
			return err
		}
	}

	o.notes = append(o.notes, ns...)

	return nil
}

func (o *outbox) types() []notify.Type {
	o.mu.Lock()
	defer o.mu.Unlock()

	out := make([]notify.Type, 0, len(o.notes))
	for i := range o.notes {
		out = append(out, o.notes[i].Type)
	}

	return out
}

// harness is a service over a real SQLite store with one namespace.
type harness struct {
	t      *testing.T
	svc    *issuersvc.Service
	store  *sqlite.Store
	clock  *clock
	outbox *outbox
	ns     namespace.Namespace
	admin  *profile.Actor
	reader *profile.Actor
	nobody *profile.Actor
}

func allActions() []identity.Action {
	return []identity.Action{identity.ActionNamespaceManageIssuer, identity.ActionNamespaceRead}
}

// options changes the config of a harness.
type options func(*issuersvc.Config, *harness)

func newHarness(t *testing.T, opts ...options) *harness {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	h := &harness{
		t: t, svc: nil, store: st,
		clock:  &clock{mu: sync.Mutex{}, now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)},
		outbox: &outbox{mu: sync.Mutex{}, notes: nil},
		ns: namespace.Namespace{
			CreatedAt: time.Time{}, ID: "", Name: "", PrimaryInstallation: "",
			Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
			FailoverVersion: 0,
		},
		admin: nil, reader: nil, nobody: nil,
	}

	h.ns = h.addNamespace("acme")
	h.admin, h.reader, h.nobody = h.actor(alice), h.actor("rita"), h.actor("nobody")

	cfg := issuersvc.Config{
		Store: st.Issuer(), Namespaces: st, Sealer: scopedSealer{}, Notifier: h.outbox, Fetcher: nil,
		Authorizer: grants{
			alice: allActions(), "rita": {identity.ActionNamespaceRead},
		},
		IssuerBaseURL: issuerHost, PublicURL: "https://api.example.com", Now: h.clock.Now, Log: nil,
	}

	for _, o := range opts {
		o(&cfg, h)
	}

	h.svc, err = issuersvc.NewService(cfg)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return h
}

func (h *harness) addNamespace(name string) namespace.Namespace {
	h.t.Helper()

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: h.clock.Now(), Name: namespace.Name(name), PrimaryInstallation: "local",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := h.store.CreateNamespace(h.t.Context(), ns); err != nil {
		h.t.Fatalf("create namespace: %v", err)
	}

	return ns
}

func (h *harness) actor(subject string) *profile.Actor {
	return &profile.Actor{
		Principal: identity.Principal{
			Subject: subject, DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
		},
		By: auditBy(""),
	}
}

// open returns the context of an open in ns for a subject.
func (h *harness) open(ns namespace.Namespace, subject string) profile.OpenContext {
	return profile.OpenContext{
		Claims:    map[string]any{"access": "write", "tool": "pulumi"},
		By:        auditBy(alice),
		Namespace: ns.Name, NamespaceID: ns.ID, FailoverVersion: ns.FailoverVersion, Subject: subject,
		Profile: "aws", ProfileID: "prf-1",
	}
}

const defaultSubject = "namespace:acme:access:write"

// login signs a token for ns with the default open.
func (h *harness) login(aud string, lifetime time.Duration) (profile.Token, error) {
	h.t.Helper()

	return h.svc.Login(h.t.Context(), h.open(h.ns, defaultSubject), "aws", aud, lifetime)
}

// mustLogin signs a token and fails the test on an error.
func (h *harness) mustLogin() profile.Token {
	h.t.Helper()

	tok, err := h.login("sts.amazonaws.com", 5*time.Minute)
	if err != nil {
		h.t.Fatalf("Login: %v", err)
	}

	return tok
}

func (h *harness) jwks(ns namespace.Namespace) []byte {
	h.t.Helper()

	doc, err := h.svc.JWKS(h.t.Context(), ns.ID)
	if err != nil {
		h.t.Fatalf("JWKS: %v", err)
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		h.t.Fatalf("encode JWKS: %v", err)
	}

	return raw
}

func (h *harness) issuer(ns namespace.Namespace) string {
	return issuerHost + "/oidc/" + ns.ID.String()
}

// freshNamespace reads ns again, after a write that changed its fence.
func (h *harness) keysOf(ns namespace.Namespace) []profile.Key {
	h.t.Helper()

	keys, err := h.store.Issuer().Live(h.t.Context(), ns.Name)
	if err != nil {
		h.t.Fatalf("Live: %v", err)
	}

	return keys
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()

	if err == nil || !errors.Is(err, want) {
		t.Errorf("%s: error = %v, want %v", what, err, want)
	}
}

// issuerConfig is the config of the service under test.
type issuerConfig = issuersvc.Config

// auditBy returns the audit template of a person who acts through the API.
func auditBy(subject string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: subject, IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, PrincipalKind: audit.PrincipalKindPerson, JobID: "", Reason: "",
	}
}

// patch changes the settings of the namespace as a.
func (h *harness) patch(
	a *profile.Actor, p profile.SettingsPatch, version int64, fresh func() error,
) (profile.IssuerSettings, error) {
	h.t.Helper()

	req := &issuersvc.PatchRequest{Fresh: fresh, Patch: p, IfVersion: version}

	got, err := h.svc.Patch(h.t.Context(), a, h.ns.Name, req)
	if err != nil {
		return profile.IssuerSettings{}, fmt.Errorf("patch: %w", err)
	}

	return got, nil
}
