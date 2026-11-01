package main

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/setting"
)

var errSyncFailed = errors.New("the store is gone")

func TestReloadReplacesTheStaticLayerAndRecordsIt(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west", "branding.preset": "first"}`, nil)

	var syncs atomic.Int32

	fx.r.flags.settingsSync = func(context.Context) error {
		syncs.Add(1)

		return nil
	}

	layer := fx.r.flags.staticLayer()

	if v, ok := layer.Lookup("branding.preset"); !ok || v.Value != "first" || v.Source != setting.SourceFile {
		t.Fatalf("branding.preset before the reload = %+v, %v", v, ok)
	}

	fx.write(t, `{"installation": "eu-west", "branding.preset": "second"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if v, ok := layer.Lookup("branding.preset"); !ok || v.Value != "second" {
		t.Fatalf("branding.preset after the reload = %+v, %v; want second", v, ok)
	}

	if syncs.Load() != 1 {
		t.Fatalf("the reload recorded the static layer %d times, want 1", syncs.Load())
	}
}

func TestReloadThatFailsKeepsTheStaticLayer(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"branding.preset": "first"}`, nil)
	layer := fx.r.flags.staticLayer()

	fx.write(t, `{"branding.preset": "second", "no-such-key": 1}`)
	fx.r.reload(t.Context(), triggerPoll)

	if v, ok := layer.Lookup("branding.preset"); !ok || v.Value != "first" {
		t.Fatalf("branding.preset = %+v, %v; want the old value after a refused file", v, ok)
	}
}

func TestSyncFailureNeverFailsTheReload(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"branding.preset": "first"}`, nil)
	fx.r.flags.settingsSync = func(context.Context) error { return errSyncFailed }

	fx.write(t, `{"branding.preset": "second"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 1 || !got[0] {
		t.Fatalf("results = %v, want one success", got)
	}

	if logs := fx.logs.String(); !strings.Contains(logs, "settings revision not updated") {
		t.Fatalf("logs = %s, want the warning", logs)
	}
}

func TestSyncSettingsWithoutAStoreDoesNothing(t *testing.T) {
	t.Parallel()

	var f serveFlags

	f.syncSettings(t.Context(), nil)
}

// digestStore is a setting.Store that records the digests of Sync.
type digestStore struct {
	setting.Store

	digests []string
	err     error
}

func (s *digestStore) Sync(_ context.Context, digest string) (int64, bool, error) {
	s.digests = append(s.digests, digest)

	return 0, false, s.err
}

func TestWireSettingsRecordsTheDigestOfTheStaticLayer(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"branding.preset": "first"}`, map[string]string{
		"DUSTPAN_DATABASE": "sqlite:/tmp/canary-5b1e.db",
	})
	store := &digestStore{Store: nil, digests: nil, err: nil}

	wiring, err := fx.r.flags.wireSettings(store)
	if err != nil {
		t.Fatalf("wireSettings: %v", err)
	}

	if len(wiring.options) != 1 || wiring.sync == nil {
		t.Fatalf("wiring = %+v, want one option and a sync", wiring)
	}

	if syncErr := wiring.sync(t.Context()); syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}

	fx.r.flags.staticLayer().Replace(fx.r.current)

	if syncErr := wiring.sync(t.Context()); syncErr != nil {
		t.Fatalf("sync: %v", syncErr)
	}

	if len(store.digests) != 2 || store.digests[0] != store.digests[1] || store.digests[0] == "" {
		t.Fatalf("digests = %v, want the same non-empty digest twice", store.digests)
	}

	if strings.Contains(store.digests[0], "canary") {
		t.Fatal("the digest holds the text of a secret")
	}

	store.err = errSyncFailed
	if syncErr := wiring.sync(t.Context()); !errors.Is(syncErr, errSyncFailed) {
		t.Fatalf("sync = %v, want the error of the store", syncErr)
	}
}
