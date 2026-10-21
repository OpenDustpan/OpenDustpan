//go:build e2e

package e2e_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// sinksAgent is the User-Agent of the sink API requests.
const sinksAgent = "opendustpan-e2e-sinks"

// sinksBase is the sink route of the scenario namespace.
const sinksBase = "/api/dustpan/v1/namespaces/" + e2e.Namespace + "/sinks"

// sinkChatMarker stands for the token in the URL of a chat webhook. No answer
// and no audit entry may hold it.
const sinkChatMarker = "e2e-chat-token-4711"

var errSinkAnswer = errors.New("the sink answer is not the expected one")

var sinkKeyPattern = regexp.MustCompile(`"signingKey":"([0-9a-f]{64})"`)

// sinkDoc is the part of a sink answer that the steps read.
type sinkDoc struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	URLSet  bool   `json:"urlSet"`
	Version int64  `json:"version"`
}

// TestNotificationSinksAPI drives the sink routes with the real server, on each
// store. No CLI calls these routes. The steps make a signed webhook and a chat
// webhook, send the test message, change and delete them, and prove that the
// URL of a chat webhook and the signing key of a webhook show in no later
// answer or audit entry, and that the outbound address rules refuse a URL on
// the machine of the server. A receiver on the machine of the server is out
// of reach by design, so the test message of the steps goes to a host name
// that does not resolve and the result is the class temporary.
func TestNotificationSinksAPI(t *testing.T) {
	t.Parallel()

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			env := e2e.NewEnv(t.Context(), t, e2e.NoCLI(), store)

			sinksScenario(t, env)
		})
	}
}

// sinkCall is one request of a step. The builder methods set the optional
// fields.
type sinkCall struct {
	name   string
	method string
	path   string
	body   string
	key    string
	want   int
	check  func(string) error
}

// sinkRequest returns a request with no body, no key and no check.
func sinkCase(name, method, path string, want int) sinkCall {
	return sinkCall{name: name, method: method, path: path, body: "", key: "", want: want, check: nil}
}

// withBody returns c with a body.
func (c sinkCall) withBody(body string) sinkCall {
	c.body = body

	return c
}

// withKey returns c with the Idempotency-Key header.
func (c sinkCall) withKey(key string) sinkCall {
	c.key = key

	return c
}

// checking returns c with a check of the answer.
func (c sinkCall) checking(check func(string) error) sinkCall {
	c.check = check

	return c
}

// sinkStep runs one sink request as principal A.
func sinkStep(t *testing.T, env *e2e.Env, c sinkCall) {
	t.Helper()

	headers := map[string]string{}
	if c.key != "" {
		headers["Idempotency-Key"] = c.key
	}

	env.APIStepWithHeaders(t.Context(), t, e2e.APIStepSpec{
		UserAgent: sinksAgent, Name: c.name, Method: c.method, Path: c.path, Body: c.body, Want: c.want, Check: c.check,
		Scheme: "token",
	}, headers)
}

func sinksScenario(t *testing.T, env *e2e.Env) {
	t.Helper()

	var hook, chat sinkDoc

	var key string

	hookBody := `{"kind":"webhook","name":"Operations","target":"https://hooks.example.invalid/dustpan"}`
	chatBody := `{"kind":"chat","platform":"slack","name":"Ops channel",` +
		`"url":"https://hooks.slack.com/services/T000/B000/` + sinkChatMarker + `"}`

	sinkStep(t, env, sinkCase("a webhook on the machine of the server is refused", http.MethodPost, sinksBase,
		http.StatusBadRequest).
		withBody(`{"kind":"webhook","name":"Local","target":"https://127.0.0.1:9443/hook-`+sinkChatMarker+`"}`).
		checking(sinkNeverContains(sinkChatMarker, "127.0.0.1")))
	sinkStep(t, env, sinkCase("a webhook on the metadata address is refused", http.MethodPost, sinksBase,
		http.StatusBadRequest).withBody(`{"kind":"webhook","name":"Meta","target":"https://169.254.169.254/latest"}`))
	sinkStep(t, env, sinkCase("make a signed webhook", http.MethodPost, sinksBase, http.StatusCreated).
		withBody(hookBody).checking(func(body string) error { return rememberSink(body, &hook, &key) }))
	sinkStep(t, env, sinkCase("make a chat webhook", http.MethodPost, sinksBase, http.StatusCreated).
		withBody(chatBody).checking(func(body string) error { return rememberSink(body, &chat, nil) }))

	if hook.ID == "" || chat.ID == "" || !chat.URLSet || chat.Target != "hooks.slack.com" {
		t.Fatalf("sinks = %+v, %+v, want a webhook and a chat sink with the host as target", hook, chat)
	}

	sinkReadSteps(t, env, hook, chat, key)
	sinkTestSteps(t, env, hook, key)
	sinkKeyedSteps(t, env)
	sinkAuditStep(t, env, key)
	sinkMemberStep(t, env, hook)

	sinkStep(t, env, sinkCase("delete the webhook", http.MethodDelete, sinksBase+"/"+hook.ID, http.StatusNoContent))
	sinkStep(t, env, sinkCase("the deleted webhook is gone", http.MethodGet, sinksBase+"/"+hook.ID,
		http.StatusNotFound))
	sinkStep(t, env, sinkCase("delete the chat sink", http.MethodDelete, sinksBase+"/"+chat.ID,
		http.StatusNoContent))
}

