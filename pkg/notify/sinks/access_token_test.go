package sinks_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

const authCanary = "syt_TOKEN-CANARY-77aa"

// tokenSender is a chat sender for a platform that needs an access token: the
// platform matrix.
type tokenSender struct {
	fakeSender
}

func (*tokenSender) TakesAuthSecret(platform string) bool { return platform == "matrix" }

func newTokenFixture(t *testing.T) (fixture, *tokenSender) {
	t.Helper()

	f := newFixture(t, nil)
	chat := &tokenSender{fakeSender{kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0}}

	svc, err := sinks.NewService(config(f.store, fakeNamespaces{notHome: false}, f.hook, chat))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc

	return f, chat
}

func matrixSink(token string) sinks.New {
	return sinks.New{
		Kind: notify.SinkChat, Platform: "matrix", Name: "Ops room", Target: "",
		URL: "https://chat.example.com/_matrix/client/v3/rooms/room", AccessToken: token,
	}
}

func TestCreateWithAnAccessToken(t *testing.T) {
	t.Parallel()

	f, chat := newTokenFixture(t)

	made, err := f.svc.Create(t.Context(), f.caller, shop, matrixSink(authCanary))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if len(made.Sink.SecretRefs) != 2 || made.SigningKey != "" {
		t.Fatalf("sink = %+v, want a URL item, a token item and no signing key", made.Sink)
	}

	checkStoredWithoutToken(t, f, made.Sink.ID)

	if _, err = f.svc.SendTest(t.Context(), f.caller, shop, made.Sink.ID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}

	if len(chat.got) != 1 || len(chat.got[0].Secrets) != 1 || string(chat.got[0].Secrets[0]) != authCanary {
		t.Fatalf("sender got %+v, want the access token as the one secret", chat.got)
	}
}

// checkStoredWithoutToken checks that the store holds the two sealed items of
// the sink id, and that no stored item and no audit entry holds the token.
func checkStoredWithoutToken(t *testing.T, f fixture, id string) {
	t.Helper()

	sealed, err := f.store.SinkSecrets(t.Context(), shop, id)
	if err != nil || len(sealed) != 2 {
		t.Fatalf("stored items = %d (%v), want 2", len(sealed), err)
	}

	for _, item := range sealed {
		if strings.Contains(string(item.Ciphertext()), authCanary) {
			t.Error("a stored item holds the token in plaintext")
		}
	}

	for i := range f.store.entries {
		if strings.Contains(fmt.Sprintf("%+v", f.store.entries[i]), authCanary) {
			t.Errorf("an audit entry holds the token: %+v", f.store.entries[i])
		}
	}
}

func TestAccessTokenRefusals(t *testing.T) {
	t.Parallel()

	slack := matrixSink(authCanary)
	slack.Platform = "slack"

	hook := sinks.New{
		Kind: notify.SinkWebhook, Platform: "", Name: "h", Target: "https://hooks.example.com/x", URL: "",
		AccessToken: authCanary,
	}

	cases := []struct {
		name string
		in   sinks.New
		rule string
	}{
		{"a platform that takes no token", slack, "takes no access token"},
		{"a webhook", hook, "Only a chat destination"},
		{"no token for a platform that needs one", matrixSink(""), "needs an access token"},
		{"a token with a space", matrixSink("a b" + authCanary), "printable ASCII"},
		{"a token with a line break", matrixSink("a\r\nb" + authCanary), "printable ASCII"},
		{"a token over the limit", matrixSink(strings.Repeat("t", notify.MaxSecretLen+1)), "printable ASCII"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f, _ := newTokenFixture(t)

			_, err := f.svc.Create(t.Context(), f.caller, shop, tc.in)
			if !errors.Is(err, sinks.ErrAccessToken) || !errors.Is(err, notify.ErrInvalid) {
				t.Fatalf("Create = %v, want ErrAccessToken and ErrInvalid", err)
			}

			if !strings.Contains(sinks.AccessTokenRule(err), tc.rule) {
				t.Errorf("rule = %q, want %q", sinks.AccessTokenRule(err), tc.rule)
			}

			if strings.Contains(err.Error(), authCanary) || strings.Contains(sinks.AccessTokenRule(err), authCanary) {
				t.Errorf("the error holds the token: %v", err)
			}

			if f.store.puts != 0 {
				t.Errorf("the store got %d writes, want none", f.store.puts)
			}
		})
	}
}

func TestAccessTokenRuleOfAnotherError(t *testing.T) {
	t.Parallel()

	if got := sinks.AccessTokenRule(notify.ErrInvalid); got != "" {
		t.Errorf("AccessTokenRule(ErrInvalid) = %q, want empty", got)
	}
}

func TestReplaceAccessToken(t *testing.T) {
	t.Parallel()

	f, chat := newTokenFixture(t)

	made, err := f.svc.Create(t.Context(), f.caller, shop, matrixSink(authCanary))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newToken := authCanary + "-2"
	oldURLRef, oldTokenRef := made.Sink.SecretRefs[0], made.Sink.SecretRefs[1]

	next, err := f.svc.Replace(t.Context(), f.caller, shop, made.Sink.ID, sinks.Change{
		Version: 1, Name: "Ops room", Target: nil, URL: nil, AccessToken: &newToken,
	})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if len(next.SecretRefs) != 2 || next.SecretRefs[0] != oldURLRef || next.SecretRefs[1] == oldTokenRef {
		t.Fatalf("refs = %v, want the old URL ref and a new token ref", next.SecretRefs)
	}

	if _, err = f.svc.SendTest(t.Context(), f.caller, shop, made.Sink.ID); err != nil {
		t.Fatalf("SendTest: %v", err)
	}

	if len(chat.got) != 1 || string(chat.got[0].Secrets[0]) != newToken {
		t.Fatalf("sender got %+v, want the new access token", chat.got)
	}

	empty := ""

	_, err = f.svc.Replace(t.Context(), f.caller, shop, made.Sink.ID, sinks.Change{
		Version: 2, Name: "Ops room", Target: nil, URL: nil, AccessToken: &empty,
	})
	if !errors.Is(err, sinks.ErrAccessToken) {
		t.Fatalf("Replace with an empty token = %v, want ErrAccessToken", err)
	}
}

// TestReplaceRefusesAPathThatLeavesItsPlace sends a hostile URL to the replace
// of a chat sink: a segment that decodes to a slash or to a dot segment would
// lead the request, with the token of the sink, to another path on the host.
func TestReplaceRefusesAPathThatLeavesItsPlace(t *testing.T) {
	t.Parallel()

	f, _ := newTokenFixture(t)

	made, err := f.svc.Create(t.Context(), f.caller, shop, matrixSink(authCanary))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, path := range []string{"/rooms/x%2F..%2F..%2Fadmin", "/rooms/%2e%2e", "/rooms/../admin", "/rooms/a%3Fb"} {
		url := "https://chat.example.com/_matrix/client/v3" + path

		_, err = f.svc.Replace(t.Context(), f.caller, shop, made.Sink.ID, sinks.Change{
			Version: 1, Name: "Ops room", Target: nil, URL: &url, AccessToken: nil,
		})
		if !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("Replace with the path %q = %v, want ErrInvalid", path, err)
		}

		if err != nil && strings.Contains(err.Error(), "admin") {
			t.Errorf("the error holds a part of the URL: %v", err)
		}
	}
}
