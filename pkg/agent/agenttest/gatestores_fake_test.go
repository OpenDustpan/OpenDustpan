package agenttest_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/agent/agenttest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func TestGateStoresSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunGateStores(t, func(t *testing.T) agenttest.GateStoresHarness {
		t.Helper()

		f := &fakeGateStores{
			mu: sync.Mutex{}, windows: nil, maintenance: false, consents: map[string]agent.Consent{},
			installation: nil, optIns: map[string]agent.PersonOptIns{},
		}

		return agenttest.GateStoresHarness{
			Windows:     fakeWindows{f},
			Consents:    fakeConsents{f},
			Settings:    fakeSettings{f},
			Namespace:   "acme",
			Fence:       namespace.Fence{Namespace: "acme", FailoverVersion: 1},
			Maintenance: func(*testing.T) bool { return f.inMaintenance() },
		}
	})
}

// fakeGateStores holds the rows of the three gate stores in memory, under
// one lock, as one database does. It ignores fences and audit entries.
type fakeGateStores struct {
	mu           sync.Mutex
	windows      []agent.SecretWindow
	maintenance  bool
	consents     map[string]agent.Consent
	installation *agent.InstallationSettings
	optIns       map[string]agent.PersonOptIns
}

func (f *fakeGateStores) inMaintenance() bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.maintenance
}

type fakeWindows struct{ f *fakeGateStores }

func (s fakeWindows) JoinOrOpen(_ context.Context, instance string, at time.Time, _ audit.Entry) (
	agent.SecretWindow, bool, error,
) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	if n := len(s.f.windows); n > 0 && s.f.windows[n-1].Open() {
		w := &s.f.windows[n-1]
		if !slices.Contains(w.Instances, instance) {
			w.Instances = append(w.Instances, instance)
		}

		return *w, false, nil
	}

	w := agent.SecretWindow{
		ID: fmt.Sprintf("win-%d", len(s.f.windows)+1), OpenedAt: at, Instances: []string{instance},
		Acceptance: nil, ClosedAt: time.Time{},
	}
	s.f.windows = append(s.f.windows, w)
	s.f.maintenance = true

	return w, true, nil
}

func (s fakeWindows) Accept(_ context.Context, id string, a agent.Acceptance, _ audit.Entry) (
	agent.SecretWindow, error,
) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	w, err := s.open(id)
	if err != nil {
		var zero agent.SecretWindow

		return zero, err
	}

	if err := a.Validate(); err != nil {
		return *w, fmt.Errorf("accept: %w", err)
	}

	w.Acceptance = &a
	s.f.maintenance = false

	return *w, nil
}

func (s fakeWindows) Close(_ context.Context, id string, at time.Time) (agent.SecretWindow, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	w, err := s.open(id)
	if err != nil {
		var zero agent.SecretWindow

		return zero, err
	}

	w.ClosedAt = at
	s.f.maintenance = false

	return *w, nil
}

func (s fakeWindows) ForInstance(_ context.Context, instance string) (agent.SecretWindow, bool, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	for i := range slices.Backward(s.f.windows) {
		if slices.Contains(s.f.windows[i].Instances, instance) {
			return s.f.windows[i], true, nil
		}
	}

	var none agent.SecretWindow

	return none, false, nil
}

// open returns the open window id. The caller holds the lock.
func (s fakeWindows) open(id string) (*agent.SecretWindow, error) {
	for i := range s.f.windows {
		if s.f.windows[i].ID != id {
			continue
		}

		if !s.f.windows[i].Open() {
			return nil, agent.ErrWindowClosed
		}

		return &s.f.windows[i], nil
	}

	return nil, agent.ErrWindowNotFound
}

type fakeConsents struct{ f *fakeGateStores }

func (s fakeConsents) Create(_ context.Context, c agent.Consent) (agent.Consent, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	c.ID, c.ApprovedAt, c.UsedAt = fmt.Sprintf("cns-%d", len(s.f.consents)+1), time.Time{}, time.Time{}
	s.f.consents[c.ID] = c

	return c, nil
}

