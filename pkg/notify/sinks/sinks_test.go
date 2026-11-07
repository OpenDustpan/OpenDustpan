package sinks_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate/authgatetest"
	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

const (
	shop   = namespace.Name("shop")
	canary = "CANARY-TOKEN-4711"
)

// fakeStore is a SinkStore in memory. It keeps what the service passes and
// checks nothing else: the conformance suite checks the stores.
type fakeStore struct {
	mu      sync.Mutex
	sinks   map[string]notify.Sink
	secrets map[string][]notify.SealedSecret
	entries []audit.Entry
	puts    int
	// secretReads counts the reads of the sealed secrets of a sink.
	secretReads int
	// testErr, when set, is the error of RecordSinkTest: a failed audit append.
	testErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		mu: sync.Mutex{}, sinks: map[string]notify.Sink{}, secrets: map[string][]notify.SealedSecret{},
		entries: nil, puts: 0, secretReads: 0, testErr: nil,
	}
}

func (f *fakeStore) PutSink(
	_ context.Context, s notify.Sink, secrets []notify.SealedSecret, _ namespace.Fence, e audit.Entry,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	old, ok := f.sinks[s.ID]
	if old.Version != s.Version {
		return fmt.Errorf("put: %w", notify.ErrConflict)
	}

	s.Version++
	f.sinks[s.ID] = s

	// The items of the refs of s, in order: a new item, or the stored item of
	// the same ref. A ref that s no longer names loses its item.
	kept := make([]notify.SealedSecret, 0, len(s.SecretRefs))

	for _, ref := range s.SecretRefs {
		for _, item := range slices.Concat(secrets, f.secrets[s.ID]) {
			if item.Ref() == ref {
				kept = append(kept, item)

				break
			}
		}
	}

	if len(secrets) > 0 || !ok {
		f.secrets[s.ID] = kept
	}

	f.entries = append(f.entries, e)
	f.puts++

	return nil
}

func (f *fakeStore) SinkSecrets(_ context.Context, scope namespace.Name, id string) ([]notify.SealedSecret, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if s, ok := f.sinks[id]; !ok || s.Scope != scope {
		return nil, notify.ErrNotFound
	}

	f.secretReads++

	return slices.Clone(f.secrets[id]), nil
}

func (f *fakeStore) DeleteSink(_ context.Context, id string, _ namespace.Fence, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.sinks[id]; !ok {
		return notify.ErrNotFound
	}

	delete(f.sinks, id)
	delete(f.secrets, id)

	f.entries = append(f.entries, e)

	return nil
}

func (f *fakeStore) Sinks(_ context.Context, scope namespace.Name) ([]notify.Sink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []notify.Sink

	for id := range f.sinks {
		if f.sinks[id].Scope == scope {
			out = append(out, f.sinks[id])
		}
	}

	slices.SortFunc(out, func(a, b notify.Sink) int { return strings.Compare(a.ID, b.ID) })

	return out, nil
}

func (f *fakeStore) RecordSinkTest(_ context.Context, id string, _ namespace.Fence, e audit.Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.sinks[id]; !ok {
		return notify.ErrNotFound
	}

	if f.testErr != nil {
		return f.testErr
	}

	f.entries = append(f.entries, e)

	return nil
}

// fakeNamespaces knows the namespace shop. With notHome set, the primary installation of
// shop is another installation.
type fakeNamespaces struct {
	notHome bool
}

func (f fakeNamespaces) WritableHere(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	ns, err := f.Get(ctx, name)
	if err != nil {
		return namespace.Namespace{}, err
	}

	if f.notHome {
		return namespace.Namespace{}, namespace.ErrNotHome
	}

	return ns, nil
}

func (fakeNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != shop {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: "ns-1", CreatedAt: time.Time{}, Name: shop, PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// fakeSender records the message and returns err.
type fakeSender struct {
	kind notify.SinkKind
	err  error

	mu   sync.Mutex
	got  []notify.Message
	left time.Duration
}

func (f *fakeSender) Kind() notify.SinkKind { return f.kind }

func (f *fakeSender) Send(ctx context.Context, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if dl, ok := ctx.Deadline(); ok {
		f.left = time.Until(dl)
	}

	m.Secrets = cloneAll(m.Secrets)
	f.got = append(f.got, m)

	return f.err
}

func cloneAll(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = bytes.Clone(in[i])
	}

	return out
}

type fixture struct {
	svc    *sinks.Service
	store  *fakeStore
	hook   *fakeSender
	caller *sinks.Caller
}

// person returns a principal of the kind kind.
func person(kind identity.Kind) identity.Principal {
	return identity.Principal{
		Subject: "alice", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: kind, Credential: "", JobID: "",
	}
}

func testNow() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) }

