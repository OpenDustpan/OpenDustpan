package notifytest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// LegacyWebhookID is the ID of the webhook sink that a migration test plants
// in the shape of the schema before webhook URLs became secret items.
const LegacyWebhookID = "snk-legacy1"

// LegacyHost is the host of the URL of the legacy webhook sink.
const LegacyHost = "hooks.example.com:8443"

// LegacyKeyRefs returns the refs of the two signing keys of the legacy
// webhook sink, in order.
func LegacyKeyRefs() []string { return []string{"itm-legacy-k1", "itm-legacy-k2"} }

// LegacyToken returns the token in the URL of the legacy webhook sink. No
// table may hold it after the migration. The parts are joined at run time,
// so that no credential-shaped text sits in the source.
func LegacyToken() string { return strings.Join([]string{"legacy", "token", "9f3a2c"}, "-") }

// LegacyWebhookURL returns the URL that the legacy webhook sink held as its
// plain text target.
func LegacyWebhookURL() string { return "https://" + LegacyHost + "/ops/" + LegacyToken() + "?k=1" }

// LegacyKeyItems returns the sealed signing keys of the legacy webhook sink
// in scope, in the order of LegacyKeyRefs, as the old shape stored them.
func LegacyKeyItems(ctx context.Context, scope namespace.Name) []notify.SealedSecret {
	s := legacySink(scope)
	out := make([]notify.SealedSecret, 0, len(s.SecretRefs))

	for _, ref := range s.SecretRefs {
		out = append(out, secret(ctx, s, ref))
	}

	return out
}

// legacySink returns the legacy webhook sink in scope, with the refs of its
// keys only: the shape before the URL became the first secret item.
func legacySink(scope namespace.Name) notify.Sink {
	return notify.Sink{
		ID: LegacyWebhookID, Scope: scope, Kind: notify.SinkWebhook, Name: "Old hook", Platform: "",
		Target: LegacyHost, SecretRefs: LegacyKeyRefs(), CreatedBy: "alice", Version: 1,
	}
}

// CheckMigratedWebhook checks the legacy webhook sink in the scope shop after
// the migration that moved the URL out of the target. The adapter planted the
// sink with a plain text URL as its target and the two keys of
// LegacyKeyItems, then opened the store, which applied the migration. fence
// is the fence of shop. The check proves that:
//   - the target is the host and nothing else;
//   - the placeholder item comes first and never opens: opening the sink
//     fails closed with notify.ErrNoURL;
//   - the two signing keys keep their refs and their values;
//   - a replace with a new URL seals the URL as a new item, drops the
//     placeholder and keeps the keys.
func CheckMigratedWebhook(t *testing.T, sinks notify.SinkStore, fence namespace.Fence) {
	t.Helper()

	got, err := sinks.Sinks(t.Context(), shop)
	if err != nil || len(got) != 1 {
		t.Fatalf("Sinks after the migration = %+v (%v), want the legacy webhook sink", got, err)
	}

	s := got[0]
	if s.ID != LegacyWebhookID || s.Target != LegacyHost || len(s.SecretRefs) != 3 ||
		!strings.HasPrefix(s.SecretRefs[0], "itm-nourl-") || !slices.Equal(s.SecretRefs[1:], LegacyKeyRefs()) {
		t.Fatalf("sink after the migration = %+v, want the host as target, a placeholder and the two key refs", s)
	}

	sealed, err := sinks.SinkSecrets(t.Context(), shop, s.ID)
	if err != nil || len(sealed) != 3 {
		t.Fatalf("SinkSecrets after the migration = %d items (%v), want 3", len(sealed), err)
	}

	if _, openErr := Sealer().OpenDestination(t.Context(), s, sealed); !errors.Is(openErr, notify.ErrNoURL) {
		t.Fatalf("OpenDestination of the migrated sink: err = %v, want ErrNoURL", openErr)
	}

	replaceLegacyURL(t, sinks, fence, s)
}

// replaceLegacyURL replaces the URL of the migrated sink s and checks that the
// sink opens, with the new URL and the old keys.
func replaceLegacyURL(t *testing.T, sinks notify.SinkStore, fence namespace.Fence, s notify.Sink) {
	t.Helper()

	next := s
	next.SecretRefs = slices.Concat([]string{"itm-legacy-url2"}, s.SecretRefs[1:])

	url := "https://" + LegacyHost + "/new"
	item := sealed(t.Context(), next, next.SecretRefs[0], url)

	if err := sinks.PutSink(t.Context(), next, []notify.SealedSecret{item}, fence, manage()); err != nil {
		t.Fatalf("PutSink of a new URL on the migrated sink: %v", err)
	}

	stored, err := sinks.SinkSecrets(t.Context(), shop, s.ID)
	if err != nil || len(stored) != 3 || stored[0].Ref() != "itm-legacy-url2" {
		t.Fatalf("SinkSecrets after the replace = %d items (%v), want the new URL item first", len(stored), err)
	}

	dest, err := Sealer().OpenDestination(t.Context(), next, stored)
	if err != nil {
		t.Fatalf("OpenDestination after the replace: %v", err)
	}

	defer dest.Clear()

	if dest.URL != url || len(dest.Keys) != 2 || string(dest.Keys[0]) != plaintext("itm-legacy-k1") ||
		string(dest.Keys[1]) != plaintext("itm-legacy-k2") {
		t.Fatalf("destination after the replace = %q and %d keys, want the new URL and the old keys", dest.URL,
			len(dest.Keys))
	}
}

