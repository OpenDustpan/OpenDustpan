package outputsharetest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// Values of the cases of the setting.
const (
	// settingAction is the action of the audit entry that the cases of the
	// setting write. The store keeps the entry that the caller gives.
	settingAction = "unit:manage-sharing"
	// settingAtMs is the time of the audit entry, in Unix milliseconds.
	settingAtMs = 1_790_000_000_000
	// acme is the namespace of most cases.
	acme namespace.Name = "acme"
	// globex is the second namespace of the cases.
	globex namespace.Name = "globex"
)

// settingEntry returns a valid audit entry of a change of the setting in the
// namespace of fence.
func settingEntry(fence namespace.Fence) audit.Entry {
	return audit.Entry{
		At:            time.UnixMilli(settingAtMs).UTC(),
		Namespace:     fence.Namespace,
		Subject:       "tester",
		IDPSubject:    "",
		TokenID:       "",
		Action:        settingAction,
		Object:        fence.Namespace.String(),
		Version:       "",
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: "",
		JobID:         "",
		Reason:        "",
	}
}

// wantSetting fails the test unless the setting of ns is want.
func wantSetting(t *testing.T, h Harness, ns namespace.Name, want outputshare.Default) {
	t.Helper()

	got, err := h.Settings.Setting(t.Context(), ns)
	if err != nil {
		t.Fatalf("Setting(%s): %v", ns, err)
	}

	if got != want {
		t.Fatalf("Setting(%s) = %q, want %q", ns, got, want)
	}
}

// setSetting stores d as the setting of ns with its current fence, and fails
// the test on an error.
func setSetting(t *testing.T, h Harness, fence namespace.Fence, d outputshare.Default) {
	t.Helper()

	if err := h.Settings.SetSetting(t.Context(), fence.Namespace, d, fence, settingEntry(fence)); err != nil {
		t.Fatalf("SetSetting(%s, %q): %v", fence.Namespace, d, err)
	}
}

// settingCases are the behaviors of the Settings port of Store.
func settingCases() []suiteCase {
	return []suiteCase{
		{testSettingDefault, "store: the setting is nobody for a namespace that never set it"},
		{testSettingRoundTrip, "store: set setting round trips and refuses an invalid value"},
		{testSettingStoredInvalid, "store: a stored setting that fails Validate gives ErrInvalid"},
		{testSettingFence, "store: set setting refuses a stale fence and a fence of another namespace"},
		{testSettingUnknownNamespace, "store: the setting of an unknown namespace is not found"},
		{testSettingIsolated, "store: the setting of one namespace does not change another"},
		{testSettingAudit, "store: set setting appends one audit entry in its transaction"},
	}
}

// testSettingDefault proves that a new namespace reads nobody: no job of
// another unit reads the outputs of its units until a person saves a rule.
func testSettingDefault(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())
	wantSetting(t, h, acme, outputshare.DefaultNobody)
}

// testSettingRoundTrip proves that each value of the setting round trips,
// and that a value that is not a value of the setting is refused with
// ErrInvalid and changes nothing.
func testSettingRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())

	setSetting(t, h, fence, outputshare.DefaultNamespace)
	wantSetting(t, h, acme, outputshare.DefaultNamespace)

	for _, bad := range []outputshare.Default{"", "Nobody", "NAMESPACE", "everyone", "nobody ", "namespace\n", "nobody\x00"} {
		err := h.Settings.SetSetting(t.Context(), acme, bad, fence, settingEntry(fence))
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("SetSetting(%q) = %v, want ErrInvalid", bad, err)
		}

		wantSetting(t, h, acme, outputshare.DefaultNamespace)
	}

	setSetting(t, h, fence, outputshare.DefaultNobody)
	wantSetting(t, h, acme, outputshare.DefaultNobody)
}

// testSettingStoredInvalid proves that a stored value that fails Validate,
// as a row of another version or a restore can hold, reads as ErrInvalid and
// never as a value, so that the caller fails closed. A change over such a
// value fails closed too.
func testSettingStoredInvalid(t *testing.T, h Harness) {
	t.Helper()

	if h.SettingRaw == nil {
		t.Skip("the adapter has no hook that writes a raw setting")
	}

	fence := h.Namespace(t, acme.String())

	for _, bad := range []string{"", "everyone", "Namespace", "namespace "} {
		h.SettingRaw(t, acme, bad)

		// A change reads the value it replaces, so it fails closed too.
		werr := h.Settings.SetSetting(t.Context(), acme, outputshare.DefaultNamespace, fence, settingEntry(fence))
		if !errors.Is(werr, outputshare.ErrInvalid) {
			t.Fatalf("SetSetting over the stored value %q = %v, want ErrInvalid", bad, werr)
		}

		got, err := h.Settings.Setting(t.Context(), acme)
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Setting of the stored value %q = %q, %v, want ErrInvalid", bad, got, err)
		}

		if got != "" {
			t.Fatalf("Setting of the stored value %q = %q with the error, want no value", bad, got)
		}
	}
}