// config returns a service config over store, with the senders.
func config(store notify.SinkStore, ns sinks.Namespaces, senders ...notify.Sender) sinks.Config {
	return sinks.Config{
		Store: store, Sealer: notifytest.Sealer(), Namespaces: ns, Senders: senders, Ports: egress.Ports{},
		Counter: authgatetest.NewMemory(testNow),
		Now:     testNow,
	}
}

func newFixture(t *testing.T, hookErr error) fixture {
	t.Helper()

	store := newFakeStore()
	hook := &fakeSender{kind: notify.SinkWebhook, err: hookErr, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(config(store, fakeNamespaces{notHome: false}, hook))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	by := audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "req-1",
		ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}

	return fixture{
		svc: svc, store: store, hook: hook,
		caller: &sinks.Caller{Principal: person(identity.KindPerson), By: by},
	}
}

func TestNewServiceNeedsPorts(t *testing.T) {
	t.Parallel()

	noStore := config(nil, fakeNamespaces{notHome: false})
	noNamespaces := config(newFakeStore(), nil)
	noSealer := config(newFakeStore(), fakeNamespaces{notHome: false})
	noSealer.Sealer = notify.SinkSealer{Namespaces: nil, Installation: nil}
	noCounter := config(newFakeStore(), fakeNamespaces{notHome: false})
	noCounter.Counter = nil

	cases := []struct {
		name string
		cfg  sinks.Config
	}{
		{"no store", noStore},
		{"no namespaces", noNamespaces},
		{"no sealer", noSealer},
		{"no counter", noCounter},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := sinks.NewService(tc.cfg); !errors.Is(err, sinks.ErrMissingDependency) {
				t.Fatalf("NewService = %v, want ErrMissingDependency", err)
			}
		})
	}
}

// createCase is a successful create: the input, the target that the sink gets,
// and whether the create answers a signing key and stores a secret item.
type createCase struct {
	name       string
	in         sinks.New
	wantTarget string
	wantKey    bool
	wantSecret bool
}

func TestCreate(t *testing.T) {
	t.Parallel()

	chatURL := "https://hooks.slack.com/services/T000/B000/" + canary

	cases := []createCase{
		{
			"a webhook",
			sinks.New{
				Kind: notify.SinkWebhook, Platform: "", Name: "Ops",
				Target: "https://hooks.example.com/ops/" + canary, URL: "",
				AccessToken: "",
			},
			"hooks.example.com", true, true,
		},
		{
			"a chat webhook",
			sinks.New{
				Kind: notify.SinkChat, Platform: "slack", Name: "Ops chat", Target: "", URL: chatURL,
				AccessToken: "",
			},
			"hooks.slack.com", false, true,
		},
		{
			"a chat webhook in upper case",
			sinks.New{
				Kind: notify.SinkChat, Platform: "mattermost", Name: "Ops chat", Target: "",
				URL:         "https://Chat.Example.com/hooks/" + canary,
				AccessToken: "",
			},
			"chat.example.com", false, true,
		},
		{
			"an email sink",
			sinks.New{
				Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "ops@example.com", URL: "",
				AccessToken: "",
			},
			"ops@example.com", false, false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			got, err := f.svc.Create(t.Context(), f.caller, shop, tc.in)
			if err != nil {
				t.Fatalf("Create = %v, want nil", err)
			}

			checkCreated(t, f, got, &tc)
		})
	}
}

func checkCreated(t *testing.T, f fixture, got sinks.Created, want *createCase) {
	t.Helper()

	s := got.Sink
	if !strings.HasPrefix(s.ID, notify.SinkIDPrefix) || s.Version != 1 || s.Target != want.wantTarget ||
		s.CreatedBy != "alice" || s.Scope != shop {
		t.Fatalf("sink = %+v, want an snk- ID, version 1, target %q, creator alice", s, want.wantTarget)
	}

	if (got.SigningKey != "") != want.wantKey || (want.wantKey && len(got.SigningKey) != 64) {
		t.Fatalf("signing key = %q, want a key of 64 hex digits: %t", got.SigningKey, want.wantKey)
	}

	checkStoredSecret(t, f, got, want)
	checkCreateEntry(t, f, got)
}