// rememberSink decodes a sink answer into into, and the signing key into
// keyInto when it is set.
func rememberSink(body string, into *sinkDoc, keyInto *string) error {
	if err := json.Unmarshal([]byte(body), into); err != nil {
		return fmt.Errorf("decode sink: %w", err)
	}

	if keyInto == nil {
		return nil
	}

	m := sinkKeyPattern.FindStringSubmatch(body)
	if m == nil {
		return fmt.Errorf("%w: no signing key in %s", errSinkAnswer, body)
	}

	*keyInto = m[1]

	return nil
}

// sinkReadSteps lists, reads and changes the sinks.
func sinkReadSteps(t *testing.T, env *e2e.Env, hook, chat sinkDoc, key string) {
	t.Helper()

	sinkStep(t, env, sinkCase("the list holds both and no secret", http.MethodGet, sinksBase, http.StatusOK).
		checking(func(body string) error {
			if !strings.Contains(body, hook.ID) || !strings.Contains(body, chat.ID) {
				return fmt.Errorf("%w: both sinks are not in %s", errSinkAnswer, body)
			}

			return sinkNeverContains(sinkChatMarker, key, "signingKey")(body)
		}))
	sinkStep(t, env, sinkCase("read the chat sink", http.MethodGet, sinksBase+"/"+chat.ID, http.StatusOK).
		checking(sinkNeverContains(sinkChatMarker, "/services/")))
	sinkStep(t, env, sinkCase("rename the webhook", http.MethodPut, sinksBase+"/"+hook.ID, http.StatusOK).
		withBody(`{"version":1,"name":"Operations team"}`))
	sinkStep(t, env, sinkCase("a stale version is refused", http.MethodPut, sinksBase+"/"+hook.ID,
		http.StatusConflict).withBody(`{"version":1,"name":"Again"}`))
}

