package agenttest

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// GateStoresHarness is one fresh, empty database for one case of the gate
// store suite.
type GateStoresHarness struct {
	Windows  agent.WindowStore
	Consents agent.ConsentStore
	Settings agent.SettingsStore
	// Fence is the fence of the namespace Namespace, which exists.
	Namespace namespace.Name
	Fence     namespace.Fence
	// Maintenance reports whether the installation is in maintenance mode.
	Maintenance func(t *testing.T) bool
}

// GateStoresFactory returns a fresh harness for one case.
type GateStoresFactory func(t *testing.T) GateStoresHarness

// RunGateStores runs the suite of the window, consent and settings stores
// against the stores that f makes.
func RunGateStores(t *testing.T, f GateStoresFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h GateStoresHarness)
	}{
		{name: "one acceptance covers the window", run: caseWindowAccept},
		{name: "a start after the acceptance opens a new window", run: caseWindowReopen},
		{name: "acceptance needs the text and a fresh MFA step", run: caseWindowAcceptInvalid},
		{name: "window closed with no acceptance", run: caseWindowClose},
		{name: "consent is used once", run: caseConsentOnce},
		{name: "consent needs an approval", run: caseConsentApproval},
		{name: "consent of another call", run: caseConsentOther},
		{name: "settings defaults", run: caseSettingsDefaults},
		{name: "settings refuse a zero limit", run: caseSettingsZeroLimit},
		{name: "opt-in needs a valid acceptance", run: caseSettingsOptIn},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			c.run(t, f(t))
		})
	}
}

// Accept returns a valid acceptance of subject at now.
func Accept(subject string, now time.Time) agent.Acceptance {
	return agent.Acceptance{Subject: subject, Text: agent.AcceptanceText, MFAAt: now.Add(-time.Minute), At: now}
}

// TestEntry returns an audit entry of action by subject in ns.
func TestEntry(ns namespace.Name, subject, action string) audit.Entry {
	var e audit.Entry

	e.At, e.Namespace, e.Subject, e.Action = time.Now(), ns, subject, action
	e.Kind, e.Outcome, e.Channel = audit.KindChange, audit.OutcomeSuccess, audit.ChannelAPI

	return e
}

func join(t *testing.T, h GateStoresHarness, instance string) (agent.SecretWindow, bool) {
	t.Helper()

	w, opened, err := h.Windows.JoinOrOpen(t.Context(), instance, time.Now(), TestEntry(audit.ServerScope, "startup",
		"server:start-with-agent-secret-access"))
	if err != nil {
		t.Fatalf("JoinOrOpen(%q) = %v", instance, err)
	}

	return w, opened
}

func caseWindowAccept(t *testing.T, h GateStoresHarness) {
	t.Helper()

	first, opened := join(t, h, "i1")
	if !opened || !first.Open() || !h.Maintenance(t) {
		t.Fatalf("JoinOrOpen() = %+v, %v, want a new open window in maintenance mode", first, opened)
	}

	second, opened := join(t, h, "i2")
	if opened || second.ID != first.ID {
		t.Fatalf("second JoinOrOpen() = %+v, %v, want to join window %q", second, opened, first.ID)
	}

	got, err := acceptWindow(t, h, first.ID)
	if err != nil || !got.Covers("i1") || !got.Covers("i2") || h.Maintenance(t) {
		t.Fatalf("Accept() = %+v, %v, want one acceptance for both instances and no maintenance mode", got, err)
	}

	wantCovered(t, h, "i1", "i2")

	if _, err := acceptWindow(t, h, first.ID); !errors.Is(err, agent.ErrWindowClosed) {
		t.Fatalf("second Accept() = %v, want ErrWindowClosed", err)
	}
}

// acceptWindow accepts the window id with a valid acceptance of an
// administrator.
func acceptWindow(t *testing.T, h GateStoresHarness, id string) (agent.SecretWindow, error) {
	t.Helper()

	w, err := h.Windows.Accept(t.Context(), id, Accept("admin", time.Now()),
		TestEntry(audit.ServerScope, "admin", string(agent.ActionServerAcceptAgentSecretAccess)))
	if err != nil {
		return w, fmt.Errorf("accept window %s: %w", id, err)
	}

	return w, nil
}

// wantCovered fails t unless the window of each instance covers it.
func wantCovered(t *testing.T, h GateStoresHarness, instances ...string) {
	t.Helper()

	for _, instance := range instances {
		w, ok, err := h.Windows.ForInstance(t.Context(), instance)
		if err != nil || !ok || !w.Covers(instance) {
			t.Fatalf("ForInstance(%q) = %+v, %v, %v, want the accepted window", instance, w, ok, err)
		}
	}
}

