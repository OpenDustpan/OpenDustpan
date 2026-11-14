package notifications_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/internal/fakecred"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// errNetwork stands for an error of the network. The tests wrap it with an
// address and a canary that no answer may show.
var errNetwork = errors.New("network error")

func TestCreateWebhookAnswersTheKeyOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create", got, http.StatusCreated)

	s := decode[sinkWire](t, got)
	if !strings.HasPrefix(s.ID, notify.SinkIDPrefix) || s.Kind != "webhook" || s.Name != "Ops" ||
		s.Namespace != "acme" ||
		s.Target != webhookHost ||
		s.Version != 1 ||
		s.CreatedBy != "admin" ||
		!s.URLSet ||
		!hex64.MatchString(s.SigningKey) {
		t.Fatalf("created = %+v, want a webhook at version 1 with a signing key of 64 hex digits", s)
	}

	if loc := got.header.Get("Location"); loc != base+"/"+s.ID {
		t.Errorf("Location = %q, want %q", loc, base+"/"+s.ID)
	}

	if cc := got.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	checkKeyNotAnsweredAgain(t, f, s)
	checkKeyNotStored(t, f, s)
}

func TestWebhookURLIsWriteOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	created := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create", created, http.StatusCreated)

	s := decode[sinkWire](t, created)
	one := base + "/" + s.ID
	moved := f.do(t, send("admin", http.MethodPut, one,
		`{"version":1,"name":"Ops","target":"https://new.example.com/path/`+canary+`?k=`+canary+`"}`))
	wantCode(t, "replace", moved, http.StatusOK)

	answers := map[string]response{
		"create":  created,
		"list":    f.do(t, get("reader", base)),
		"get":     f.do(t, get("reader", one)),
		"replace": moved,
		"test":    f.do(t, send("admin", http.MethodPost, one+"/test", "")),
	}

	for name, got := range answers {
		for _, hidden := range []string{canary, "/ops", "/path", "token=", "?k="} {
			if strings.Contains(got.body, hidden) {
				t.Errorf("%s answer shows %q: %s", name, hidden, got.body)
			}
		}
	}

	shown := []string{f.logs.String(), fmt.Sprintf("%+v", f.sinkEntries(t))}
	for _, text := range shown {
		if strings.Contains(text, canary) || strings.Contains(text, "hooks.example.com/ops") {
			t.Errorf("an audit entry or a log line shows the URL: %s", text)
		}
	}

	for _, name := range []string{"list", "get"} {
		if !strings.Contains(answers[name].body, `"target":"new.example.com"`) {
			t.Errorf("%s answer = %s, want the host as target", name, answers[name].body)
		}
	}
}

// TestRefusedWebhookWritesShowNoURL proves that a create and a replace that the
// server refuses answer with a 400 that holds no part of the URL, and that no
// audit entry and no log line holds it.
func TestRefusedWebhookWritesShowNoURL(t *testing.T) {
	t.Parallel()

	tail := "/ops/" + canary + "?token=" + canary
	targets := map[string]string{
		"a scheme that is not https":  "http://hooks.example.com" + tail,
		"a port the operator refuses": "https://hooks.example.com:8443" + tail,
		"a metadata address":          "https://169.254.169.254" + tail,
		"a sub-delimiter in the host": "https://hooks.example.com;" + canary + tail,
		"an empty port":               "https://hooks.example.com:" + tail,
		"raw Unicode in the host":     "https://h\u00f6\u00f6ks.example.com" + tail,
	}

	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			created := f.do(t, send("admin", http.MethodPost, base,
				jsonString(t, map[string]string{"kind": "webhook", "name": "Ops", "target": target})))
			wantCode(t, "refused create", created, http.StatusBadRequest)

			ok := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
			wantCode(t, "create", ok, http.StatusCreated)

			one := base + "/" + decode[sinkWire](t, ok).ID
			moved := f.do(t, send("admin", http.MethodPut, one,
				jsonString(t, map[string]any{"version": 1, "name": "Ops", "target": target})))
			wantCode(t, "refused replace", moved, http.StatusBadRequest)

			texts := []string{
				created.body, moved.body, f.logs.String(), fmt.Sprintf("%+v", f.sinkEntries(t)),
				f.do(t, get("reader", one)).body,
			}
			for _, text := range texts {
				for _, hidden := range []string{canary, "/ops", "token="} {
					if strings.Contains(text, hidden) {
						t.Errorf("a refused write shows %q: %s", hidden, text)
					}
				}
			}
		})
	}
}

