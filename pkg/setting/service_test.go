package setting_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress/plainconn"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
	"opendustpan.org/opendustpan/pkg/setting/settingtest"
)

type fakeNamespaces struct{ failover map[namespace.Name]int64 }

func (f fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	v, ok := f.failover[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		CreatedAt: time.Time{}, ID: "", Name: name, PrimaryInstallation: "",
		Overrides: namespace.Overrides{Forced: false, Unlocked: false}, FailoverVersion: v,
	}, nil
}

func newService(t *testing.T, static setting.Static) (*setting.Service, *memStore) {
	t.Helper()

	m := newMemStore()
	m.failover["acme"] = 1

	now := func() time.Time { return time.UnixMilli(1_700_000_000_123) }

	return setting.NewService(m, settingtest.Registry(), static, fakeNamespaces{failover: m.failover}, now), m
}

func request(object string) setting.PatchRequest {
	return setting.PatchRequest{
		Entry: audit.Entry{
			At: time.Time{}, Namespace: "", Subject: "alice", IDPSubject: "", TokenID: "", Action: "", Object: object,
			Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
			Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
			Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
		},
		IfMatch: 0, Fresh: false,
	}
}

func TestServiceGetShowsEveryDeclaredSetting(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t, settingtest.StaticMap{settingtest.KeyFixed: settingtest.File("fixed-by-file")})

	res, err := svc.Get(t.Context(), setting.InstallationScope())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if len(res.Items) != 5 {
		t.Fatalf("items = %d, want the 5 installation settings", len(res.Items))
	}

	for i := 1; i < len(res.Items); i++ {
		if res.Items[i-1].Key >= res.Items[i].Key {
			t.Fatalf("items are not in key order: %s, %s", res.Items[i-1].Key, res.Items[i].Key)
		}
	}

	it, err := svc.Effective(t.Context(), setting.InstallationScope(), settingtest.KeyFixed)
	if err != nil || it.Value != "fixed-by-file" || it.Source != setting.SourceFile {
		t.Fatalf("Effective = %+v, %v", it, err)
	}

	_, err = svc.Effective(t.Context(), setting.InstallationScope(), "nope")
	if !errors.Is(err, setting.ErrUnknownKey) {
		t.Fatalf("Effective of an unknown key = %v", err)
	}
}

func TestServicePatchStampsTheEntry(t *testing.T) {
	t.Parallel()

	svc, m := newService(t, nil)
	patch := setting.Patch{{Key: settingtest.KeyLimit, Value: "42", Delete: false}}

	if _, err := svc.Patch(t.Context(), setting.InstallationScope(), patch, request("@server")); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	written := m.entries[audit.ServerScope]
	if len(written) != 1 {
		t.Fatalf("entries = %d, want 1", len(written))
	}

	e := written[0]
	if e.At.UnixMilli() != 1_700_000_000_123 || e.Kind != audit.KindChange || e.Outcome != audit.OutcomeSuccess ||
		e.Namespace != audit.ServerScope || e.Subject != "alice" {
		t.Fatalf("entry = %+v", e)
	}
}

// TestServicePatchOfThePlainConnectionSettingNeedsAFreshStep proves the
// security rule of the setting at the service: a change from off to on
// without a fresh step changes nothing, and with one it stores the value and
// writes one entry that names the key.
func TestServicePatchOfThePlainConnectionSettingNeedsAFreshStep(t *testing.T) {
	t.Parallel()

	reg, err := setting.NewRegistry(plainconn.SettingDeclarations())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	m := newMemStore()
	now := func() time.Time { return time.UnixMilli(1_700_000_000_123) }
	svc := setting.NewService(m, reg, nil, nil, now)
	patch := setting.Patch{{Key: plainconn.SettingAllowPlainConnections, Value: "true", Delete: false}}

	_, err = svc.Patch(t.Context(), setting.InstallationScope(), patch, request("@server"))
	if !errors.Is(err, setting.ErrFreshStepRequired) {
		t.Fatalf("Patch without a fresh step = %v, want ErrFreshStepRequired", err)
	}

	checkNothingWritten(t, m)

	req := request("@server")
	req.Fresh = true

	res, err := svc.Patch(t.Context(), setting.InstallationScope(), patch, req)
	if err != nil {
		t.Fatalf("Patch with a fresh step: %v", err)
	}

	checkPlainConnectionChange(t, res, m)
}