// checkStoredSecret checks that the store holds one sealed secret item when
// the case wants one, and that no plaintext reached the store.
func checkStoredSecret(t *testing.T, f fixture, got sinks.Created, want *createCase) {
	t.Helper()

	// A webhook sink has the URL and the signing key. A chat sink has the URL.
	items := 0
	if want.wantSecret {
		items = 1
	}

	if want.wantKey {
		items = 2
	}

	sealed, err := f.store.SinkSecrets(t.Context(), shop, got.Sink.ID)
	if err != nil || len(sealed) != items {
		t.Fatalf("stored secrets = %d (%v), want %d", len(sealed), err, items)
	}

	for _, item := range sealed {
		plain := bytes.Contains(item.Ciphertext(), []byte(canary)) ||
			(got.SigningKey != "" && bytes.Contains(item.Ciphertext(), []byte(got.SigningKey)))
		if plain {
			t.Fatal("the store got a plaintext value")
		}
	}
}

// checkCreateEntry checks the one audit entry of a create, and that it holds no
// secret.
func checkCreateEntry(t *testing.T, f fixture, got sinks.Created) {
	t.Helper()

	if len(f.store.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.store.entries))
	}

	e := f.store.entries[0]
	if e.Action != "sink:create" || e.ObjectID != got.Sink.ID || e.Namespace != shop || e.Subject != "alice" ||
		e.PrincipalKind != audit.PrincipalKindPerson || e.Kind != audit.KindChange || e.RequestID != "req-1" {
		t.Fatalf("audit entry = %+v, want sink:create of %s by alice", e, got.Sink.ID)
	}

	shown := fmt.Sprintf("%+v", e)
	if strings.Contains(shown, canary) || (got.SigningKey != "" && strings.Contains(shown, got.SigningKey)) {
		t.Fatal("the audit entry holds a secret")
	}
}

func TestCreateRefused(t *testing.T) {
	t.Parallel()

	chat := func(url string) sinks.New {
		return sinks.New{Kind: notify.SinkChat, Platform: "slack", Name: "Chat", Target: "", URL: url, AccessToken: ""}
	}
	hook := func(target string) sinks.New {
		return sinks.New{Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: target, URL: "", AccessToken: ""}
	}

	cases := []struct {
		name    string
		in      sinks.New
		refused bool
	}{
		{"a chat URL over http", chat("http://hooks.slack.com/services/" + canary), true},
		{"a chat URL on loopback", chat("https://127.0.0.1/services/" + canary), true},
		{"a chat URL on localhost", chat("https://localhost/services/" + canary), true},
		{"a chat URL on the metadata address", chat("https://169.254.169.254/" + canary), true},
		{"a chat URL on a private address", chat("https://10.0.0.5/services/" + canary), true},
		{"a chat URL on a private IPv6 address", chat("https://[fd00::1]/services/" + canary), true},
		{
			"a chat URL with user information",
			chat(fakecred.URL("https", "user", "pw", "hooks.slack.com", "/") + canary),
			true,
		},
		{"a chat URL with a fragment", chat("https://hooks.slack.com/x#" + canary), true},
		{"a chat URL in a number host", chat("https://2130706433/" + canary), true},
		{"a chat URL that does not parse", chat("https://hooks.slack.com/%zz" + canary), true},
		{"no chat URL", chat(""), true},
		{"a chat URL with an encoded slash", chat("https://hooks.slack.com/services/x%2F..%2F" + canary), false},
		{"a chat URL with encoded dots", chat("https://hooks.slack.com/services/%2e%2e/" + canary), false},
		{"a chat URL with a dot segment", chat("https://hooks.slack.com/services/../" + canary), false},
		{"a chat URL over the bound", chat("https://hooks.slack.com/" + strings.Repeat("a", egress.MaxURLBytes)), true},
		{"a chat target", sinks.New{
			Kind: notify.SinkChat, Platform: "slack", Name: "Chat", Target: "hooks.slack.com",
			URL:         "https://hooks.slack.com/x",
			AccessToken: "",
		}, false},
		{"a chat sink with no platform", sinks.New{
			Kind: notify.SinkChat, Platform: "", Name: "Chat", Target: "", URL: "https://hooks.slack.com/x",
			AccessToken: "",
		}, false},
		{"a chat sink with an unknown platform", sinks.New{
			Kind: notify.SinkChat, Platform: "irc", Name: "Chat", Target: "", URL: "https://hooks.slack.com/x",
			AccessToken: "",
		}, false},
		{
			"a webhook with a url",
			sinks.New{
				Kind:        notify.SinkWebhook,
				Platform:    "",
				Name:        "Hook",
				Target:      "https://hooks.example.com/",
				URL:         "https://x.example.com",
				AccessToken: "",
			},
			false,
		},
		{"a webhook with a platform", sinks.New{
			Kind: notify.SinkWebhook, Platform: "slack", Name: "Hook", Target: "https://hooks.example.com/", URL: "",
			AccessToken: "",
		}, false},
		{"a webhook over http", hook("http://hooks.example.com/" + canary), true},
		{"a webhook on loopback", hook("https://127.0.0.1:8443/" + canary), true},
		{"a webhook on the metadata address", hook("https://169.254.169.254/latest/" + canary), true},
		{"a webhook on IPv6 loopback", hook("https://[::1]/" + canary), true},
		{
			"a webhook with user information",
			hook(fakecred.URL("https", "u", "p", "hooks.example.com", "/") + canary),
			true,
		},
		{"a webhook with no target", hook(""), true},
		{
			"an email sink with a url",
			sinks.New{
				Kind:        notify.SinkEmail,
				Platform:    "",
				Name:        "Mail",
				Target:      "ops@example.com",
				URL:         "https://hooks.example.com",
				AccessToken: "",
			},
			false,
		},
		{"an email sink with a name in the address", sinks.New{
			Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "Ops <ops@example.com>", URL: "",
			AccessToken: "",
		}, false},
		{
			"an unknown kind",
			sinks.New{Kind: "pager", Platform: "", Name: "P", Target: "x", URL: "", AccessToken: ""},
			false,
		},
		{"no name", sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: "", Target: "https://hooks.example.com/", URL: "",
			AccessToken: "",
		}, false},
		{"a name over the bound", sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: strings.Repeat("n", notify.MaxNameLen+1),
			Target: "https://hooks.example.com/", URL: "",
			AccessToken: "",
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			_, err := f.svc.Create(t.Context(), f.caller, shop, tc.in)
			if !errors.Is(err, notify.ErrInvalid) {
				t.Fatalf("Create = %v, want ErrInvalid", err)
			}

			if tc.refused != errors.Is(err, egress.ErrRefused) {
				t.Fatalf("Create = %v, egress.ErrRefused = %t, want %t", err, !tc.refused, tc.refused)
			}

			if f.store.puts != 0 || len(f.store.entries) != 0 {
				t.Fatalf("store got %d puts and %d entries, want none", f.store.puts, len(f.store.entries))
			}
		})
	}
}

func TestCreateInUnknownNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	_, err := f.svc.Create(t.Context(), f.caller, "nowhere", sinks.New{
		Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: "https://hooks.example.com/", URL: "",
		AccessToken: "",
	})
	if !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("Create = %v, want namespace.ErrNotFound", err)
	}

	if f.store.puts != 0 {
		t.Fatalf("store got %d puts, want none", f.store.puts)
	}
}

func TestSigningKeysDiffer(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 50 {
		k, err := sinks.NewSigningKey()
		if err != nil || len(k) != 64 || seen[string(k)] {
			t.Fatalf("NewSigningKey = %q, %v, want a new key of 64 digits", k, err)
		}

		seen[string(k)] = true
	}
}

func makeChat(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	got, err := f.svc.Create(t.Context(), f.caller, shop, sinks.New{
		Kind: notify.SinkChat, Platform: "slack", Name: "Chat", Target: "",
		URL:         "https://hooks.slack.com/services/T0/B0/" + canary,
		AccessToken: "",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	return got.Sink
}

func makeHook(t *testing.T, f fixture) sinks.Created {
	t.Helper()

	got, err := f.svc.Create(t.Context(), f.caller, shop, sinks.New{
		Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: "https://hooks.example.com/ops", URL: "",
		AccessToken: "",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	return got
}

// replaced is a successful replace: the change to make and the check of the
// stored sink after it.
type replaced struct {
	name   string
	seed   func(t *testing.T, f fixture) notify.Sink
	change func(version int64) sinks.Change
	check  func(t *testing.T, f fixture, before, after notify.Sink)
}

// refusedReplace is a replace that the service refuses with want.
type refusedReplace struct {
	name   string
	seed   func(t *testing.T, f fixture) notify.Sink
	change func(version int64) sinks.Change
	want   error
}

// change returns a Change with the version, the name and no other field.
func change(version int64, name string) sinks.Change {
	return sinks.Change{Version: version, Name: name, Target: nil, URL: nil, AccessToken: nil}
}

// seedChat makes a chat sink.
func seedChat(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	return makeChat(t, f)
}

// seedHook makes a webhook sink.
func seedHook(t *testing.T, f fixture) notify.Sink {
	t.Helper()

	return makeHook(t, f).Sink
}

func TestReplace(t *testing.T) {
	t.Parallel()

	newURL := "https://hooks.slack.com/services/T0/B0/other-" + canary

	cases := []replaced{
		{
			name: "a new name of a webhook", seed: seedHook,
			change: func(v int64) sinks.Change { return change(v, "Ops team") },
			check: func(t *testing.T, _ fixture, before, after notify.Sink) {
				t.Helper()

				if after.Name != "Ops team" || after.Version != before.Version+1 || after.Target != before.Target ||
					!slices.Equal(after.SecretRefs, before.SecretRefs) {
					t.Fatalf("sink = %+v, want only the name changed", after)
				}
			},
		},
		{
			name: "a new target of a webhook", seed: seedHook,
			change: func(v int64) sinks.Change {
				c := change(v, "Hook")
				c.Target = new("https://new.example.com/path/" + canary)

				return c
			},
			check: checkNewWebhookURL("https://new.example.com/path/" + canary),
		},
		{
			name: "a new URL of a chat sink", seed: seedChat,
			change: func(v int64) sinks.Change {
				c := change(v, "Chat")
				c.URL = &newURL

				return c
			},
			check: checkNewChatURL(newURL),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			before := tc.seed(t, f)

			after, err := f.svc.Replace(t.Context(), f.caller, shop, before.ID, tc.change(before.Version))
			if err != nil {
				t.Fatalf("Replace = %v, want nil", err)
			}

			tc.check(t, f, before, after)

			if got := f.store.entries[len(f.store.entries)-1]; got.Action != "sink:update" ||
				got.ObjectID != before.ID {
				t.Fatalf("audit entry = %+v, want sink:update of %s", got, before.ID)
			}
		})
	}
}

// checkNewChatURL returns the check of a replace of the URL of a chat sink: a
// new ref, and a stored value that opens to the new URL.
func checkNewChatURL(newURL string) func(t *testing.T, f fixture, before, after notify.Sink) {
	return func(t *testing.T, f fixture, before, after notify.Sink) {
		t.Helper()

		if len(after.SecretRefs) != 1 || after.SecretRefs[0] == before.SecretRefs[0] {
			t.Fatalf("refs = %v, want a new ref: a value never replaces the value of a ref", after.SecretRefs)
		}

		sealed, err := f.store.SinkSecrets(t.Context(), shop, after.ID)
		if err != nil || len(sealed) != 1 {
			t.Fatalf("stored secrets = %d (%v), want 1", len(sealed), err)
		}

		values, err := notifytest.Sealer().Open(t.Context(), after, sealed)
		if err != nil || string(values[0]) != newURL {
			t.Fatalf("stored value opens to %q (%v), want the new URL", values, err)
		}
	}
}

func TestReplaceRefusals(t *testing.T) {
	t.Parallel()

	cases := []refusedReplace{
		{"a stale version", seedHook, func(v int64) sinks.Change { return change(v-1, "Hook") }, notify.ErrConflict},
		{"a target for a chat sink", seedChat, func(v int64) sinks.Change {
			c := change(v, "Chat")
			c.Target = new("chat.example.com")

			return c
		}, notify.ErrInvalid},
		{"a url for a webhook", seedHook, func(v int64) sinks.Change {
			c := change(v, "Hook")
			c.URL = new("https://hooks.slack.com/x")

			return c
		}, notify.ErrInvalid},
		{"a loopback target", seedHook, func(v int64) sinks.Change {
			c := change(v, "Hook")
			c.Target = new("https://127.0.0.1/")

			return c
		}, egress.ErrRefused},
		{"a chat URL on a private address", seedChat, func(v int64) sinks.Change {
			c := change(v, "Chat")
			c.URL = new("https://10.1.2.3/hook")

			return c
		}, egress.ErrRefused},
		{"an empty name", seedHook, func(v int64) sinks.Change { return change(v, "") }, notify.ErrInvalid},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			before := tc.seed(t, f)
			entries := len(f.store.entries)

			_, err := f.svc.Replace(t.Context(), f.caller, shop, before.ID, tc.change(before.Version))
			if !errors.Is(err, tc.want) {
				t.Fatalf("Replace = %v, want %v", err, tc.want)
			}

			if len(f.store.entries) != entries {
				t.Fatal("a refused replace wrote an audit entry")
			}
		})
	}
}

func TestReplaceAndDeleteUnknownSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	_, err := f.svc.Replace(t.Context(), f.caller, shop, "snk-nothing", change(1, "x"))
	if !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Replace = %v, want ErrNotFound", err)
	}

	if err = f.svc.Delete(t.Context(), f.caller, shop, "snk-nothing"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}

	if _, err = f.svc.Get(t.Context(), shop, "snk-nothing"); !errors.Is(err, notify.ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}

	if _, err = f.svc.List(t.Context(), "nowhere"); !errors.Is(err, namespace.ErrNotFound) {
		t.Fatalf("List = %v, want namespace.ErrNotFound", err)
	}
}