func caseWindowReopen(t *testing.T, h GateStoresHarness) {
	t.Helper()

	first, _ := join(t, h, "i1")
	if _, err := acceptWindow(t, h, first.ID); err != nil {
		t.Fatalf("Accept() = %v", err)
	}

	again, opened := join(t, h, "i1")
	if !opened || again.ID == first.ID || again.Covers("i1") || !h.Maintenance(t) {
		t.Fatalf("restart JoinOrOpen() = %+v, %v, want a new window in maintenance mode", again, opened)
	}

	w, ok, err := h.Windows.ForInstance(t.Context(), "i1")
	if err != nil || !ok || w.ID != again.ID || w.Covers("i1") {
		t.Fatalf("ForInstance() = %+v, %v, %v, want the new window, not accepted", w, ok, err)
	}
}

func caseWindowAcceptInvalid(t *testing.T, h GateStoresHarness) {
	t.Helper()

	w, _ := join(t, h, "i1")

	bad := Accept("admin", time.Now())
	bad.Text = "ok"

	stale := Accept("admin", time.Now())
	stale.MFAAt = stale.At.Add(-agent.MaxMFAAge - time.Minute)

	for want, a := range map[error]agent.Acceptance{agent.ErrAcceptanceText: bad, agent.ErrMFANotFresh: stale} {
		_, err := h.Windows.Accept(t.Context(), w.ID, a,
			TestEntry(audit.ServerScope, "admin", string(agent.ActionServerAcceptAgentSecretAccess)))
		if !errors.Is(err, want) {
			t.Fatalf("Accept() = %v, want %v", err, want)
		}
	}

	if got, ok, err := h.Windows.ForInstance(t.Context(), "i1"); err != nil || !ok || got.Accepted() {
		t.Fatalf("ForInstance() = %+v, %v, %v, want the window still open", got, ok, err)
	}
}

func caseWindowClose(t *testing.T, h GateStoresHarness) {
	t.Helper()

	w, _ := join(t, h, "i1")

	got, err := h.Windows.Close(t.Context(), w.ID, time.Now())
	if err != nil || got.Open() || got.Covers("i1") {
		t.Fatalf("Close() = %+v, %v, want a closed window that covers no instance", got, err)
	}

	if _, err := h.Windows.Close(t.Context(), w.ID, time.Now()); !errors.Is(err, agent.ErrWindowClosed) {
		t.Fatalf("second Close() = %v, want ErrWindowClosed", err)
	}
}

func newConsent(t *testing.T, h GateStoresHarness) agent.Consent {
	t.Helper()

	c, err := h.Consents.Create(t.Context(), agent.Consent{
		ID: "", Subject: "ana", TokenID: "t1", SessionID: "mcp-1", Tool: agent.ToolReadState, Unit: "pulumi:acme/web",
		CreatedAt: time.Now(), ApprovedAt: time.Time{}, UsedAt: time.Time{},
	})
	if err != nil || c.ID == "" {
		t.Fatalf("Create() = %+v, %v, want a consent with an ID", c, err)
	}

	return c
}

func approve(t *testing.T, h GateStoresHarness, c agent.Consent, subject string) error {
	t.Helper()

	_, err := h.Consents.Approve(t.Context(), c.ID, Accept(subject, time.Now()),
		TestEntry(h.Namespace, subject, string(agent.ActionAgentUse)))

	return err
}

func useConsent(t *testing.T, h GateStoresHarness, c agent.Consent) error {
	t.Helper()

	return h.Consents.Use(t.Context(), agent.ConsentUse{
		ID: c.ID, Subject: c.Subject, TokenID: c.TokenID, SessionID: c.SessionID, Tool: c.Tool, Unit: c.Unit,
		At: time.Now(),
	})
}

