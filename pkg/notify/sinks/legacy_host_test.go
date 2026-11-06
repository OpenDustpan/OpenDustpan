package sinks_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// legacyHook makes a webhook sink and then sets its stored target to a host
// that passed the checks before the host allowlist of a write.
func legacyHook(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	s := f.store.sinks[makeHook(t, f).Sink.ID]
	s.Target = "hk_1.example.com"
	f.store.sinks[s.ID] = s
	f.store.secrets[s.ID] = sealWebhook(t, s, "https://hk_1.example.com/ops")

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

// sealChat returns the sealed items of the chat sink s with the URL url.
func sealChat(t *testing.T, s notify.Sink, url string) []notify.SealedSecret {
	t.Helper()

	sealed, err := notifytest.Sealer().Seal(t.Context(), s, []notify.SinkSecret{
		{Ref: s.SecretRefs[0], Value: []byte(url)},
	})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	return sealed
}

// legacyChat makes a chat sink and then sets its stored target, and the host
// of its stored URL, to a host that passed the checks before the host
// allowlist of a write.
func legacyChat(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	s := f.store.sinks[makeChat(t, f).ID]
	s.Target = "hk_1.example.com"
	f.store.sinks[s.ID] = s
	f.store.secrets[s.ID] = sealChat(t, s, "https://hk_1.example.com/services/T0/B0/"+canary)

	return s
}

// TestTestRefusesALegacyHostOfEachKind pins the answer of the test route of a
// sink whose stored host fails the host allowlist: the class is refused, the
// text is the refused text, nothing is sent, and the list flags the sink.
func TestTestRefusesALegacyHostOfEachKind(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) (fixture, *fakeSender, notify.Sink)
	}{
		{
			name: "webhook",
			setup: func(t *testing.T) (fixture, *fakeSender, notify.Sink) {
				t.Helper()

				f := newFixture(t, nil)

				return f, f.hook, legacyHook(t, f)
			},
		},
		{
			name: "chat",
			setup: func(t *testing.T) (fixture, *fakeSender, notify.Sink) {
				t.Helper()

				f, sender := chatFixture(t)

				return f, sender, legacyChat(t, f)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, sender, s := tc.setup(t)

			res, err := runTest(t.Context(), f, shop, s.ID)
			if err != nil {
				t.Fatalf("runTest: %v", err)
			}

			if res.Delivered || res.Class != notify.ClassRefused || res.Message != sinks.TextRefused {
				t.Errorf("result = %+v, want the class refused with the refused text", res)
			}

			sender.mu.Lock()
			sent := len(sender.got)
			sender.mu.Unlock()

			if sent != 0 {
				t.Errorf("sender got %d messages, want none", sent)
			}

			got, err := f.svc.List(t.Context(), shop)
			if err != nil || len(got) != 1 || got[0].ID != s.ID || !got[0].HostNeedsUpdate() {
				t.Errorf("List = %+v, %v; want the sink, flagged for a new host", got, err)
			}
		})
	}
}
