package sinks_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// legacyHook makes a webhook sink and then sets its stored target to a host
// that passed the checks before the host allowlist of a write.
func legacyHook(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	s := f.store.sinks[makeHook(t, f).Sink.ID]
	s.Target = "https://hk_1.example.com/ops"
	f.store.sinks[s.ID] = s

	return s
}

func TestStoredSinkFromBeforeTheAllowlistIsListed(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	s := legacyHook(t, f)

	got, err := f.svc.List(t.Context(), shop)
	if err != nil || len(got) != 1 || got[0].ID != s.ID || !got[0].HostNeedsUpdate() {
		t.Fatalf("List = %+v, %v; want the sink, flagged for a new host", got, err)
	}
}

func TestReplaceOfAStoredSinkNeedsAHostThatPasses(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	s := legacyHook(t, f)

	// A change of the name only keeps the old host, so it is refused.
	_, err := f.svc.Replace(t.Context(), f.caller, shop, s.ID, change(s.Version, "Renamed"))
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("Replace with the old host = %v, want ErrInvalid", err)
	}

	c := change(s.Version, "Renamed")
	c.Target = new("https://hooks.example.com/ops")

	after, err := f.svc.Replace(t.Context(), f.caller, shop, s.ID, c)
	if err != nil || after.HostNeedsUpdate() {
		t.Fatalf("Replace with a new host = %+v, %v; want nil and no flag", after, err)
	}
}

func TestTestRefusesAStoredHostThatFailsTheAllowlist(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	s := legacyHook(t, f)

	res, err := runTest(t.Context(), f, shop, s.ID)
	if err != nil || res.Delivered || res.Class != notify.ClassRefused || res.Message != sinks.TextRefused {
		t.Fatalf("Test = %+v, %v; want the class refused", res, err)
	}

	if len(f.hook.got) != 0 {
		t.Fatalf("sender got %d messages, want none", len(f.hook.got))
	}
}
