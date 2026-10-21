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
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		mu: sync.Mutex{}, sinks: map[string]notify.Sink{}, secrets: map[string][]notify.SealedSecret{},
		entries: nil, puts: 0,
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

	if len(secrets) > 0 {
		f.secrets[s.ID] = secrets
	} else if !ok {
		f.secrets[s.ID] = nil
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

	for _, s := range f.sinks {
		if s.Scope == scope {
			out = append(out, s)
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

	f.entries = append(f.entries, e)

	return nil
}

// fakeNamespaces knows the namespace shop.
type fakeNamespaces struct{}

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name != shop {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: "ns-1", CreatedAt: time.Time{}, Name: shop, HomeRegion: "eu-west", FailoverVersion: 1,
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

// svcSealer returns the sealer that the fixture uses.
func (fixture) svcSealer() notify.SinkSealer { return notifytest.Sealer() }

func newFixture(t *testing.T, hookErr error) fixture {
	t.Helper()

	store := newFakeStore()
	hook := &fakeSender{kind: notify.SinkWebhook, err: hookErr, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(sinks.Config{
		Store: store, Sealer: notifytest.Sealer(), Namespaces: fakeNamespaces{}, Senders: []notify.Sender{hook},
		Now: func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) },
	})
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
		caller: &sinks.Caller{Principal: identity.Principal{Subject: "alice", Kind: identity.KindPerson}, By: by},
	}
}

func TestNewServiceNeedsPorts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  sinks.Config
	}{
		{"no store", sinks.Config{Store: nil, Sealer: notifytest.Sealer(), Namespaces: fakeNamespaces{}}},
		{"no namespaces", sinks.Config{Store: newFakeStore(), Sealer: notifytest.Sealer(), Namespaces: nil}},
		{"no sealer", sinks.Config{Store: newFakeStore(), Sealer: notify.SinkSealer{}, Namespaces: fakeNamespaces{}}},
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

func TestCreate(t *testing.T) {
	t.Parallel()

	chatURL := "https://hooks.slack.com/services/T000/B000/" + canary

	cases := []struct {
		name       string
		in         sinks.New
		wantTarget string
		wantKey    bool
		wantSecret bool
	}{
		{
			"a webhook", sinks.New{
				Kind: notify.SinkWebhook, Platform: "", Name: "Ops", Target: "https://hooks.example.com/ops", URL: "",
			},
			"https://hooks.example.com/ops", true, true,
		},
		{
			"a chat webhook", sinks.New{
				Kind: notify.SinkChat, Platform: "slack", Name: "Ops chat", Target: "", URL: chatURL,
			},
			"hooks.slack.com", false, true,
		},
		{
			"a chat webhook in upper case", sinks.New{
				Kind: notify.SinkChat, Platform: "mattermost", Name: "Ops chat", Target: "",
				URL: "https://Chat.Example.com:8443/hooks/" + canary,
			},
			"chat.example.com:8443", false, true,
		},
		{
			"an email sink", sinks.New{
				Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "ops@example.com", URL: "",
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

			checkCreated(t, f, got, tc.wantTarget, tc.wantKey, tc.wantSecret)
		})
	}
}

func checkCreated(t *testing.T, f fixture, got sinks.Created, target string, wantKey, wantSecret bool) {
	t.Helper()

	s := got.Sink
	if !strings.HasPrefix(s.ID, notify.SinkIDPrefix) || s.Version != 1 || s.Target != target ||
		s.CreatedBy != "alice" || s.Scope != shop {
		t.Fatalf("sink = %+v, want an snk- ID, version 1, target %q, creator alice", s, target)
	}

	if (got.SigningKey != "") != wantKey || (wantKey && len(got.SigningKey) != 64) {
		t.Fatalf("signing key = %q, want a key of 64 hex digits: %t", got.SigningKey, wantKey)
	}

	sealed, err := f.store.SinkSecrets(t.Context(), shop, s.ID)
	if err != nil || (len(sealed) == 1) != wantSecret {
		t.Fatalf("stored secrets = %d (%v), want one: %t", len(sealed), err, wantSecret)
	}

	for _, item := range sealed {
		if bytes.Contains(item.Ciphertext(), []byte(canary)) || (got.SigningKey != "" &&
			bytes.Contains(item.Ciphertext(), []byte(got.SigningKey))) {
			t.Fatal("the store got a plaintext value")
		}
	}

	if len(f.store.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(f.store.entries))
	}

	e := f.store.entries[0]
	if e.Action != "sink:create" || e.ObjectID != s.ID || e.Namespace != shop || e.Subject != "alice" ||
		e.PrincipalKind != audit.PrincipalKindPerson || e.Kind != audit.KindChange || e.RequestID != "req-1" {
		t.Fatalf("audit entry = %+v, want sink:create of %s by alice", e, s.ID)
	}

	if fmt.Sprintf("%+v", e) != strings.ReplaceAll(fmt.Sprintf("%+v", e), canary, "") ||
		(got.SigningKey != "" && strings.Contains(fmt.Sprintf("%+v", e), got.SigningKey)) {
		t.Fatal("the audit entry holds a secret")
	}
}