func caseConsentOnce(t *testing.T, h GateStoresHarness) {
	t.Helper()

	c := newConsent(t, h)
	if err := approve(t, h, c, "ana"); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		used int
	)

	for range 4 {
		wg.Go(func() {
			if useConsent(t, h, c) == nil {
				mu.Lock()
				used++
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	if used != 1 {
		t.Fatalf("%d concurrent uses succeeded, want 1", used)
	}

	if err := useConsent(t, h, c); !errors.Is(err, agent.ErrConsentNotUsable) {
		t.Fatalf("Use() after the use = %v, want ErrConsentNotUsable", err)
	}

	for session, want := range map[string]bool{c.SessionID: true, "mcp-other": false} {
		if got, err := h.Consents.SessionRevealed(t.Context(), session); err != nil || got != want {
			t.Fatalf("SessionRevealed(%q) = %v, %v, want %v", session, got, err, want)
		}
	}
}

func caseConsentApproval(t *testing.T, h GateStoresHarness) {
	t.Helper()

	c := newConsent(t, h)
	if err := useConsent(t, h, c); !errors.Is(err, agent.ErrConsentNotUsable) {
		t.Fatalf("Use() before the approval = %v, want ErrConsentNotUsable", err)
	}

	if got, err := h.Consents.SessionRevealed(t.Context(), c.SessionID); err != nil || got {
		t.Fatalf("SessionRevealed() = %v, %v, want false: a refused use reveals nothing", got, err)
	}

	if err := approve(t, h, c, "eve"); !errors.Is(err, agent.ErrConsentNotUsable) {
		t.Fatalf("Approve() by another person = %v, want ErrConsentNotUsable", err)
	}

	if _, err := h.Consents.Get(t.Context(), "no-such-consent"); !errors.Is(err, agent.ErrConsentNotFound) {
		t.Fatalf("Get() of an unknown ID = %v, want ErrConsentNotFound", err)
	}
}

func caseConsentOther(t *testing.T, h GateStoresHarness) {
	t.Helper()

	c := newConsent(t, h)
	if err := approve(t, h, c, "ana"); err != nil {
		t.Fatalf("Approve() = %v", err)
	}

	other := c
	other.Unit = "pulumi:acme/db"

	if err := useConsent(t, h, other); !errors.Is(err, agent.ErrConsentNotUsable) {
		t.Fatalf("Use() for another unit = %v, want ErrConsentNotUsable", err)
	}

	if err := useConsent(t, h, c); err != nil {
		t.Fatalf("Use() for the unit of the consent = %v: a refused use must not use the consent", err)
	}
}

func caseSettingsDefaults(t *testing.T, h GateStoresHarness) {
	t.Helper()

	if _, err := h.Settings.Connection(t.Context()); !errors.Is(err, agent.ErrDisabled) {
		t.Fatalf("Connection() = %v, want ErrDisabled: the agent is off until an administrator connects a model", err)
	}

	got, err := h.Settings.Installation(t.Context())
	if err != nil || got.MCPEnabled || got.DirectChanges || got.Limits != agent.DefaultLimits() {
		t.Fatalf("Installation() = %+v, %v, want the defaults: MCP off, no direct changes", got, err)
	}

	if o, ok, err := h.Settings.AccountSecretOptIn(t.Context()); err != nil || ok || o.Active() {
		t.Fatalf("AccountSecretOptIn() = %+v, %v, %v, want none", o, ok, err)
	}
}

func caseSettingsZeroLimit(t *testing.T, h GateStoresHarness) {
	t.Helper()

	s := agent.InstallationSettings{Limits: agent.DefaultLimits(), DirectChanges: false, MCPEnabled: true}
	s.Limits.MaxActive = 0

	err := h.Settings.PutInstallation(t.Context(), s, TestEntry(audit.ServerScope, "admin", "server:manage-agent"))
	if !errors.Is(err, agent.ErrInvalid) {
		t.Fatalf("PutInstallation() with a zero limit = %v, want ErrInvalid", err)
	}
}

func caseSettingsOptIn(t *testing.T, h GateStoresHarness) {
	t.Helper()

	weak := Accept("ana", time.Now())
	weak.MFAAt = time.Time{}

	entry := TestEntry(audit.ServerScope, "ana", string(agent.ActionCallerManageAgentOptIns))

	err := h.Settings.PutOptIns(t.Context(), "ana", agent.PersonOptIns{Direct: nil, Secrets: &weak}, entry)
	if !errors.Is(err, agent.ErrMFANotFresh) {
		t.Fatalf("PutOptIns() with no MFA step = %v, want ErrMFANotFresh", err)
	}

	good := Accept("ana", time.Now())

	err = h.Settings.PutOptIns(t.Context(), "ana", agent.PersonOptIns{Direct: nil, Secrets: &good}, entry)
	if err != nil {
		t.Fatalf("PutOptIns() = %v", err)
	}

	got, err := h.Settings.OptIns(t.Context(), "ana")
	if err != nil || got.Secrets == nil || got.Direct != nil {
		t.Fatalf("OptIns() = %+v, %v, want the secret opt-in only", got, err)
	}

	var lower agent.Limits

	ns := agent.NamespaceSettings{Lower: lower, DirectOptIn: &weak, SecretOptIn: nil}

	err = h.Settings.PutNamespace(t.Context(), h.Namespace, ns, h.Fence,
		TestEntry(h.Namespace, "ana", string(agent.ActionNamespaceManageAgent)))
	if !errors.Is(err, agent.ErrMFANotFresh) {
		t.Fatalf("PutNamespace() with no MFA step = %v, want ErrMFANotFresh", err)
	}
}