// TestReplayedReplaceShowsOnlyTheHost proves that the replay of a replace with
// the same Idempotency-Key answers the host, and that the stored replay holds
// no part of the URL.
func TestReplayedReplaceShowsOnlyTheHost(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	created := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create", created, http.StatusCreated)

	one := base + "/" + decode[sinkWire](t, created).ID
	body := `{"version":1,"name":"Ops","target":"https://new.example.com/path/` + canary + `?k=` + canary + `"}`

	first := f.do(t, keyed(send("admin", http.MethodPut, one, body), "r1"))
	wantCode(t, "replace", first, http.StatusOK)

	again := f.do(t, keyed(send("admin", http.MethodPut, one, body), "r1"))
	wantCode(t, "replay", again, http.StatusOK)

	if again.body != first.body {
		t.Errorf("replay = %s, want %s", again.body, first.body)
	}

	for _, text := range []string{first.body, again.body, f.logs.String(), fmt.Sprintf("%+v", f.sinkEntries(t))} {
		if strings.Contains(text, canary) || strings.Contains(text, "/path") {
			t.Errorf("a replace or its replay shows the URL: %s", text)
		}
	}

	if !strings.Contains(again.body, `"target":"new.example.com"`) {
		t.Errorf("replay = %s, want the host as target", again.body)
	}
}

// checkKeyNotAnsweredAgain checks that no later read holds the signing key.
func checkKeyNotAnsweredAgain(t *testing.T, f fixture, s sinkWire) {
	t.Helper()

	for _, target := range []string{base, base + "/" + s.ID} {
		again := f.do(t, get("reader", target))
		wantCode(t, "read", again, http.StatusOK)

		if strings.Contains(again.body, s.SigningKey) || strings.Contains(again.body, "signingKey") {
			t.Errorf("read of %s answers the key: %s", target, again.body)
		}
	}
}

// requireSealed fails if an item holds one of the plain texts.
func requireSealed(t *testing.T, items []notify.SealedSecret, plains ...string) {
	t.Helper()

	for _, item := range items {
		for _, plain := range plains {
			if strings.Contains(string(item.Ciphertext()), plain) {
				t.Errorf("the store holds %q in plaintext", plain)
			}
		}
	}
}

// checkKeyNotStored checks that the store holds the signing key sealed, and
// that the audit entry and the log hold neither the key nor the plaintext.
func checkKeyNotStored(t *testing.T, f fixture, s sinkWire) {
	t.Helper()

	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", s.ID)
	if err != nil || len(sealed) != 2 {
		t.Fatalf("stored secrets = %d (%v), want 2: the URL and the signing key", len(sealed), err)
	}

	requireSealed(t, sealed, s.SigningKey, canary)

	entries := f.sinkEntries(t)
	if len(entries) != 1 || entries[0].Action != "sink:create" || entries[0].ObjectID != s.ID ||
		entries[0].Subject != "admin" || entries[0].Object != "acme" || entries[0].Kind != audit.KindChange {
		t.Fatalf("audit entries = %+v, want one sink:create entry of %s", entries, s.ID)
	}

	if strings.Contains(fmt.Sprintf("%+v", entries[0]), s.SigningKey) ||
		strings.Contains(f.logs.String(), s.SigningKey) {
		t.Error("an audit entry or a log line holds the signing key")
	}
}

func TestCreateChatKeepsTheURLSecret(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, chatBody(t, "Ops chat")))
	wantCode(t, "create", got, http.StatusCreated)

	s := decode[sinkWire](t, got)
	if s.Kind != "chat" || s.Platform != "slack" || s.Target != "hooks.slack.com" || !s.URLSet || s.SigningKey != "" ||
		s.Version != 1 {
		t.Fatalf("created = %+v, want a slack chat sink with the host as target and no key", s)
	}

	// The URL and its token appear nowhere: not in an answer, not in an audit
	// entry, not in a log line, not in the store in plaintext.
	for _, text := range shownTexts(t, f, got, s) {
		if strings.Contains(text, canary) || strings.Contains(text, "/services/T0") {
			t.Errorf("the URL of the webhook shows in %q", text)
		}
	}
}

// shownTexts returns every text that the server shows about the chat sink s:
// the answer of the create, the reads, the audit entries, the log and the
// stored secret item.
func shownTexts(t *testing.T, f fixture, created response, s sinkWire) []string {
	t.Helper()

	shown := []string{created.body, f.logs.String()}

	for _, target := range []string{base, base + "/" + s.ID} {
		shown = append(shown, f.do(t, get("reader", target)).body)
	}

	entries := f.entries(t)
	for i := range entries {
		shown = append(shown, fmt.Sprintf("%+v", entries[i]))
	}

	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", s.ID)
	if err != nil || len(sealed) != 1 {
		t.Fatalf("stored secrets = %d (%v), want 1", len(sealed), err)
	}

	return append(shown, string(sealed[0].Ciphertext()))
}

