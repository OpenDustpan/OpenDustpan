package notifications_test

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestCreateWebhookAnswersTheKeyOnce(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	got := f.do(t, send("admin", http.MethodPost, base, webhookBody(t, "Ops")))
	wantCode(t, "create", got, http.StatusCreated)

	s := decode[sinkWire](t, got)
	if !strings.HasPrefix(s.ID, notify.SinkIDPrefix) || s.Kind != "webhook" || s.Name != "Ops" || s.Namespace != "acme" ||
		s.Target != "https://hooks.example.com/ops" || s.Version != 1 || s.CreatedBy != "admin" || s.URLSet ||
		!hex64.MatchString(s.SigningKey) {
		t.Fatalf("created = %+v, want a webhook at version 1 with a signing key of 64 hex digits", s)
	}

	if loc := got.header.Get("Location"); loc != base+"/"+s.ID {
		t.Errorf("Location = %q, want %q", loc, base+"/"+s.ID)
	}

	if cc := got.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	// No later answer holds the key.
	for _, target := range []string{base, base + "/" + s.ID} {
		again := f.do(t, get("reader", target))
		wantCode(t, "read", again, http.StatusOK)

		if strings.Contains(again.body, s.SigningKey) || strings.Contains(again.body, "signingKey") {
			t.Errorf("read of %s answers the key: %s", target, again.body)
		}
	}

	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", s.ID)
	if err != nil || len(sealed) != 1 {
		t.Fatalf("stored secrets = %d (%v), want 1", len(sealed), err)
	}

	if strings.Contains(string(sealed[0].Ciphertext()), s.SigningKey) {
		t.Error("the store holds the signing key in plaintext")
	}

	entries := f.sinkEntries(t)
	if len(entries) != 1 || entries[0].Action != "sink:create" || entries[0].ObjectID != s.ID ||
		entries[0].Subject != "admin" || entries[0].Object != "acme" || entries[0].Kind != audit.KindChange {
		t.Fatalf("audit entries = %+v, want one sink:create entry of %s", entries, s.ID)
	}

	if strings.Contains(fmt.Sprintf("%+v", entries[0]), s.SigningKey) || strings.Contains(f.logs.String(), s.SigningKey) {
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
	shown := []string{got.body}

	for _, target := range []string{base, base + "/" + s.ID} {
		shown = append(shown, f.do(t, get("reader", target)).body)
	}

	for _, e := range f.entries(t) {
		shown = append(shown, fmt.Sprintf("%+v", e))
	}

	shown = append(shown, f.logs.String())

	sealed, err := f.store.NotificationSinks().SinkSecrets(t.Context(), "acme", s.ID)
	if err != nil || len(sealed) != 1 {
		t.Fatalf("stored secrets = %d (%v), want 1", len(sealed), err)
	}

	shown = append(shown, string(sealed[0].Ciphertext()))

	for _, text := range shown {
		if strings.Contains(text, canary) || strings.Contains(text, "/services/T0") {
			t.Errorf("the URL of the webhook shows in %q", text)
		}
	}
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
		{"a chat URL with user information", "admin", chat("https://u:p@hooks.slack.com/" + canary), 400, "sink.invalid"},
		{"a webhook over http", "admin", hook("http://hooks.example.com/" + canary), 400, "sink.invalid"},
		{"a webhook on loopback", "admin", hook("https://127.0.0.1:8443/" + canary), 400, "sink.invalid"},
		{"a webhook on IPv6 loopback", "admin", hook("https://[::1]/" + canary), 400, "sink.invalid"},
		{"a webhook on the metadata address", "admin", hook("https://169.254.169.254/" + canary), 400, "sink.invalid"},
		{"no kind", "admin", `{"name":"x"}`, 400, "sink.invalid"},
		{"no name", "admin", `{"kind":"webhook"}`, 400, "sink.invalid"},
		{"an unknown kind", "admin", `{"kind":"pager","name":"x","target":"x"}`, 400, "sink.invalid"},
		{"an unknown field", "admin", `{"kind":"webhook","name":"x","target":"https://a.example.com/","extra":1}`, 400,
			"request.body-invalid"},
		{"two documents", "admin", webhookBody(t, "a") + webhookBody(t, "b"), 400, "request.body-invalid"},
		{"a body that is not JSON", "admin", "kind=webhook", 400, "request.body-invalid"},
		{"a body over the limit", "admin", `{"kind":"webhook","name":"` + strings.Repeat("n", 70<<10) + `"}`, 400,
			"request.body-invalid"},
		{"a reader", "reader", webhookBody(t, "a"), 403, "auth.forbidden"},
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
	s := f.makeWebhook(t, "Ops")
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

	if got := decode[sinkWire](t, f.do(t, get("reader", path))); got.Target != "https://new.example.com/hook" ||
		got.Version != 3 {
		t.Errorf("read after the change = %+v, want the new target at version 3", got)
	}

	refused := []struct{ name, body string }{
		{"no version", `{"name":"x"}`},
		{"no name", `{"version":3}`},
		{"a loopback target", `{"version":3,"name":"x","target":"https://127.0.0.1/"}`},
		{"a url for a webhook", `{"version":3,"name":"x","url":"https://hooks.slack.com/x"}`},
		{"an unknown field", `{"version":3,"name":"x","kind":"chat"}`},
	}

	for _, tc := range refused {
		got := f.do(t, send("admin", http.MethodPut, path, tc.body))
		if got.code != http.StatusBadRequest {
			t.Errorf("replace with %s: %d %s, want 400", tc.name, got.code, got.body)
		}
	}

	entries := f.sinkEntries(t)

	actions := make([]string, 0, len(entries))
	for _, e := range entries {
		actions = append(actions, e.Action)
	}

	if strings.Join(actions, ",") != "sink:create,sink:update,sink:update" {
		t.Errorf("audit actions = %v, want one entry for each write that succeeded", actions)
	}

	wantCode(t, "unknown sink", f.do(t, send("admin", http.MethodPut, base+"/snk-nothing",
		`{"version":1,"name":"x"}`)), http.StatusNotFound)
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
	if err != nil || len(after) != 1 || len(after[0].SecretRefs) != 1 || after[0].SecretRefs[0] == before[0].SecretRefs[0] {
		t.Fatalf("Sinks after the change = %+v (%v), want a new secret ref", after, err)
	}

	if strings.Contains(got.body, canary) || strings.Contains(f.logs.String(), canary) {
		t.Error("the answer or a log line holds the new URL")
	}

	// A target is not a field of a chat sink.
	refused := f.do(t, send("admin", http.MethodPut, path, `{"version":2,"name":"Chat","target":"evil.example.com"}`))
	wantCode(t, "target of a chat sink", refused, http.StatusBadRequest)

	// A URL on a private address leaves the sink as it was.
	private := f.do(t, send("admin", http.MethodPut, path,
		jsonString(t, map[string]any{"version": 2, "name": "Chat", "url": "https://10.0.0.7/" + canary})))
	wantCode(t, "private URL", private, http.StatusBadRequest)

	if strings.Contains(private.body, canary) {
		t.Error("the refusal echoes the URL")
	}

	if again, _ := f.store.NotificationSinks().Sinks(t.Context(), "acme"); len(again) != 1 || again[0].Version != 2 {
		t.Errorf("sink after the refusals = %+v, want version 2", again)
	}
}

func TestDeleteAndRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	s := f.makeWebhook(t, "Ops")
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
	s := f.makeWebhook(t, "Ops")
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
	s := f.makeWebhook(t, "Ops")

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
		{"an unknown error", errors.New(leaky), false, "temporary"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			s := f.makeWebhook(t, "Ops")
			f.hook.fail(tc.err)

			got := f.do(t, send("admin", http.MethodPost, base+"/"+s.ID+"/test", ""))
			wantCode(t, "test", got, http.StatusOK)

			res := decode[struct {
				Delivered bool   `json:"delivered"`
				Class     string `json:"class"`
				Message   string `json:"message"`
			}](t, got)
			if res.Delivered != tc.delivered || res.Class != tc.class || res.Message == "" {
				t.Fatalf("test = %+v, want delivered %t, class %q and a fixed text", res, tc.delivered, tc.class)
			}

			for _, secret := range []string{canary, "203.0.113.9", "hooks.example.com", s.SigningKey} {
				for _, shown := range []string{got.body, f.logs.String(), fmt.Sprintf("%+v", f.sinkEntries(t))} {
					if strings.Contains(shown, secret) {
						t.Errorf("%q shows %q", shown, secret)
					}
				}
			}

			if f.hook.count() != 1 {
				t.Fatalf("sender got %d messages, want 1", f.hook.count())
			}

			m := f.hook.sent[0]
			if m.To != "https://hooks.example.com/ops" || len(m.Secrets) != 1 || string(m.Secrets[0]) != s.SigningKey ||
				m.Notification.Type != notify.SinkTest {
				t.Errorf("message = to %q, %d secrets, type %q, want the target, the signing key and sink.test",
					m.To, len(m.Secrets), m.Notification.Type)
			}

			entries := f.sinkEntries(t)
			if last := entries[len(entries)-1]; last.Action != "sink:test" || last.ObjectID != s.ID {
				t.Errorf("last audit entry = %+v, want sink:test of %s", last, s.ID)
			}
		})
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
		{"unknown", errors.New(canary)},
		{"fenced", fmt.Errorf("%s: %w", canary, namespace.ErrFenced)},
	} {
		got := apinotifications.ClassifyText(pr.err)
		if strings.Contains(got, canary) {
			t.Errorf("%s: the answer %q holds the text of the error", pr.name, got)
		}
	}
}

func TestPermissionsAreOnTheNamespace(t *testing.T) {
	t.Parallel()

	for _, route := range newFixture(t).handler.Routes() {
		for _, a := range route.Actions {
			if a != identity.ActionNamespaceRead && a != identity.ActionNamespaceManageNotifications {
				t.Errorf("route %s %s declares %q, want the namespace read or manage-notifications action",
					route.Method, route.Path, a)
			}
		}
	}
}