func (s fakeConsents) Get(_ context.Context, id string) (agent.Consent, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	c, ok := s.f.consents[id]
	if !ok {
		return c, agent.ErrConsentNotFound
	}

	return c, nil
}

func (s fakeConsents) Approve(_ context.Context, id string, a agent.Acceptance, _ audit.Entry) (agent.Consent, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	c, ok := s.f.consents[id]

	switch {
	case !ok:
		return c, agent.ErrConsentNotFound
	case a.Validate() != nil:
		return c, fmt.Errorf("approve: %w", a.Validate())
	case a.Subject != c.Subject || !c.ApprovedAt.IsZero() || a.At.Sub(c.CreatedAt) > agent.ConsentTTL:
		return c, agent.ErrConsentNotUsable
	default:
	}

	c.ApprovedAt = a.At
	s.f.consents[id] = c

	return c, nil
}

func (s fakeConsents) Use(_ context.Context, u agent.ConsentUse) error {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	c, ok := s.f.consents[u.ID]
	if !ok {
		return agent.ErrConsentNotFound
	}

	if err := c.Usable(u); err != nil {
		return fmt.Errorf("use: %w", err)
	}

	c.UsedAt = u.At
	s.f.consents[u.ID] = c

	return nil
}

func (s fakeConsents) SessionRevealed(_ context.Context, sessionID string) (bool, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	for id := range s.f.consents {
		if c := s.f.consents[id]; c.SessionID == sessionID && !c.UsedAt.IsZero() {
			return true, nil
		}
	}

	return false, nil
}

type fakeSettings struct{ f *fakeGateStores }

func (fakeSettings) Connection(context.Context) (agent.Connection, error) {
	var none agent.Connection

	return none, agent.ErrDisabled
}

func (fakeSettings) PutConnection(context.Context, agent.Connection, []byte, audit.Entry) error {
	return nil
}

func (fakeSettings) DeleteConnection(context.Context, audit.Entry) error { return agent.ErrDisabled }

func (s fakeSettings) Installation(context.Context) (agent.InstallationSettings, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	if s.f.installation == nil {
		return agent.InstallationSettings{Limits: agent.DefaultLimits(), DirectChanges: false, MCPEnabled: false}, nil
	}

	return *s.f.installation, nil
}

func (s fakeSettings) PutInstallation(_ context.Context, in agent.InstallationSettings, _ audit.Entry) error {
	if err := in.Limits.Validate(); err != nil {
		return fmt.Errorf("installation settings: %w", err)
	}

	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	s.f.installation = &in

	return nil
}

func (fakeSettings) Namespace(context.Context, namespace.Name) (agent.NamespaceSettings, error) {
	var zero agent.NamespaceSettings

	return zero, nil
}

func (fakeSettings) PutNamespace(
	_ context.Context, _ namespace.Name, ns agent.NamespaceSettings, _ namespace.Fence, _ audit.Entry,
) error {
	return validOptIns(ns.DirectOptIn, ns.SecretOptIn)
}

func (s fakeSettings) OptIns(_ context.Context, subject string) (agent.PersonOptIns, error) {
	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	return s.f.optIns[subject], nil
}

func (s fakeSettings) PutOptIns(_ context.Context, subject string, o agent.PersonOptIns, _ audit.Entry) error {
	if err := validOptIns(o.Direct, o.Secrets); err != nil {
		return err
	}

	s.f.mu.Lock()
	defer s.f.mu.Unlock()

	s.f.optIns[subject] = o

	return nil
}

func (fakeSettings) AccountSecretOptIn(context.Context) (agent.AccountSecretOptIn, bool, error) {
	var none agent.AccountSecretOptIn

	return none, false, nil
}

func (fakeSettings) PutAccountSecretOptIn(_ context.Context, o agent.AccountSecretOptIn, _ audit.Entry) error {
	return validOptIns(&o.Acceptance)
}

func validOptIns(opts ...*agent.Acceptance) error {
	for _, a := range opts {
		if a == nil {
			continue
		}

		if err := a.Validate(); err != nil {
			return fmt.Errorf("opt-in: %w", err)
		}
	}

	return nil
}