func TestCreateRefusals(t *testing.T) {
	t.Parallel()

	chat := func(url string) string {
		return jsonString(t, map[string]string{"kind": "chat", "platform": "slack", "name": "c", "url": url})
	}
	hook := func(target string) string {
		return jsonString(t, map[string]string{"kind": "webhook", "name": "h", "target": target})
	}

	cases := []struct {
		name   string
		token  string
		body   string
		status int
		code   string
	}{
		{"a chat URL over http", "admin", chat("http://hooks.slack.com/" + canary), 400, "sink.invalid"},
		{"a chat URL on loopback", "admin", chat("https://127.0.0.1/" + canary), 400, "sink.invalid"},
		{"a chat URL on localhost", "admin", chat("https://localhost/" + canary), 400, "sink.invalid"},
		{"a chat URL on the metadata address", "admin", chat("https://169.254.169.254/" + canary), 400, "sink.invalid"},
		{"a chat URL on a private address", "admin", chat("https://192.168.1.10/" + canary), 400, "sink.invalid"},
		{
			"a chat URL with user information",
			"admin",
			chat(fakecred.URL("https", "u", "p", "hooks.slack.com", "/") + canary),
			400,
			"sink.invalid",
		},
		{"a webhook over http", "admin", hook("http://hooks.example.com/" + canary), 400, "sink.invalid"},
		{"a webhook on loopback", "admin", hook("https://127.0.0.1:8443/" + canary), 400, "sink.invalid"},
		{"a webhook on IPv6 loopback", "admin", hook("https://[::1]/" + canary), 400, "sink.invalid"},
		{"a webhook on the metadata address", "admin", hook("https://169.254.169.254/" + canary), 400, "sink.invalid"},
		{"a webhook on ssh", "admin", hook("https://hooks.example.com:22/" + canary), 400, "sink.invalid"},
		{"a webhook on redis", "admin", hook("https://hooks.example.com:6379/" + canary), 400, "sink.invalid"},
		{"a webhook on 8443", "admin", hook("https://hooks.example.com:8443/" + canary), 400, "sink.invalid"},
		{"a chat URL on smtp", "admin", chat("https://chat.example.com:25/" + canary), 400, "sink.invalid"},
		{"a chat URL on 8443", "admin", chat("https://chat.example.com:8443/" + canary), 400, "sink.invalid"},
		{"no kind", "admin", `{"name":"x"}`, 400, "sink.invalid"},
		{"no name", "admin", `{"kind":"webhook"}`, 400, "sink.invalid"},
		{"an unknown kind", "admin", `{"kind":"pager","name":"x","target":"x"}`, 400, "sink.invalid"},
		{
			"an unknown field",
			"admin",
			`{"kind":"webhook","name":"x","target":"https://a.example.com/","extra":1}`,
			400,
			"request.body-invalid",
		},
		{"two documents", "admin", webhookBody(t, "a") + webhookBody(t, "b"), 400, "request.body-invalid"},
		{"a body that is not JSON", "admin", "kind=webhook", 400, "request.body-invalid"},
		{
			"a body over the limit", "admin", `{"kind":"webhook","name":"` + strings.Repeat("n", 70<<10) + `"}`, 400,
			"request.body-invalid",
		},
		{"a reader", "reader", webhookBody(t, "a"), 403, "access.denied"},
		{"no token", "", webhookBody(t, "a"), 401, "auth.unauthenticated"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			got := f.do(t, send(tc.token, http.MethodPost, base, tc.body))
			if got.code != tc.status || !strings.Contains(got.body, `"errorCode":"`+tc.code+`"`) {
				t.Fatalf("create: %d %s, want %d %s", got.code, got.body, tc.status, tc.code)
			}

			// The refusal never echoes a part of the request, and it changes
			// nothing.
			if strings.Contains(got.body, canary) || strings.Contains(f.logs.String(), canary) {
				t.Errorf("the refusal or a log line echoes the URL: %s", got.body)
			}

			if n := len(decode[listWire](t, f.do(t, get("reader", base))).Sinks); n != 0 {
				t.Errorf("sinks after the refusal = %d, want 0", n)
			}

			if n := len(f.sinkEntries(t)); n != 0 {
				t.Errorf("audit entries after the refusal = %d, want 0", n)
			}
		})
	}
}

func TestUnknownNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, r := range []request{
		get("reader", "/api/dustpan/v1/namespaces/nowhere/sinks"),
		send("admin", http.MethodPost, "/api/dustpan/v1/namespaces/nowhere/sinks", webhookBody(t, "a")),
	} {
		got := f.do(t, r)
		if got.code != http.StatusNotFound || !strings.Contains(got.body, "namespace.not-found") {
			t.Errorf("%s: %d %s, want 404 namespace.not-found", r.method, got.code, got.body)
		}
	}

	wantCode(t, "bad namespace name", f.do(t, get("reader", "/api/dustpan/v1/namespaces/bad%20name/sinks")),
		http.StatusBadRequest)
}

func TestReplace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t)
	path := base + "/" + s.ID

	renamed := f.do(t, send("admin", http.MethodPut, path, `{"version":1,"name":"Ops team"}`))
	wantCode(t, "rename", renamed, http.StatusOK)

	after := decode[sinkWire](t, renamed)
	if after.Name != "Ops team" || after.Version != 2 || after.Target != s.Target || after.SigningKey != "" {
		t.Fatalf("renamed = %+v, want version 2 with the new name and no key", after)
	}

	stale := f.do(t, send("admin", http.MethodPut, path, `{"version":1,"name":"Again"}`))
	if stale.code != http.StatusConflict || !strings.Contains(stale.body, "sink.conflict") {
		t.Errorf("stale replace: %d %s, want 409 sink.conflict", stale.code, stale.body)
	}

	moved := f.do(t, send("admin", http.MethodPut, path,
		`{"version":2,"name":"Ops team","target":"https://new.example.com/hook"}`))
	wantCode(t, "new target", moved, http.StatusOK)

	if got := decode[sinkWire](t, f.do(t, get("reader", path))); got.Target != "new.example.com" ||
		got.Version != 3 {
		t.Errorf("read after the change = %+v, want the new target at version 3", got)
	}

	actions := make([]string, 0, 3)
	for _, e := range f.sinkEntries(t) {
		actions = append(actions, e.Action)
	}

	if strings.Join(actions, ",") != "sink:create,sink:update,sink:update" {
		t.Errorf("audit actions = %v, want one entry for each write that succeeded", actions)
	}

	wantCode(t, "unknown sink", f.do(t, send("admin", http.MethodPut, base+"/snk-nothing",
		`{"version":1,"name":"x"}`)), http.StatusNotFound)
}

func TestReplaceRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, body string }{
		{"no version", `{"name":"x"}`},
		{"no name", `{"version":1}`},
		{"a loopback target", `{"version":1,"name":"x","target":"https://127.0.0.1/"}`},
		{"a url for a webhook", `{"version":1,"name":"x","url":"https://hooks.slack.com/x"}`},
		{"an unknown field", `{"version":1,"name":"x","kind":"chat"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			s := f.makeWebhook(t)

			wantCode(
				t,
				"replace",
				f.do(t, send("admin", http.MethodPut, base+"/"+s.ID, tc.body)),
				http.StatusBadRequest,
			)

			if n := len(f.sinkEntries(t)); n != 1 {
				t.Errorf("audit entries = %d, want only the create", n)
			}
		})
	}
}

func TestReplaceChatURLIsANewSecret(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeChat(t, "Chat")
	path := base + "/" + s.ID

	before, err := f.store.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(before) != 1 {
		t.Fatalf("Sinks = %d (%v), want 1", len(before), err)
	}

	body := jsonString(t, map[string]any{
		"version": 1, "name": "Chat", "url": "https://hooks.slack.com/services/T0/B0/NEW-" + canary,
	})

	got := f.do(t, send("admin", http.MethodPut, path, body))
	wantCode(t, "new URL", got, http.StatusOK)

	after, err := f.store.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(after) != 1 || len(after[0].SecretRefs) != 1 ||
		after[0].SecretRefs[0] == before[0].SecretRefs[0] {
		t.Fatalf("Sinks after the change = %+v (%v), want a new secret ref", after, err)
	}

	if strings.Contains(got.body, canary) || strings.Contains(f.logs.String(), canary) {
		t.Error("the answer or a log line holds the new URL")
	}
}

func TestReplaceChatRefusals(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeChat(t, "Chat")
	path := base + "/" + s.ID

	// A target is not a field of a chat sink.
	refused := f.do(t, send("admin", http.MethodPut, path, `{"version":1,"name":"Chat","target":"evil.example.com"}`))
	wantCode(t, "target of a chat sink", refused, http.StatusBadRequest)

	// A URL on a private address leaves the sink as it was.
	private := f.do(t, send("admin", http.MethodPut, path,
		jsonString(t, map[string]any{"version": 1, "name": "Chat", "url": "https://10.0.0.7/" + canary})))
	wantCode(t, "private URL", private, http.StatusBadRequest)

	if strings.Contains(private.body, canary) {
		t.Error("the refusal echoes the URL")
	}

	again, err := f.store.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(again) != 1 || again[0].Version != 1 {
		t.Errorf("sink after the refusals = %+v, %v, want version 1", again, err)
	}
}

func TestDeleteAndRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t)
	path := base + "/" + s.ID

	wantCode(t, "read", f.do(t, get("reader", path)), http.StatusOK)
	wantCode(t, "delete", f.do(t, send("admin", http.MethodDelete, path, "")), http.StatusNoContent)
	wantCode(t, "read after the delete", f.do(t, get("reader", path)), http.StatusNotFound)
	wantCode(t, "delete again", f.do(t, send("admin", http.MethodDelete, path, "")), http.StatusNotFound)

	if n := len(decode[listWire](t, f.do(t, get("reader", base))).Sinks); n != 0 {
		t.Errorf("sinks after the delete = %d, want 0", n)
	}

	entries := f.sinkEntries(t)
	if len(entries) != 2 || entries[1].Action != "sink:delete" || entries[1].ObjectID != s.ID {
		t.Errorf("audit entries = %+v, want sink:create and sink:delete of %s", entries, s.ID)
	}
}

func TestSinksBelongToTheirNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t)
	other := "/api/dustpan/v1/namespaces/other/sinks"

	wantCode(t, "read in another namespace", f.do(t, get("reader", other+"/"+s.ID)), http.StatusNotFound)
	wantCode(t, "delete in another namespace", f.do(t, send("admin", http.MethodDelete, other+"/"+s.ID, "")),
		http.StatusNotFound)
	wantCode(t, "replace in another namespace",
		f.do(t, send("admin", http.MethodPut, other+"/"+s.ID, `{"version":1,"name":"x"}`)), http.StatusNotFound)
	wantCode(t, "test in another namespace", f.do(t, send("admin", http.MethodPost, other+"/"+s.ID+"/test", "")),
		http.StatusNotFound)

	if n := len(decode[listWire](t, f.do(t, get("reader", other))).Sinks); n != 0 {
		t.Errorf("sinks of the other namespace = %d, want 0", n)
	}

	if f.hook.count() != 0 {
		t.Errorf("sender got %d messages, want none", f.hook.count())
	}
}

func TestReaderCannotManage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t)

	for _, r := range []request{
		send("reader", http.MethodPost, base, webhookBody(t, "x")),
		send("reader", http.MethodPut, base+"/"+s.ID, `{"version":1,"name":"x"}`),
		send("reader", http.MethodDelete, base+"/"+s.ID, ""),
		send("reader", http.MethodPost, base+"/"+s.ID+"/test", ""),
		get("manageonly", base),
		get("manageonly", base+"/"+s.ID),
	} {
		wantCode(t, r.method+" "+r.target, f.do(t, r), http.StatusForbidden)
	}

	if f.hook.count() != 0 {
		t.Errorf("sender got %d messages, want none", f.hook.count())
	}
}

func TestAuthentication(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	wantCode(t, "no token", f.do(t, get("", base)), http.StatusUnauthorized)
	wantCode(t, "unknown token", f.do(t, get("stranger", base)), http.StatusUnauthorized)

	req := get("", base).withHeader("Authorization", "Basic dGVzdA==")
	wantCode(t, "basic scheme", f.do(t, req), http.StatusUnauthorized)

	bearer := get("", base).withHeader("Authorization", "Bearer reader")
	wantCode(t, "bearer scheme", f.do(t, bearer), http.StatusOK)
}

func TestTestRouteSendsAndNeverEchoes(t *testing.T) {
	t.Parallel()

	leaky := "dial tcp 203.0.113.9:443: " + canary

	cases := []struct {
		name      string
		err       error
		delivered bool
		class     string
	}{
		{"an accepted message", nil, true, ""},
		{"an address that the rules refuse", fmt.Errorf("%w: %s", egress.ErrRefused, leaky), false, "refused"},
		{"an answer for good", fmt.Errorf("%w: %s", notify.ErrPermanent, leaky), false, "permanent"},
		{"an answer for now", fmt.Errorf("%w: %s", notify.ErrTemporary, leaky), false, "temporary"},
		{"an unknown error", fmt.Errorf("%w: %s", errNetwork, leaky), false, "temporary"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			s := f.makeWebhook(t)
			f.hook.fail(tc.err)

			got := f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
			wantCode(t, "test", got, http.StatusOK)

			res := decode[testWire](t, got)
			if res.Delivered != tc.delivered || res.Class != tc.class || res.Message == "" {
				t.Fatalf("test = %+v, want delivered %t, class %q and a fixed text", res, tc.delivered, tc.class)
			}

			assertNoLeak(t, f, got, s)
			checkTestMessageSent(t, f, s)
		})
	}
}

// testWire is the answer of a test.
type testWire struct {
	Delivered bool   `json:"delivered"`
	Class     string `json:"class"`
	Message   string `json:"message"`
}

// assertNoLeak checks that the answer, the log and the audit entries of a test
// hold neither the canary, nor the address, nor the host, nor the key.
func assertNoLeak(t *testing.T, f fixture, got response, s sinkWire) {
	t.Helper()

	shown := []string{got.body, f.logs.String(), fmt.Sprintf("%+v", f.sinkEntries(t))}

	for _, secret := range []string{canary, "203.0.113.9", "hooks.example.com", s.SigningKey} {
		for _, text := range shown {
			if strings.Contains(text, secret) {
				t.Errorf("%q shows %q", text, secret)
			}
		}
	}
}

// checkTestMessageSent checks the one message that the sender got, and the
// last audit entry.
func checkTestMessageSent(t *testing.T, f fixture, s sinkWire) {
	t.Helper()

	if f.hook.count() != 1 {
		t.Fatalf("sender got %d messages, want 1", f.hook.count())
	}

	m := f.hook.sent[0]
	if m.To != webhookURL || len(m.Secrets) != 1 || string(m.Secrets[0]) != s.SigningKey ||
		m.Notification.Type != notify.SinkTest {
		t.Errorf("message = to %q, %d secrets, type %q, want the URL, the signing key and sink.test",
			m.To, len(m.Secrets), m.Notification.Type)
	}

	entries := f.sinkEntries(t)
	if last := entries[len(entries)-1]; last.Action != "sink:test" || last.ObjectID != s.ID {
		t.Errorf("last audit entry = %+v, want sink:test of %s", last, s.ID)
	}
}

func TestTestRouteWithoutASender(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeChat(t, "Chat")

	got := f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
	if got.code != http.StatusUnprocessableEntity || !strings.Contains(got.body, "sink.no-sender") ||
		strings.Contains(got.body, canary) {
		t.Fatalf("test of a chat sink: %d %s, want 422 sink.no-sender", got.code, got.body)
	}

	if n := len(f.sinkEntries(t)); n != 1 {
		t.Errorf("audit entries = %d, want only the create", n)
	}

	wantCode(t, "unknown sink", f.do(t, send("admin", http.MethodPost, base+"/snk-nothing/test", "")),
		http.StatusNotFound)
}

func TestErrorsAreFixedText(t *testing.T) {
	t.Parallel()

	for _, pr := range []struct {
		name string
		err  error
	}{
		{"invalid", fmt.Errorf("%w: https://hooks.slack.com/%s", notify.ErrInvalid, canary)},
		{"not found", fmt.Errorf("%w: %s", notify.ErrNotFound, canary)},
		{"conflict", fmt.Errorf("%w: %s", notify.ErrConflict, canary)},
		{"unknown", fmt.Errorf("%w: %s", errNetwork, canary)},
		{"fenced", fmt.Errorf("%s: %w", canary, namespace.ErrFenced)},
	} {
		got := apinotifications.ClassifyText(pr.err)
		if strings.Contains(got, canary) {
			t.Errorf("%s: the answer %q holds the text of the error", pr.name, got)
		}
	}
}

func TestRefusedPlatformHostHasItsOwnCode(t *testing.T) {
	t.Parallel()

	// An error with no rule, and one that repeats a secret in its text.
	err := fmt.Errorf("%w: https://hooks.example.com/%s", sinks.ErrPlatformHost, canary)

	if got := apinotifications.ClassifyCode(err); got != "sink.platform-host" {
		t.Errorf("code = %q, want sink.platform-host", got)
	}

	got := apinotifications.ClassifyText(err)
	if strings.Contains(got, canary) || strings.Contains(got, "hooks.example.com") {
		t.Errorf("the answer %q holds the text of the error", got)
	}

	if !strings.Contains(got, "webhook URL") {
		t.Errorf("the answer %q does not say what is wrong", got)
	}
}

func TestPermissionsAreOnTheNamespace(t *testing.T) {
	t.Parallel()

	for _, rt := range newFixture(t).handler.Routes() {
		for _, a := range rt.Actions {
			if a != identity.ActionNamespaceRead && a != identity.ActionNamespaceManageNotifications {
				t.Errorf("route %s %s declares %q, want the namespace read or manage-notifications action",
					rt.Method, rt.Path, a)
			}
		}
	}
}

// errorWire is the error body of the API.
type errorWire struct {
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	ErrorCode string         `json:"errorCode"`
	ErrorArgs map[string]any `json:"errorArgs"`
}

// checkLimited checks an answer 429 of the test route: the code, the
// Retry-After header, errorArgs and that nothing more was sent or written.
func checkLimited(t *testing.T, f fixture, got response, sent, entries int) {
	t.Helper()

	if got.code != http.StatusTooManyRequests {
		t.Fatalf("test: %d %s, want 429", got.code, got.body)
	}

	body := decode[errorWire](t, got)
	if body.ErrorCode != "sink.test-rate-limited" || body.Code != http.StatusTooManyRequests ||
		got.header.Get("Cache-Control") != "no-store" {
		t.Errorf("error = %+v, want sink.test-rate-limited with no-store", body)
	}

	wait, err := strconv.Atoi(got.header.Get("Retry-After"))
	if err != nil || wait < 1 || wait > 60 {
		t.Errorf("Retry-After = %q, want whole seconds from 1 to 60", got.header.Get("Retry-After"))
	}

	if args, ok := body.ErrorArgs["retryAfterSeconds"].(float64); !ok || int(args) != wait {
		t.Errorf("errorArgs = %v, want retryAfterSeconds %d", body.ErrorArgs, wait)
	}

	if f.hook.count() != sent || len(f.sinkEntries(t)) != entries {
		t.Errorf("a limited test sent %d messages and wrote %d entries, want none",
			f.hook.count()-sent, len(f.sinkEntries(t))-entries)
	}
}

func TestTestRouteIsLimitedForEachSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t)
	path := base + "/" + s.ID + "/test"

	wantCode(t, "first test", f.do(t, send("admin", http.MethodPost, path, "")), http.StatusOK)

	sent, entries := f.hook.count(), len(f.sinkEntries(t))

	// A new Idempotency-Key for each request does not get round the limit.
	for i := range 3 {
		got := f.do(t, keyed(send("admin", http.MethodPost, path, ""), fmt.Sprintf("key-%d", i)))
		checkLimited(t, f, got, sent, entries)
	}

	// Another sink of the namespace has its own interval.
	other := f.makeWebhook(t)
	wantCode(t, "test of another sink", f.do(t, send("admin", http.MethodPost, base+"/"+other.ID+"/test", "")),
		http.StatusOK)
}

func TestTestRouteIsLimitedForEachNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for i := range 10 {
		s := f.makeWebhook(t)
		wantCode(t, fmt.Sprintf("test %d", i), f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", "")),
			http.StatusOK)
	}

	s := f.makeWebhook(t)
	sent, entries := f.hook.count(), len(f.sinkEntries(t))

	checkLimited(t, f, f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", "")), sent, entries)

	// The limits of one namespace leave another namespace alone.
	elsewhere := "/api/dustpan/v1/namespaces/other/sinks"
	otherSink := decode[sinkWire](t, f.do(t, send("admin", http.MethodPost, elsewhere, webhookBody(t, "Ops"))))
	wantCode(t, "test in another namespace",
		f.do(t, send("admin", http.MethodPost, elsewhere+"/"+otherSink.ID+"/test", "")), http.StatusOK)
}

// fencedStore bumps the failover version of every namespace just before the
// first audit append of a test, as a failover does between the read of the
// fence and the append.
type fencedStore struct {
	notify.SinkStore

	bump func(context.Context) error
}

func (s fencedStore) RecordSinkTest(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	if err := s.bump(ctx); err != nil {
		return err
	}

	return s.SinkStore.RecordSinkTest(ctx, id, fence, e)
}

func TestTestRouteSendsNothingToAFencedNamespace(t *testing.T) {
	t.Parallel()

	f := buildFixtureWith(t, keyMiddleware, func(st *sqlite.Store, inner notify.SinkStore) notify.SinkStore {
		return fencedStore{SinkStore: inner, bump: func(ctx context.Context) error {
			_, err := st.BumpFailoverVersions(ctx)

			return err
		}}
	})
	s := f.makeWebhook(t)

	before := len(f.sinkEntries(t))

	got := f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
	if got.code != http.StatusConflict || !strings.Contains(got.body, "namespace.fenced") {
		t.Fatalf("test: %d %s, want 409 namespace.fenced", got.code, got.body)
	}

	if f.hook.count() != 0 {
		t.Fatalf("sender got %d messages, want none", f.hook.count())
	}

	if n := len(f.sinkEntries(t)); n != before {
		t.Fatalf("audit entries = %d, want %d", n, before)
	}
}

// TestTestFailsClosedWithoutAUsableURL proves that a webhook sink whose sealed
// URL is on another host than its target sends nothing and answers 422
// sink.no-url, and that neither the answer nor the log shows the URL.
func TestTestFailsClosedWithoutAUsableURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	got := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create", got, http.StatusCreated)

	s := decode[sinkWire](t, got)
	f.replaceURLItem(t, s.ID, "https://other.example.com/"+canary)

	got = f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
	if got.code != http.StatusUnprocessableEntity || !strings.Contains(got.body, "sink.no-url") ||
		strings.Contains(got.body, canary) || strings.Contains(f.logs.String(), canary) {
		t.Fatalf(
			"test of a sink with a URL on another host: %d %s, want 422 sink.no-url and no URL",
			got.code,
			got.body,
		)
	}

	if n := f.hook.count(); n != 0 {
		t.Errorf("sender got %d messages, want none", n)
	}
}

// replaceURLItem replaces the sealed URL item of the webhook sink id in acme
// with a new item for url, as a store left by another version could hold it.
func (f fixture) replaceURLItem(t *testing.T, id, url string) {
	t.Helper()

	stored0, err := f.store.NotificationSinks().Sinks(t.Context(), "acme")
	if err != nil || len(stored0) != 1 {
		t.Fatalf("Sinks = %d (%v), want 1", len(stored0), err)
	}

	next := stored0[0]
	next.SecretRefs = []string{"itm-replaced", next.SecretRefs[1]}

	sealed, err := notify.SinkSealer{Namespaces: f.secrets, Installation: nil}.Seal(t.Context(), next,
		[]notify.SinkSecret{{Ref: "itm-replaced", Value: []byte(url)}})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	ns, err := f.store.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("GetNamespace: %v", err)
	}

	e := f.sinkEntries(t)[0]
	e.Action, e.ObjectID, e.Hash, e.PrevHash, e.Sequence = "sink:update", id, "", "", 0

	if err = f.store.NotificationSinks().PutSink(t.Context(), next, sealed, ns.Fence(), e); err != nil {
		t.Fatalf("PutSink: %v", err)
	}
}

// TestAnswerHoldsOnlyTheHostOfAWholeURLTarget proves the answer-side layer:
// a row whose target is a whole URL, as a restore or another version could
// leave it, never shows a path, a query, user information or a token, and
// the diagnostic of the answer names the sink without showing the target.
func TestAnswerHoldsOnlyTheHostOfAWholeURLTarget(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, target, want string }{
		{"a path and a query", "https://hooks.example.com/ops/" + canary + "?k=" + canary, ""},
		{"user information", "https://user:" + canary + "@hooks.example.com/x", ""},
		{"a host and a path", "hooks.example.com/" + canary, ""},
		{"a fragment", "hooks.example.com#" + canary, ""},
		{"a bare host", "hooks.example.com:8443", "hooks.example.com:8443"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, kind := range []notify.SinkKind{notify.SinkWebhook, notify.SinkChat} {
				s := notify.Sink{
					ID: "snk-1", Scope: "acme", Kind: kind, Name: "Ops", Platform: "", Target: tc.target,
					SecretRefs: []string{"itm-1", "itm-2"}, CreatedBy: "ana", Version: 1,
				}

				body, logs := documentWithLog(t, &s)

				if strings.Contains(string(body), canary) ||
					!strings.Contains(string(body), `"target":"`+tc.want+`"`) {
					t.Errorf("%s answer = %s, want the target %q and no token", kind, body, tc.want)
				}

				checkTargetLog(t, string(kind), logs, &s, tc.want)
			}
		})
	}
}

// TestURLSetIsFalseForThePlaceholder proves that a webhook sink that has only
// the placeholder item of the migration answers urlSet false, and that a sink
// with a real URL item answers true.
func TestURLSetIsFalseForThePlaceholder(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		refs []string
		want string
	}{
		{"a placeholder first", []string{notify.NoURLRefPrefix + "snk-1", "itm-k1", "itm-k2"}, `"urlSet":false`},
		{"a URL item first", []string{"itm-url", "itm-k1"}, `"urlSet":true`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := notify.Sink{
				ID: "snk-1", Scope: "acme", Kind: notify.SinkWebhook, Name: "Ops", Platform: "",
				Target: "hooks.example.com", SecretRefs: tc.refs, CreatedBy: "ana", Version: 1,
			}

			body, err := apinotifications.DocumentJSON(&s)
			if err != nil || !strings.Contains(string(body), tc.want) {
				t.Errorf("answer = %s (%v), want %s", body, err, tc.want)
			}
		})
	}
}
