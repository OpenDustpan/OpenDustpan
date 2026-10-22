package outputsharesvc_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// fakeSettings is an outputshare.Settings that records each change and
// accepts any value, so a test proves the checks of the service alone.
type fakeSettings struct {
	err     error
	value   outputshare.Default
	entries []audit.Entry
	fences  []namespace.Fence
	reads   int
}

func (f *fakeSettings) Setting(_ context.Context, _ namespace.Name) (outputshare.Default, error) {
	f.reads++

	return f.value, f.err
}

func (f *fakeSettings) SetSetting(
	_ context.Context, _ namespace.Name, d outputshare.Default, fence namespace.Fence, e audit.Entry,
) error {
	if f.err != nil {
		return f.err
	}

	f.value = d
	f.entries = append(f.entries, e)
	f.fences = append(f.fences, fence)

	return nil
}

// fakeNamespaces answers each namespace with the failover version 7.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(_ context.Context, _ namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1).UTC(), Name: name, HomeRegion: "eu-west",
		FailoverVersion: 7, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(
	_ context.Context,
	_ namespace.Name,
	_ namespace.Overrides,
	_ namespace.Fence,
) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(_ context.Context) ([]namespace.Namespace, error) {
	return nil, nil
}

func (fakeNamespaces) Ping(_ context.Context) error { return nil }

// denyAuthorizer denies the actions in deny, and records each call.
type denyAuthorizer struct {
	deny  map[identity.Action]bool
	calls []string
}

func (d *denyAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	d.calls = append(d.calls, string(action)+" "+obj.String())

	if d.deny[action] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, action)
	}

	return nil
}

// newService returns a service over settings with an authorizer that denies
// the actions in deny.
func newService(
	t *testing.T,
	settings *fakeSettings,
	deny ...identity.Action,
) (*outputsharesvc.Service, *denyAuthorizer) {
	t.Helper()

	authz := &denyAuthorizer{deny: map[identity.Action]bool{}, calls: nil}
	for _, a := range deny {
		authz.deny[a] = true
	}

	svc, err := outputsharesvc.New(outputsharesvc.Config{
		Settings: settings, Namespaces: fakeNamespaces{}, Authorizer: authz,
		Now: func() time.Time { return time.UnixMilli(1_790_000_000_000) },
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return svc, authz
}

// alice returns the principal of the calls: a person who is not an
// administrator.
func alice() identity.Principal {
	return identity.Principal{
		TemporaryExpiresAt: time.Time{}, TokenExpiresAt: time.Time{}, Subject: "alice", DisplayName: "alice",
		TokenID: "", Kind: "", Credential: "", JobID: "", Admin: false, TemporaryAdministrator: false,
	}
}

// hostileEntry is an audit entry of a caller that names another namespace,
// another object and another action, as a caller with a fault could.
func hostileEntry() audit.Entry {
	var e audit.Entry

	e.Subject = "alice"
	e.Channel = audit.ChannelAPI
	e.Namespace = "globex"
	e.Object = "pulumi:globex/web/prod"
	e.Action = "unit:read"
	e.Kind = audit.KindReveal
	e.Outcome = audit.OutcomeFailure
	e.Changes = []audit.Change{{Key: "other", Old: nil, New: "x", Layer: "", TakesEffect: ""}}

	return e
}

func TestNewRefusesAMissingPort(t *testing.T) {
	t.Parallel()

	_, err := outputsharesvc.New(outputsharesvc.Config{
		Settings: nil, Namespaces: fakeNamespaces{}, Authorizer: &denyAuthorizer{deny: nil, calls: nil}, Now: nil,
	})
	if !errors.Is(err, outputsharesvc.ErrMissingDependency) {
		t.Fatalf("New = %v, want ErrMissingDependency", err)
	}
}

// TestSettingNeedsNamespaceRead proves that the service checks the action
// namespace:read on the namespace itself, and asks the store nothing for a
// denied caller.
func TestSettingNeedsNamespaceRead(t *testing.T) {
	t.Parallel()

	settings := &fakeSettings{err: nil, value: outputshare.DefaultNamespace, entries: nil, fences: nil, reads: 0}
	svc, authz := newService(t, settings, identity.ActionNamespaceRead)

	_, err := svc.Setting(t.Context(), alice(), "acme")
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("Setting = %v, want identity.ErrDenied", err)
	}

	if settings.reads != 0 {
		t.Fatalf("a denied call asked the store %d times, want none", settings.reads)
	}

	if len(authz.calls) != 1 || authz.calls[0] != "namespace:read acme" {
		t.Fatalf("authorize calls = %q, want namespace:read on acme", authz.calls)
	}

	allowed, _ := newService(t, settings)

	got, err := allowed.Setting(t.Context(), alice(), "acme")
	if err != nil || got != outputshare.DefaultNamespace {
		t.Fatalf("Setting = %q, %v, want namespace", got, err)
	}
}

// TestSetSettingNeedsManageSharing proves that a change checks
// unit:manage-sharing on the namespace itself, and that namespace:read alone
// or unit:read-outputs alone changes nothing.
func TestSetSettingNeedsManageSharing(t *testing.T) {
	t.Parallel()

	settings := &fakeSettings{err: nil, value: outputshare.DefaultNobody, entries: nil, fences: nil, reads: 0}
	svc, authz := newService(t, settings, identity.ActionUnitManageSharing)

	err := svc.SetSetting(t.Context(), alice(), "acme",
		outputshare.DefaultNamespace, hostileEntry())
	if !errors.Is(err, identity.ErrDenied) {
		t.Fatalf("SetSetting = %v, want identity.ErrDenied", err)
	}

	if len(settings.entries) != 0 || settings.value != outputshare.DefaultNobody {
		t.Fatalf("a denied change wrote %d entries and the value %q", len(settings.entries), settings.value)
	}

	if len(authz.calls) != 1 || authz.calls[0] != "unit:manage-sharing acme" {
		t.Fatalf("authorize calls = %q, want unit:manage-sharing on acme", authz.calls)
	}
}

// TestSetSettingRefusesAnInvalidValue proves the check of the value in the
// service alone: the fake store accepts any value, and the service refuses
// each value that is not a value of the setting.
func TestSetSettingRefusesAnInvalidValue(t *testing.T) {
	t.Parallel()

	for _, bad := range []outputshare.Default{"", "everyone", "Nobody", "namespace ", "nobody\r\n"} {
		settings := &fakeSettings{err: nil, value: outputshare.DefaultNobody, entries: nil, fences: nil, reads: 0}
		svc, _ := newService(t, settings)

		err := svc.SetSetting(t.Context(), alice(), "acme", bad, hostileEntry())
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("SetSetting(%q) = %v, want ErrInvalid", bad, err)
		}

		if len(settings.entries) != 0 || settings.value != outputshare.DefaultNobody {
			t.Fatalf("the refused value %q reached the store", bad)
		}
	}
}