func TestCreateRefused(t *testing.T) {
	t.Parallel()

	chat := func(url string) sinks.New {
		return sinks.New{Kind: notify.SinkChat, Platform: "slack", Name: "Chat", Target: "", URL: url}
	}
	hook := func(target string) sinks.New {
		return sinks.New{Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: target, URL: ""}
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
		{"a chat URL with user information", chat("https://user:pw@hooks.slack.com/" + canary), true},
		{"a chat URL with a fragment", chat("https://hooks.slack.com/x#" + canary), true},
		{"a chat URL in a number host", chat("https://2130706433/" + canary), true},
		{"a chat URL that does not parse", chat("https://hooks.slack.com/%zz" + canary), true},
		{"no chat URL", chat(""), true},
		{"a chat URL over the bound", chat("https://hooks.slack.com/" + strings.Repeat("a", egress.MaxURLBytes)), true},
		{"a chat target", sinks.New{
			Kind: notify.SinkChat, Platform: "slack", Name: "Chat", Target: "hooks.slack.com",
			URL: "https://hooks.slack.com/x",
		}, false},
		{"a chat sink with no platform", sinks.New{
			Kind: notify.SinkChat, Platform: "", Name: "Chat", Target: "", URL: "https://hooks.slack.com/x",
		}, false},
		{"a chat sink with an unknown platform", sinks.New{
			Kind: notify.SinkChat, Platform: "irc", Name: "Chat", Target: "", URL: "https://hooks.slack.com/x",
		}, false},
		{"a webhook with a url", sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: "Hook", Target: "https://hooks.example.com/", URL: "https://x.example.com",
		}, false},
		{"a webhook with a platform", sinks.New{
			Kind: notify.SinkWebhook, Platform: "slack", Name: "Hook", Target: "https://hooks.example.com/", URL: "",
		}, false},
		{"a webhook over http", hook("http://hooks.example.com/" + canary), true},
		{"a webhook on loopback", hook("https://127.0.0.1:8443/" + canary), true},
		{"a webhook on the metadata address", hook("https://169.254.169.254/latest/" + canary), true},
		{"a webhook on IPv6 loopback", hook("https://[::1]/" + canary), true},
		{"a webhook with user information", hook("https://u:p@hooks.example.com/" + canary), true},
		{"a webhook with no target", hook(""), true},
		{"an email sink with a url", sinks.New{
			Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "ops@example.com", URL: "https://hooks.example.com",
		}, false},
		{"an email sink with a name in the address", sinks.New{
			Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "Ops <ops@example.com>", URL: "",
		}, false},
		{"an unknown kind", sinks.New{Kind: "pager", Platform: "", Name: "P", Target: "x", URL: ""}, false},
		{"no name", sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: "", Target: "https://hooks.example.com/", URL: "",
		}, false},
		{"a name over the bound", sinks.New{
			Kind: notify.SinkWebhook, Platform: "", Name: strings.Repeat("n", notify.MaxNameLen+1),
			Target: "https://hooks.example.com/", URL: "",
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
		URL: "https://hooks.slack.com/services/T0/B0/" + canary,
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
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	return got
}

func ptr[T any](v T) *T { return &v }

func TestReplace(t *testing.T) {
	t.Parallel()

	newURL := "https://hooks.slack.com/services/T0/B0/other-" + canary

	cases := []struct {
		name    string
		chat    bool
		change  func(version int64) sinks.Change
		wantErr error
		check   func(t *testing.T, f fixture, before, after notify.Sink)
	}{
		{
			name: "a new name of a webhook", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Ops team", Target: nil, URL: nil}
			},
			check: func(t *testing.T, _ fixture, before, after notify.Sink) {
				t.Helper()

				if after.Name != "Ops team" || after.Version != before.Version+1 || after.Target != before.Target ||
					!slices.Equal(after.SecretRefs, before.SecretRefs) {
					t.Fatalf("sink = %+v, want only the name changed", after)
				}
			},
		},
		{
			name: "a new target of a webhook", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Hook", Target: ptr("https://new.example.com/"), URL: nil}
			},
			check: func(t *testing.T, _ fixture, _, after notify.Sink) {
				t.Helper()

				if after.Target != "https://new.example.com/" {
					t.Fatalf("target = %q, want the new target", after.Target)
				}
			},
		},
		{
			name: "a new URL of a chat sink", chat: true,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Chat", Target: nil, URL: &newURL}
			},
			check: func(t *testing.T, f fixture, before, after notify.Sink) {
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
			},
		},
		{
			name: "a stale version", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v - 1, Name: "Hook", Target: nil, URL: nil}
			},
			wantErr: notify.ErrConflict,
		},
		{
			name: "a target for a chat sink", chat: true,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Chat", Target: ptr("chat.example.com"), URL: nil}
			},
			wantErr: notify.ErrInvalid,
		},
		{
			name: "a url for a webhook", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Hook", Target: nil, URL: ptr("https://hooks.slack.com/x")}
			},
			wantErr: notify.ErrInvalid,
		},
		{
			name: "a loopback target", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Hook", Target: ptr("https://127.0.0.1/"), URL: nil}
			},
			wantErr: egress.ErrRefused,
		},
		{
			name: "a chat URL on a private address", chat: true,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "Chat", Target: nil, URL: ptr("https://10.1.2.3/hook")}
			},
			wantErr: egress.ErrRefused,
		},
		{
			name: "an empty name", chat: false,
			change: func(v int64) sinks.Change {
				return sinks.Change{Version: v, Name: "", Target: nil, URL: nil}
			},
			wantErr: notify.ErrInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			var before notify.Sink
			if tc.chat {
				before = makeChat(t, f)
			} else {
				before = makeHook(t, f).Sink
			}

			entries := len(f.store.entries)

			after, err := f.svc.Replace(t.Context(), f.caller, shop, before.ID, tc.change(before.Version))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Replace = %v, want %v", err, tc.wantErr)
				}

				if len(f.store.entries) != entries {
					t.Fatal("a refused replace wrote an audit entry")
				}

				return
			}

			if err != nil {
				t.Fatalf("Replace = %v, want nil", err)
			}

			tc.check(t, f, before, after)

			if got := f.store.entries[len(f.store.entries)-1]; got.Action != "sink:update" || got.ObjectID != before.ID {
				t.Fatalf("audit entry = %+v, want sink:update of %s", got, before.ID)
			}
		})
	}
}

func TestReplaceAndDeleteUnknownSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	_, err := f.svc.Replace(t.Context(), f.caller, shop, "snk-nothing", sinks.Change{
		Version: 1, Name: "x", Target: nil, URL: nil,
	})
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

	if left, _ := f.svc.List(t.Context(), shop); len(left) != 1 || left[0].ID != chat.ID {
		t.Fatalf("List after the delete = %+v, want only the chat sink", left)
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
