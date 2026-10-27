package notifications_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/notify"
)

// authCanary stands for an access token. It must never appear in an answer,
// an audit entry, a log line or a stored value in plaintext.
const authCanary = "syt_TOKEN-CANARY-5d1e7a"

// tokenSender is a chat sender for a platform that needs an access token: the
// platform matrix. It records the messages of the test route.
type tokenSender struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (*tokenSender) Kind() notify.SinkKind { return notify.SinkChat }

func (*tokenSender) TakesAuthSecret(platform string) bool { return platform == "matrix" }

func (s *tokenSender) Send(_ context.Context, m notify.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// The service clears its plaintext after the send, so the recorder keeps
	// its own copy of each secret.
	copies := make([][]byte, len(m.Secrets))
	for i := range m.Secrets {
		copies[i] = bytes.Clone(m.Secrets[i])
	}

	m.Secrets = copies
	s.sent = append(s.sent, m)

	return nil
}

func (s *tokenSender) messages() []notify.Message {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]notify.Message(nil), s.sent...)
}

func newTokenFixture(t *testing.T) (fixture, *tokenSender) {
	t.Helper()

	chat := &tokenSender{mu: sync.Mutex{}, sent: nil}

	return newFixtureWithSenders(t, chat), chat
}

// matrixBody is the body of the create of a destination for the platform
// platform, with the access token when token is not empty.
func matrixBody(t *testing.T, platform, token string) string {
	t.Helper()

	body := map[string]string{
		"kind": "chat", "platform": platform, "name": "Ops room",
		"url": "https://chat.example.com/_matrix/client/v3/rooms/" + canary,
	}
	if token != "" {
		body["accessToken"] = token
	}

	return jsonString(t, body)
}

func TestAccessTokenIsSealedAndNeverShown(t *testing.T) {
	t.Parallel()

	f, chat := newTokenFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, matrixBody(t, "matrix", authCanary)))
	wantCode(t, "create", got, http.StatusCreated)

	s := decode[sinkWire](t, got)
	if s.Platform != "matrix" || s.Target != "chat.example.com" || !s.URLSet {
		t.Fatalf("created = %+v, want a matrix chat sink with the host as target", s)
	}

	reads := []string{
		f.do(t, get("reader", base)).body, f.do(t, get("reader", base+"/"+s.ID)).body,
	}

	// A replace answers the sink too.
	put := fmt.Sprintf(`{"version":1,"name":"Ops room 2","accessToken":%q}`, authCanary+"-2")
	replaced := f.do(t, send("admin", http.MethodPut, base+"/"+s.ID, put))
	wantCode(t, "replace", replaced, http.StatusOK)

	for _, text := range slices.Concat([]string{got.body, replaced.body}, reads, f.storedTexts(t, s.ID)) {
		if strings.Contains(text, authCanary) || strings.Contains(text, "accessToken") {
			t.Errorf("the access token or its field shows in %q", text)
		}
	}

	// The test route hands the new token to the sender, as its one secret.
	test := f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
	wantCode(t, "test", test, http.StatusOK)

	msgs := chat.messages()
	if len(msgs) != 1 || len(msgs[0].Secrets) != 1 || string(msgs[0].Secrets[0]) != authCanary+"-2" {
		t.Fatalf("sender got %d messages, want 1 with the new access token as its one secret", len(msgs))
	}

	if strings.Contains(test.body, authCanary) || strings.Contains(f.logs.String(), authCanary) {
		t.Error("the answer of the test or a log line holds the access token")
	}
}

// storedTexts returns what the server keeps about the sink id: the log, the
// audit entries and the sealed secret items. It checks that there are two
// items, the URL and the access token.
func (f fixture) storedTexts(t *testing.T, id string) []string {
	t.Helper()

	entries := f.entries(t)
	shown := make([]string, 0, 1+len(entries)+2)
	shown = append(shown, f.logs.String())

	for i := range entries {
		shown = append(shown, fmt.Sprintf("%+v", entries[i]))
	}

	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", id)
	if err != nil || len(sealed) != 2 {
		t.Fatalf("stored secrets = %d (%v), want 2: the URL and the access token", len(sealed), err)
	}

	for _, item := range sealed {
		shown = append(shown, string(item.Ciphertext()))
	}

	return shown
}

type refusalCase struct {
	name     string
	platform string
	token    string
	rule     string
}