// TestSetSettingStampsTheAuditEntry proves that the service writes the
// namespace, the object, the action and the change of the entry itself, and
// does not keep the values of a caller that names another namespace, and
// that it passes the current fence of the namespace.
func TestSetSettingStampsTheAuditEntry(t *testing.T) {
	t.Parallel()

	settings := &fakeSettings{err: nil, value: outputshare.DefaultNobody, entries: nil, fences: nil, reads: 0}
	svc, _ := newService(t, settings)

	err := svc.SetSetting(t.Context(), alice(), "acme",
		outputshare.DefaultNamespace, hostileEntry())
	if err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	if len(settings.entries) != 1 {
		t.Fatalf("the change wrote %d entries, want 1", len(settings.entries))
	}

	checkStampedEntry(t, settings.entries[0])

	if want := (namespace.Fence{Namespace: "acme", FailoverVersion: 7}); settings.fences[0] != want {
		t.Fatalf("fence = %+v, want %+v", settings.fences[0], want)
	}
}

// checkStampedEntry fails the test unless e is the entry of the change of
// acme by alice to namespace. The service leaves the Old value to the store,
// which reads it in the transaction of the write.
func checkStampedEntry(t *testing.T, e audit.Entry) {
	t.Helper()

	got := []string{
		e.Namespace.String(), e.Object, e.Action, string(e.Kind), string(e.Outcome), e.Subject,
		e.At.UTC().Format(time.RFC3339Nano),
	}
	want := []string{
		"acme", "acme", "unit:manage-sharing", string(audit.KindChange), string(audit.OutcomeSuccess), "alice",
		time.UnixMilli(1_790_000_000_000).UTC().Format(time.RFC3339Nano),
	}

	if !slices.Equal(got, want) {
		t.Fatalf("audit entry fields = %q, want %q", got, want)
	}

	if len(e.Changes) != 1 {
		t.Fatalf("audit changes = %+v, want one", e.Changes)
	}

	c := e.Changes[0]
	if c.Key != outputsharesvc.SettingKey || c.Old != nil || c.New != "namespace" {
		t.Fatalf("audit change = %+v, want the new value namespace and no old value", c)
	}
}

// TestSetSettingFailsClosedOnAStoreError proves that an error of the store
// at the write stops the change.
func TestSetSettingFailsClosedOnAStoreError(t *testing.T) {
	t.Parallel()

	settings := &fakeSettings{
		err: fmt.Errorf("stored setting: %w", outputshare.ErrInvalid), value: "", entries: nil, fences: nil, reads: 0,
	}
	svc, _ := newService(t, settings)

	err := svc.SetSetting(t.Context(), alice(), "acme",
		outputshare.DefaultNamespace, hostileEntry())
	if !errors.Is(err, outputshare.ErrInvalid) {
		t.Fatalf("SetSetting = %v, want the error of the store", err)
	}

	if len(settings.entries) != 0 {
		t.Fatalf("a failed change wrote %d entries", len(settings.entries))
	}
}