// testSinksWebhookURL checks the URL item of a webhook sink in the store: a
// create keeps the URL sealed and the host as target, a read gives back what
// the create wrote, a replace of the URL keeps the keys, a delete removes
// every item, and an item that is missing or does not open fails closed with
// notify.ErrNoURL.
func testSinksWebhookURL(t *testing.T, h SinkHarness) {
	t.Helper()

	fence := sinkSetup(t, h)
	s := sink("snk-1", shop)
	url := "https://" + s.Target + "/" + LegacyToken()

	items := []notify.SealedSecret{
		sealed(t.Context(), s, s.SecretRefs[0], url),
		secret(t.Context(), s, s.SecretRefs[1]),
	}

	if err := h.Sinks.PutSink(t.Context(), s, items, fence, manage()); err != nil {
		t.Fatalf("PutSink of a webhook sink: %v", err)
	}

	// Read back: the host is the target, and no item holds the URL or the key
	// in plaintext.
	got := sinksOf(t, h, shop)
	if len(got) != 1 || got[0].Target != s.Target || strings.Contains(got[0].Target, "/") {
		t.Fatalf("Sinks = %+v, want the host as the target", got)
	}

	stored, err := h.Sinks.SinkSecrets(t.Context(), shop, s.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("SinkSecrets = %d items (%v), want the URL and the key", len(stored), err)
	}

	for _, it := range stored {
		if bytes.Contains(it.Ciphertext(), []byte(LegacyToken())) {
			t.Fatalf("item %s holds the token of the URL in plaintext", it.Ref())
		}
	}

	checkDestination(t, got[0], stored, url, 1)

	// Replace: a new URL item first, and the key keeps its ref and value.
	next := got[0]
	next.SecretRefs = []string{"itm-snk-1-url2", s.SecretRefs[1]}
	newURL := "https://" + s.Target + "/other"

	err = h.Sinks.PutSink(t.Context(), next, []notify.SealedSecret{sealed(t.Context(), next, next.SecretRefs[0], newURL)},
		fence, manage())
	if err != nil {
		t.Fatalf("PutSink with a new URL: %v", err)
	}

	stored, err = h.Sinks.SinkSecrets(t.Context(), shop, s.ID)
	if err != nil || len(stored) != 2 {
		t.Fatalf("SinkSecrets after the replace = %d items (%v), want 2", len(stored), err)
	}

	next.Version++
	checkDestination(t, next, stored, newURL, 1)
	checkFailsClosed(t, next, stored)

	// Delete: no item stays.
	if err = h.Sinks.DeleteSink(t.Context(), s.ID, fence, manage()); err != nil {
		t.Fatalf("DeleteSink: %v", err)
	}

	if _, err = h.Sinks.SinkSecrets(t.Context(), shop, s.ID); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("SinkSecrets after the delete: err = %v, want ErrNotFound", err)
	}
}

// checkDestination checks that OpenDestination gives wantURL and wantKeys keys.
func checkDestination(t *testing.T, s notify.Sink, stored []notify.SealedSecret, wantURL string, wantKeys int) {
	t.Helper()

	dest, err := Sealer().OpenDestination(t.Context(), s, stored)
	if err != nil {
		t.Fatalf("OpenDestination: %v", err)
	}

	defer dest.Clear()

	if dest.URL != wantURL || len(dest.Keys) != wantKeys || string(dest.Keys[0]) != plaintext(s.SecretRefs[1]) {
		t.Fatalf("OpenDestination = %q and %d keys, want %q and %d keys", dest.URL, len(dest.Keys), wantURL, wantKeys)
	}
}

// checkFailsClosed checks that OpenDestination returns notify.ErrNoURL when an
// item is missing, when an item has no value, when an item does not open, and
// when the URL is on another host than the target.
func checkFailsClosed(t *testing.T, s notify.Sink, stored []notify.SealedSecret) {
	t.Helper()

	wrongHost := sealed(t.Context(), s, s.SecretRefs[0], "https://other.example.com/x")
	empty := notify.StoredSecret(s.Scope, s.ID, s.SecretRefs[0], []byte{0})
	garbled := notify.StoredSecret(s.Scope, s.ID, s.SecretRefs[0], bytes.Repeat([]byte{7}, 64))

	cases := []struct {
		name   string
		sealed []notify.SealedSecret
	}{
		{"no item", nil},
		{"the URL item only", stored[:1]},
		{"the key item only", stored[1:]},
		{"an item with no value", []notify.SealedSecret{empty, stored[1]}},
		{"an item that does not open", []notify.SealedSecret{garbled, stored[1]}},
		{"a URL on another host", []notify.SealedSecret{wrongHost, stored[1]}},
	}

	for _, tc := range cases {
		dest, err := Sealer().OpenDestination(t.Context(), s, tc.sealed)
		if !errors.Is(err, notify.ErrNoURL) || dest.URL != "" || len(dest.Keys) != 0 {
			t.Fatalf("OpenDestination with %s = %q, %v, want ErrNoURL and no value", tc.name, dest.URL, err)
		}
	}
}