func TestDeleteAndList(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	hook := makeHook(t, f).Sink
	chat := makeChat(t, f)

	all, err := f.svc.List(t.Context(), shop)
	if err != nil || len(all) != 2 {
		t.Fatalf("List = %d sinks (%v), want 2", len(all), err)
	}

	for _, s := range all {
		if got, getErr := f.svc.Get(t.Context(), shop, s.ID); getErr != nil || got.ID != s.ID {
			t.Fatalf("Get %s = %+v, %v", s.ID, got, getErr)
		}
	}

	if err = f.svc.Delete(t.Context(), f.caller, shop, hook.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	e := f.store.entries[len(f.store.entries)-1]
	if e.Action != "sink:delete" || e.ObjectID != hook.ID {
		t.Fatalf("audit entry = %+v, want sink:delete of %s", e, hook.ID)
	}

	left, err := f.svc.List(t.Context(), shop)
	if err != nil || len(left) != 1 || left[0].ID != chat.ID {
		t.Fatalf("List after the delete = %+v, %v, want only the chat sink", left, err)
	}
}

func TestAuditKindOfPrincipal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind identity.Kind
		want string
	}{
		{identity.KindPerson, audit.PrincipalKindPerson},
		{identity.KindService, audit.PrincipalKindService},
		{identity.KindWorkload, audit.PrincipalKindWorkload},
		{"", ""},
		{"robot", ""},
	}

	for _, tc := range cases {
		t.Run("kind "+string(tc.kind), func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			f.caller.Principal.Kind = tc.kind

			makeHook(t, f)

			if got := f.store.entries[0].PrincipalKind; got != tc.want {
				t.Fatalf("PrincipalKind = %q, want %q", got, tc.want)
			}

			if err := f.store.entries[0].Validate(); err != nil {
				t.Fatalf("audit entry is not valid: %v", err)
			}
		})
	}
}