// testSettingFence proves that a change with a stale fence, a fence of
// another namespace or a fence that the namespace never held is refused with
// namespace.ErrFenced and changes nothing.
func testSettingFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())
	other := h.Namespace(t, globex.String())

	for _, bad := range []namespace.Fence{
		{Namespace: acme, FailoverVersion: fence.FailoverVersion - 1},
		{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1},
		other,
	} {
		err := h.Settings.SetSetting(t.Context(), acme, outputshare.DefaultNamespace, bad, settingEntry(bad))
		if !errors.Is(err, namespace.ErrFenced) {
			t.Fatalf("SetSetting with the fence %+v = %v, want namespace.ErrFenced", bad, err)
		}

		wantSetting(t, h, acme, outputshare.DefaultNobody)
		wantSetting(t, h, globex, outputshare.DefaultNobody)
	}
}

// testSettingUnknownNamespace proves that the read and the change of the
// setting of a namespace that does not exist answer namespace.ErrNotFound,
// and that a name that no namespace can have answers ErrInvalid.
func testSettingUnknownNamespace(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, acme.String())

	for _, bad := range []namespace.Name{"", "../acme", "acme\n", "acme/prod", "acme%00", ".acme", "ac me"} {
		if _, err := h.Settings.Setting(t.Context(), bad); !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("Setting(%q) = %v, want ErrInvalid", bad, err)
		}

		fence := namespace.Fence{Namespace: bad, FailoverVersion: 1}

		err := h.Settings.SetSetting(t.Context(), bad, outputshare.DefaultNamespace, fence, settingEntry(fence))
		if !errors.Is(err, outputshare.ErrInvalid) {
			t.Fatalf("SetSetting(%q) = %v, want ErrInvalid", bad, err)
		}
	}

	wantSetting(t, h, acme, outputshare.DefaultNobody)

	if _, err := h.Settings.Setting(t.Context(), "nowhere"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Setting of an unknown namespace = %v, want namespace.ErrNotFound", err)
	}

	fence := namespace.Fence{Namespace: "nowhere", FailoverVersion: 1}

	err := h.Settings.SetSetting(t.Context(), "nowhere", outputshare.DefaultNamespace, fence, settingEntry(fence))
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("SetSetting of an unknown namespace = %v, want namespace.ErrNotFound", err)
	}
}

// testSettingIsolated proves that a change of the setting of one namespace
// leaves the setting of each other namespace as it is.
func testSettingIsolated(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, acme.String())
	h.Namespace(t, globex.String())

	setSetting(t, h, fence, outputshare.DefaultNamespace)
	wantSetting(t, h, acme, outputshare.DefaultNamespace)
	wantSetting(t, h, globex, outputshare.DefaultNobody)
}

// testSettingAudit proves that each change writes one audit entry in the
// namespace of the setting, and that a refused change writes none.
func testSettingAudit(t *testing.T, h Harness) {
	t.Helper()

	if h.Audited == nil {
		t.Skip("the adapter has no hook that reads the audit entries")
	}

	fence := h.Namespace(t, acme.String())
	h.Namespace(t, globex.String())

	before := len(h.Audited(t, acme.String()))
	beforeOther := len(h.Audited(t, globex.String()))

	setSetting(t, h, fence, outputshare.DefaultNamespace)

	if err := h.Settings.SetSetting(t.Context(), acme, "everyone", fence, settingEntry(fence)); err == nil {
		t.Fatal("SetSetting of an invalid value succeeded")
	}

	stale := namespace.Fence{Namespace: acme, FailoverVersion: fence.FailoverVersion + 1}
	err := h.Settings.SetSetting(t.Context(), acme, outputshare.DefaultNobody, stale, settingEntry(stale))
	if err == nil {
		t.Fatal("SetSetting with a stale fence succeeded")
	}

	entries := h.Audited(t, acme.String())
	if added := entries[before:]; !slices.Equal(added, []string{settingAction}) {
		t.Fatalf("the change of the setting wrote the entries %q, want one %q", added, settingAction)
	}

	if n := len(h.Audited(t, globex.String())); n != beforeOther {
		t.Fatalf("the change of the setting of acme wrote %d entries in globex, want none", n-beforeOther)
	}
}

// EntryForSetting returns a valid audit entry of a change of the setting in the
// namespace of fence, with one change that has no Old value. Tests of an
// adapter use it to call SetSetting.
func EntryForSetting(fence namespace.Fence, d outputshare.Default) audit.Entry {
	e := settingEntry(fence)
	e.Changes = []audit.Change{
		{Key: "outputSharing.newUnitDefault", Old: nil, New: string(d), Layer: "", TakesEffect: ""},
	}

	return e
}