// sinkHasError checks that the answer is an error with this errorCode and this
// message.
func sinkHasError(answer, code, message string) error {
	var doc struct {
		Message   string `json:"message"`
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal([]byte(answer), &doc); err != nil {
		return fmt.Errorf("%w: read the error: %w", errSinkAnswer, err)
	}

	if doc.ErrorCode != code || doc.Message != message {
		return fmt.Errorf("%w: errorCode %q, message %q; want %q, %q", errSinkAnswer, doc.ErrorCode, doc.Message,
			code, message)
	}

	return nil
}

// sinkNeverContains returns a check that the answer holds none of the texts.
func sinkNeverContains(texts ...string) func(string) error {
	return func(body string) error {
		for _, text := range texts {
			if strings.Contains(body, text) {
				return fmt.Errorf("%w: the answer holds %q: %s", errSinkAnswer, text, body)
			}
		}

		return nil
	}
}

// sinkTestSteps sends the test message of both sinks.
func sinkTestSteps(t *testing.T, env *e2e.Env, hook sinkDoc, key string) {
	t.Helper()

	sinkStep(t, env, sinkCase("send the test message of the webhook", http.MethodPost,
		sinksBase+"/"+hook.ID+"/test", http.StatusOK).checking(func(body string) error {
		var res struct {
			Delivered bool   `json:"delivered"`
			Class     string `json:"class"`
			Message   string `json:"message"`
		}

		if err := json.Unmarshal([]byte(body), &res); err != nil {
			return fmt.Errorf("decode test result: %w", err)
		}

		// The host name does not resolve, so the message does not arrive.
		if res.Delivered || res.Class != "temporary" || res.Message == "" {
			return fmt.Errorf("%w: test result %s, want the class temporary", errSinkAnswer, body)
		}

		return sinkNeverContains("example.invalid", key, "hooks")(body)
	}))
	// A Slack sink has a sender, and its test goes to the real host of Slack,
	// so the steps here use a Teams sink, which has no adapter yet. The test of
	// a Slack sink is in TestSlackSink, with a receiver on the machine of the
	// test.
	var teams sinkDoc

	sinkStep(t, env, sinkCase("make a Teams sink", http.MethodPost, sinksBase, http.StatusCreated).
		withBody(`{"kind":"chat","platform":"teams","name":"Teams","url":"https://teams.example.invalid/`+sinkChatMarker+`"}`).
		checking(func(body string) error { return rememberSink(body, &teams, nil) }))
	sinkStep(t, env, sinkCase("a platform with no adapter has no sender yet", http.MethodPost,
		sinksBase+"/"+teams.ID+"/test", http.StatusUnprocessableEntity).checking(func(body string) error {
		if !strings.Contains(body, "sink.no-sender") {
			return fmt.Errorf("%w: want sink.no-sender in %s", errSinkAnswer, body)
		}

		return sinkNeverContains(sinkChatMarker)(body)
	}))
	sinkStep(t, env, sinkCase("delete the Teams sink", http.MethodDelete, sinksBase+"/"+teams.ID,
		http.StatusNoContent))
	sinkStep(t, env, sinkCase("the test of an unknown sink", http.MethodPost, sinksBase+"/snk-doesnotexist/test",
		http.StatusNotFound))
}

// sinkKeyedSteps proves that a key makes one sink, and that a replay of a
// create does not answer the signing key again.
func sinkKeyedSteps(t *testing.T, env *e2e.Env) {
	t.Helper()

	body := `{"kind":"webhook","name":"Keyed","target":"https://keyed.example.invalid/hook"}`

	var made sinkDoc

	sinkStep(t, env, sinkCase("make a webhook with an idempotency key", http.MethodPost, sinksBase,
		http.StatusCreated).withBody(body).withKey("e2e-sink-key").
		checking(func(answer string) error { return json.Unmarshal([]byte(answer), &made) }))
	sinkStep(t, env, sinkCase("the same create with the same key names the sink and not the key", http.MethodPost,
		sinksBase, http.StatusConflict).withBody(body).withKey("e2e-sink-key").checking(func(answer string) error {
		if !strings.Contains(answer, "request.idempotency-key-replayed-secret") ||
			!strings.Contains(answer, made.ID) || sinkKeyPattern.MatchString(answer) {
			return fmt.Errorf("%w: replay %s", errSinkAnswer, answer)
		}

		return nil
	}))
	sinkStep(t, env, sinkCase("the same key with another body is refused", http.MethodPost, sinksBase,
		http.StatusUnprocessableEntity).
		withBody(`{"kind":"webhook","name":"Other","target":"https://other.example.invalid/hook"}`).
		withKey("e2e-sink-key"))
	sinkStep(t, env, sinkCase("delete the keyed webhook", http.MethodDelete, sinksBase+"/"+made.ID,
		http.StatusNoContent).withKey("e2e-sink-delete"))
}

// sinkAuditStep reads the audit log of the namespace and proves that it holds
// the sink actions and no secret.
func sinkAuditStep(t *testing.T, env *e2e.Env, key string) {
	t.Helper()

	status, body, err := env.APIStatus(t.Context(), http.MethodGet,
		"/api/audit/entries?organization="+e2e.Namespace, "")
	if err != nil || status != http.StatusOK {
		t.Fatalf("list audit entries = %d, %v (%s)", status, err, body)
	}

	for _, action := range []string{"sink:create", "sink:update", "sink:test"} {
		if !strings.Contains(body, `"action":"`+action+`"`) {
			t.Errorf("audit log holds no entry with the action %s", action)
		}
	}

	for _, secret := range []string{sinkChatMarker, key, "example.invalid", "hooks.slack.com"} {
		if strings.Contains(body, secret) {
			t.Errorf("audit log holds %q", secret)
		}
	}
}

// sinkMemberStep proves that a member without the action cannot make a sink.
func sinkMemberStep(t *testing.T, env *e2e.Env, hook sinkDoc) {
	t.Helper()

	token, ok := env.MemberToken()
	if !ok {
		return
	}

	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, sinksBase, `{"kind":"webhook","name":"x","target":"https://x.example.invalid/"}`},
		{http.MethodPost, sinksBase + "/" + hook.ID + "/test", ""},
		{http.MethodDelete, sinksBase + "/" + hook.ID, ""},
	} {
		status, body, err := env.ServerAPIStatusAs(t.Context(), token, c.method, c.path, c.body)
		if err != nil || status != http.StatusForbidden {
			t.Errorf("member %s %s = %d, %v (%s), want 403", c.method, c.path, status, err, body)
		}
	}
}