// checkNewWebhookURL returns the check of a replace that gave a webhook sink
// the new URL: the host is the target, the URL is a new sealed item first, the
// old URL item is gone, and the signing key keeps its ref and its value.
func checkNewWebhookURL(newURL string) func(t *testing.T, f fixture, before, after notify.Sink) {
	return func(t *testing.T, f fixture, before, after notify.Sink) {
		t.Helper()

		if after.Target != "new.example.com" || len(after.SecretRefs) != 2 ||
			after.SecretRefs[0] == before.SecretRefs[0] || after.SecretRefs[1] != before.SecretRefs[1] {
			t.Fatalf("sink = %+v, want the new host, a new URL ref and the old key ref", after)
		}

		sealed, err := f.store.SinkSecrets(t.Context(), shop, after.ID)
		if err != nil || len(sealed) != 2 {
			t.Fatalf("stored secrets = %d (%v), want the new URL and the key", len(sealed), err)
		}

		dest, err := notifytest.Sealer().OpenDestination(t.Context(), after, sealed)
		if err != nil {
			t.Fatalf("OpenDestination: %v", err)
		}

		defer dest.Clear()

		if dest.URL != newURL || len(dest.Keys) != 1 {
			t.Fatalf("destination = %q with %d keys, want the new URL and the old key", dest.URL, len(dest.Keys))
		}

		requireNoCanary(t, sealed)
	}
}

// requireNoCanary fails if an item holds the canary in plaintext.
func requireNoCanary(t *testing.T, items []notify.SealedSecret) {
	t.Helper()

	for _, item := range items {
		if bytes.Contains(item.Ciphertext(), []byte(canary)) {
			t.Fatal("the store got a plaintext URL")
		}
	}
}