func TestAccessTokenRefusals(t *testing.T) {
	t.Parallel()

	cases := []refusalCase{
		{"a platform that takes no token", "slack", authCanary, "takes no access token"},
		{"a platform that needs a token", "matrix", "", "needs an access token"},
		{"a token with a space", "matrix", "syt_a b" + authCanary, "printable ASCII"},
		{"a token with a line break", "matrix", "syt_a\r\nX-Evil: 1" + authCanary, "printable ASCII"},
		{"a token with a non-ASCII character", "matrix", "syt_é" + authCanary, "printable ASCII"},
		{"a token over the limit", "matrix", strings.Repeat("t", 4097), "printable ASCII"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRefusal(t, tc)
		})
	}
}

// checkRefusal creates a destination that the server must refuse, and checks
// the answer, that no text holds the token or the URL, and that nothing
// changed.
func checkRefusal(t *testing.T, tc refusalCase) {
	t.Helper()

	f, chat := newTokenFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, matrixBody(t, tc.platform, tc.token)))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, `"errorCode":"sink.access-token"`) ||
		!strings.Contains(got.body, tc.rule) {
		t.Fatalf("create: %d %s, want 400 sink.access-token with %q", got.code, got.body, tc.rule)
	}

	for _, text := range []string{got.body, f.logs.String()} {
		if strings.Contains(text, authCanary) || strings.Contains(text, canary) {
			t.Errorf("the refusal or a log line echoes the token or the URL: %s", text)
		}
	}

	if n := len(decode[listWire](t, f.do(t, get("reader", base))).Sinks); n != 0 || len(chat.messages()) != 0 {
		t.Errorf("sinks after the refusal = %d, want 0", n)
	}

	if n := len(f.sinkEntries(t)); n != 0 {
		t.Errorf("audit entries after the refusal = %d, want 0", n)
	}
}

func TestAccessTokenIsRefusedWhereTheServerCannotSayItIsNeeded(t *testing.T) {
	t.Parallel()

	// The default fixture has no chat sender that knows access tokens, so the
	// server fails closed: it takes no token for any platform.
	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, matrixBody(t, "matrix", authCanary)))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, `"errorCode":"sink.access-token"`) {
		t.Fatalf("create: %d %s, want 400 sink.access-token", got.code, got.body)
	}

	hook := jsonString(t, map[string]string{
		"kind": "webhook", "name": "h", "target": "https://hooks.example.com/" + canary, "accessToken": authCanary,
	})

	got = f.do(t, send("admin", http.MethodPost, base, hook))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, `"errorCode":"sink.access-token"`) ||
		strings.Contains(got.body, authCanary) {
		t.Fatalf("create webhook: %d %s, want 400 sink.access-token with no token", got.code, got.body)
	}
}

func TestReplaceKeepsTheTokenWhenTheURLChanges(t *testing.T) {
	t.Parallel()

	f, chat := newTokenFixture(t)

	created := f.do(t, send("admin", http.MethodPost, base, matrixBody(t, "matrix", authCanary)))
	wantCode(t, "create", created, http.StatusCreated)

	s := decode[sinkWire](t, created)

	put := `{"version":1,"name":"Ops room","url":"https://chat2.example.com/_matrix/client/v3/rooms/` + canary + `"}`
	wantCode(t, "replace", f.do(t, send("admin", http.MethodPut, base+"/"+s.ID, put)), http.StatusOK)
	wantCode(t, "test", f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", "")), http.StatusOK)

	msgs := chat.messages()
	if len(msgs) != 1 || len(msgs[0].Secrets) != 1 || string(msgs[0].Secrets[0]) != authCanary ||
		!strings.HasPrefix(msgs[0].To, "https://chat2.example.com/") {
		t.Fatalf("sender got %d messages, want 1 to the new URL with the old access token", len(msgs))
	}
}

func TestReplaceRefusesATokenForAnotherKind(t *testing.T) {
	t.Parallel()

	f, _ := newTokenFixture(t)
	s := f.makeWebhook(t)

	put := fmt.Sprintf(`{"version":1,"name":"Ops","accessToken":%q}`, authCanary)

	got := f.do(t, send("admin", http.MethodPut, base+"/"+s.ID, put))
	if got.code != http.StatusBadRequest || !strings.Contains(got.body, `"errorCode":"sink.access-token"`) ||
		strings.Contains(got.body, authCanary) || strings.Contains(f.logs.String(), authCanary) {
		t.Fatalf("replace: %d %s, want 400 sink.access-token with no token", got.code, got.body)
	}
}
