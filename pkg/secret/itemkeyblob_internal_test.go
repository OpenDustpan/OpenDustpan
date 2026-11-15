package secret

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestFilterGarbageRefusesAKeyOfAnotherNamespace sends the filter of the
// reaper rule a listing that holds old, unnamed item key blobs of another
// namespace and of another family, as if the view had failed to scope the
// listing. Only the blob of the namespace of the call is garbage.
func TestFilterGarbageRefusesAKeyOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	const (
		ns      = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")
		other   = namespace.ID("ns-bbbbbbbbbbbbbbbbbbbb")
		attempt = "abcdefghij0123456789"
	)

	now := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * ItemKeyBlobGracePeriod)
	own := blob.Key("namespaces/" + string(ns) + "/item-keys/ik-1/v1-" + attempt)

	objects := []blob.Object{
		{Key: blob.Key("namespaces/" + string(other) + "/item-keys/ik-1/v1-" + attempt), ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(other) + "/item-keys/ik-2/v3-" + attempt), ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(ns) + "/pulumi/stk-1/upd-1/checkpoint-1"), ModifiedAt: old},
		{Key: own, ModifiedAt: old},
	}

	var asked []ItemKeyBlob

	named := func(_ context.Context, part ItemKeyBlob) (bool, error) {
		asked = append(asked, part)

		return false, nil
	}

	got, err := garbageRun{ns: ns, named: named, now: now, probe: ""}.filter(t.Context(), objects)
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	if !slices.Equal(got, []blob.Key{own}) {
		t.Errorf("garbage = %v, want only %s", got, own)
	}

	if len(asked) != 1 || asked[0].Namespace != ns {
		t.Errorf("records asked for %v, want only the blob of %s", asked, ns)
	}
}

// TestFilterGarbageKeepsTheProbeOfTheRun shows that the probe of the run is
// never garbage, also when the store gives it an old time, and that a probe of
// another namespace is never garbage either.
func TestFilterGarbageKeepsTheProbeOfTheRun(t *testing.T) {
	t.Parallel()

	const (
		ns    = namespace.ID("ns-aaaaaaaaaaaaaaaaaaaa")
		other = namespace.ID("ns-bbbbbbbbbbbbbbbbbbbb")
	)

	now := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-2 * ItemKeyBlobGracePeriod)
	probe := blob.Key("namespaces/" + string(ns) + "/item-keys/clock-probe-abcdefghij0123456789")

	objects := []blob.Object{
		{Key: probe, ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(other) + "/item-keys/clock-probe-0123456789abcdefghij"), ModifiedAt: old},
		{Key: blob.Key("namespaces/" + string(ns) + "/item-keys/clock-probe-short"), ModifiedAt: old},
	}

	named := func(context.Context, ItemKeyBlob) (bool, error) { return false, nil }

	got, err := garbageRun{ns: ns, named: named, now: now, probe: probe}.filter(t.Context(), objects)
	if err != nil || len(got) != 0 {
		t.Errorf("garbage = %v, %v, want none", got, err)
	}
}

// TestCheckProbeClock sends the probe check a listing directly, with the probe
// ahead or behind and every other blob in step, so the check stands alone.
func TestCheckProbeClock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.October, 30, 12, 0, 0, 0, time.UTC)
	probe := blob.Key("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/item-keys/clock-probe-abcdefghij0123456789")
	other := blob.Object{
		Key:        blob.Key("namespaces/ns-aaaaaaaaaaaaaaaaaaaa/item-keys/ik-1/v1-abcdefghij0123456789"),
		ModifiedAt: now,
	}

	cases := []struct {
		name    string
		objects []blob.Object
		wantErr bool
	}{
		{name: "in step", objects: []blob.Object{other, {Key: probe, ModifiedAt: now}}, wantErr: false},
		{
			name: "ahead inside the skew", wantErr: false,
			objects: []blob.Object{other, {Key: probe, ModifiedAt: now.Add(ItemKeyBlobClockSkew)}},
		},
		{
			name: "ahead past the skew", wantErr: true,
			objects: []blob.Object{other, {Key: probe, ModifiedAt: now.Add(ItemKeyBlobClockSkew + time.Second)}},
		},
		{
			name: "behind past the skew", wantErr: true,
			objects: []blob.Object{other, {Key: probe, ModifiedAt: now.Add(-ItemKeyBlobClockSkew - time.Second)}},
		},
		{name: "no time", objects: []blob.Object{other, {Key: probe, ModifiedAt: time.Time{}}}, wantErr: true},
		{name: "no probe", objects: []blob.Object{other}, wantErr: true},
		{name: "no listing", objects: nil, wantErr: true},
	}

	for _, tc := range cases {
		err := checkProbeClock(tc.objects, probe, now)
		if tc.wantErr != errors.Is(err, ErrItemKeyBlobClockSkew) || (!tc.wantErr && err != nil) {
			t.Errorf("%s: err = %v, want an error: %v", tc.name, err, tc.wantErr)
		}
	}
}