// checkNothingWritten fails the test when m holds a value, a revision or an
// audit entry.
func checkNothingWritten(t *testing.T, m *memStore) {
	t.Helper()

	if entries := m.entries[audit.ServerScope]; len(entries) != 0 {
		t.Fatalf("entries = %d, want none", len(entries))
	}

	stored, err := m.Load(t.Context(), setting.InstallationScope())
	if err != nil || stored.Revision != 0 || len(stored.Values) != 0 {
		t.Fatalf("stored = %+v, %v; want nothing written", stored, err)
	}
}

// checkPlainConnectionChange fails the test unless res holds the change of
// the plain-connection setting and m holds the one entry of it.
func checkPlainConnectionChange(t *testing.T, res setting.Result, m *memStore) {
	t.Helper()

	if res.Resource.Revision != 1 || !slices.Equal(res.Changed, []string{plainconn.SettingAllowPlainConnections}) {
		t.Fatalf("result = %+v, want the key changed", res)
	}

	entries := m.entries[audit.ServerScope]
	if len(entries) != 1 || len(entries[0].Changes) != 1 {
		t.Fatalf("entries = %+v, want one entry with one change", entries)
	}

	change := entries[0].Changes[0]
	if change.Key != plainconn.SettingAllowPlainConnections || change.New != "true" {
		t.Fatalf("change = %+v, want the key set to true", change)
	}
}

func TestServicePatchOfANamespaceFencesAndScopes(t *testing.T) {
	t.Parallel()

	svc, m := newService(t, nil)
	patch := setting.Patch{{Key: settingtest.KeyNSLimit, Value: "9", Delete: false}}

	if _, err := svc.Patch(t.Context(), setting.NamespaceScope("acme"), patch, request("acme")); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	if len(m.entries["acme"]) != 1 || len(m.entries[audit.ServerScope]) != 0 {
		t.Fatalf("entries = %+v", m.entries)
	}

	_, err := svc.Patch(t.Context(), setting.NamespaceScope("missing"), patch, request("missing"))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Patch of a missing namespace = %v, want ErrNotFound", err)
	}

	_, err = svc.Patch(t.Context(), setting.NamespaceScope(audit.ServerScope), patch, request("@server"))
	if !errors.Is(err, setting.ErrScope) {
		t.Fatalf("Patch of the server scope as a namespace = %v, want ErrScope", err)
	}
}

func TestServiceSyncStaticChangesTheRevisionOnlyWhenTheLayerChanged(t *testing.T) {
	t.Parallel()

	svc, _ := newService(t, settingtest.StaticMap{settingtest.KeyLimit: settingtest.File("1")})

	if rev, changed, err := svc.SyncStatic(t.Context()); err != nil || rev != 0 || changed {
		t.Fatalf("first sync = %d, %v, %v", rev, changed, err)
	}

	if _, changed, err := svc.SyncStatic(t.Context()); err != nil || changed {
		t.Fatalf("second sync = %v, %v", changed, err)
	}
}

func TestNewServiceDefaults(t *testing.T) {
	t.Parallel()

	svc := setting.NewService(newMemStore(), settingtest.Registry(), nil, nil, nil)
	if svc.Registry() == nil {
		t.Fatal("no registry")
	}

	res, err := svc.Get(t.Context(), setting.InstallationScope())
	if err != nil || len(res.Items) == 0 {
		t.Fatalf("Get = %+v, %v", res, err)
	}
}

func TestChangeRefusesAKeyTwiceInOnePatch(t *testing.T) {
	t.Parallel()

	m := newMemStore()
	svc := setting.NewService(m, settingtest.Registry(), nil, nil, nil)

	// A patch from ParsePatch never holds a key twice. This one is built by
	// hand, so only the check of Change stops it.
	patch := setting.Patch{
		{Key: settingtest.KeyLabel, Value: "first", Delete: false},
		{Key: settingtest.KeyLabel, Value: "second", Delete: false},
	}

	_, err := svc.Patch(t.Context(), setting.InstallationScope(), patch, request("@server"))
	if !errors.Is(err, setting.ErrInvalidPatch) {
		t.Fatalf("Patch = %v, want ErrInvalidPatch", err)
	}

	stored, loadErr := m.Load(t.Context(), setting.InstallationScope())
	if loadErr != nil || stored.Revision != 0 || len(stored.Values) != 0 {
		t.Fatalf("stored = %+v, %v; want nothing written", stored, loadErr)
	}
}
