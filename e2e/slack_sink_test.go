//go:build e2e

package e2e_test

import (
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/e2e"
	"opendustpan.org/opendustpan/pkg/notify/chat"
	"opendustpan.org/opendustpan/pkg/notify/chat/slack"
)

// slackMarker stands for the token in the incoming webhook URL of Slack. No
// answer and no audit entry may hold it.
const slackMarker = "e2e-slack-token-8821"

// slackReceiver stands in for Slack. It records each request and answers as
// the step says.
type slackReceiver struct {
	mu     sync.Mutex
	paths  []string
	bodies []string
	status int
	answer string
}

func (r *slackReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	b, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "read", http.StatusInternalServerError)

		return
	}

	r.mu.Lock()
	r.paths = append(r.paths, req.Method+" "+req.URL.Path)
	r.bodies = append(r.bodies, string(b))
	status, answer := r.status, r.answer
	r.mu.Unlock()

	if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
		w.Header().Set("Location", "https://other.example.com/capture")
	}

	w.WriteHeader(status)
	_, _ = io.WriteString(w, answer) //nolint:errcheck // A test receiver.
}

// TestSlackSink proves the path of a platform engineer who adds a Slack
// incoming webhook to a namespace and gets a readable message: the sink is
// saved, the test route sends the message to a receiver on the machine of the
// test through the real sender and renderer, and the answers and the audit log
// never hold the URL. The built-in sender refuses a receiver on the machine of
// the server by design, so the in-process server gets a sender with a seam
// that only this build has (chat.NewLoopback, build tag e2e). The server
// binary has no such seam, so the test needs the in-process server.
func TestSlackSink(t *testing.T) {
	t.Parallel()

	if !e2e.SinkSendersApply() {
		t.Skip("the seam of the receiver is in the in-process server only")
	}

	stores, err := e2e.Databases()
	if err != nil {
		t.Fatalf("stores: %v", err)
	}

	for _, store := range stores {
		t.Run(string(store), func(t *testing.T) {
			t.Parallel()

			recv := &slackReceiver{mu: sync.Mutex{}, paths: nil, bodies: nil, status: http.StatusOK, answer: "ok"}
			srv := httptest.NewTLSServer(recv)
			t.Cleanup(srv.Close)

			roots := x509.NewCertPool()
			roots.AddCert(srv.Certificate())

			// The certificate of the test server is valid for example.com.
			sender := chat.NewLoopback(srv.Listener.Addr().String(), roots, "example.com", []chat.Renderer{slack.New()})
			t.Cleanup(sender.Close)

			env := e2e.NewEnvWithSinkSenders(t.Context(), t, e2e.NoCLI(), store, sender)

			slackScenario(t, env, recv)
		})
	}
}

// slackTestResult decodes the answer of the test route.
type slackTestResult struct {
	Delivered bool   `json:"delivered"`
	Class     string `json:"class"`
	Message   string `json:"message"`
}

func slackScenario(t *testing.T, env *e2e.Env, recv *slackReceiver) {
	t.Helper()

	url := "https://hooks.slack.com/services/T000/B000/" + slackMarker
	body := `{"kind":"chat","platform":"slack","name":"Ops channel","url":"` + url + `"}`

	var slackSink sinkDoc

	sinkStep(
		t,
		env,
		sinkCase("a Slack URL on another host is refused", http.MethodPost, sinksBase, http.StatusBadRequest).
			withBody(`{"kind":"chat","platform":"slack","name":"Wrong","url":"https://hooks.example.com/services/`+slackMarker+`"}`).
			checking(sinkNeverContains(slackMarker)),
	)
	sinkStep(t, env, sinkCase("a Slack URL with a port is refused", http.MethodPost, sinksBase, http.StatusBadRequest).
		withBody(`{"kind":"chat","platform":"slack","name":"Port","url":"https://hooks.slack.com:8443/services/`+slackMarker+`"}`).
		checking(sinkNeverContains(slackMarker)))
	sinkStep(
		t,
		env,
		sinkCase("a URL on the machine of the server is refused", http.MethodPost, sinksBase, http.StatusBadRequest).
			withBody(`{"kind":"chat","platform":"slack","name":"Local","url":"https://127.0.0.1/services/`+slackMarker+`"}`).
			checking(sinkNeverContains(slackMarker, "127.0.0.1")),
	)
	sinkStep(t, env, sinkCase("make a Slack sink", http.MethodPost, sinksBase, http.StatusCreated).withBody(body).
		checking(func(answer string) error {
			if err := rememberSink(answer, &slackSink, nil); err != nil {
				return err
			}

			return sinkNeverContains(slackMarker, "/services/")(answer)
		}))

	if slackSink.ID == "" || !slackSink.URLSet || slackSink.Target != "hooks.slack.com" {
		t.Fatalf("sink = %+v, want a Slack sink with the host as target", slackSink)
	}

	testPath := sinksBase + "/" + slackSink.ID + "/test"

	slackTestStep(t, env, testPath, slackExpect{"the test message reaches Slack", true, ""})

	taken := recv.taken()
	if len(taken.paths) != 1 || taken.paths[0] != "POST /services/T000/B000/"+slackMarker {
		t.Fatalf("receiver got %v, want one POST to the path of the webhook", taken.paths)
	}

	checkSlackMessage(t, taken.bodies[0], slackSink.ID)

	// The test route takes one test of a sink in 10 seconds, so each answer of
	// the receiver gets a sink of its own.
	for i, c := range []struct {
		name      string
		status    int
		answer    string
		delivered bool
		class     string
	}{
		{"Slack refuses the URL", http.StatusNotFound, "invalid_token", false, "permanent"},
		{"Slack fails for now", http.StatusInternalServerError, "", false, "temporary"},
		{"Slack answers 200 with a refusal", http.StatusOK, "no_text", false, "permanent"},
		{"a redirect is not followed", http.StatusFound, "", false, "refused"},
		{"GovSlack has its own host", http.StatusOK, "ok", true, ""},
	} {
		host := "hooks.slack.com"
		if c.name == "GovSlack has its own host" {
			host = "hooks.slack-gov.com"
		}

		var other sinkDoc

		sinkStep(t, env, sinkCase("make a sink for: "+c.name, http.MethodPost, sinksBase, http.StatusCreated).
			withBody(fmt.Sprintf(`{"kind":"chat","platform":"slack","name":"Case %d","url":"https://%s/services/T000/B000/%s"}`,
				i, host, slackMarker)).
			checking(func(answer string) error { return rememberSink(answer, &other, nil) }))

		recv.set(c.status, c.answer)
		slackTestStep(t, env, sinksBase+"/"+other.ID+"/test", slackExpect{c.name, c.delivered, c.class})
	}

	sinkStep(
		t,
		env,
		sinkCase("the sink reads without the URL", http.MethodGet, sinksBase+"/"+slackSink.ID, http.StatusOK).
			checking(sinkNeverContains(slackMarker, "/services/")),
	)
	slackAuditStep(t, env)
	sinkStep(t, env, sinkCase("delete the Slack sink", http.MethodDelete, sinksBase+"/"+slackSink.ID,
		http.StatusNoContent))
}

func (r *slackReceiver) set(status int, answer string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.status, r.answer = status, answer
}

func (r *slackReceiver) taken() slackTaken {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slackTaken{paths: append([]string(nil), r.paths...), bodies: append([]string(nil), r.bodies...)}
}

// slackTaken is what the receiver took so far.
type slackTaken struct {
	paths  []string
	bodies []string
}

// slackExpect is what a test of a Slack sink must answer.
type slackExpect struct {
	name      string
	delivered bool
	class     string
}

// slackTestStep runs the test route and checks the result and that the answer
// holds no URL and no text of the receiver.
func slackTestStep(t *testing.T, env *e2e.Env, path string, want slackExpect) {
	t.Helper()

	sinkStep(t, env, sinkCase(want.name, http.MethodPost, path, http.StatusOK).checking(func(answer string) error {
		var res slackTestResult
		if err := json.Unmarshal([]byte(answer), &res); err != nil {
			return fmt.Errorf("decode test result: %w", err)
		}

		if res.Delivered != want.delivered || res.Class != want.class || res.Message == "" {
			return fmt.Errorf(
				"%w: test result %s, want delivered %v and class %q",
				errSinkAnswer,
				answer,
				want.delivered,
				want.class,
			)
		}

		return sinkNeverContains(slackMarker, "/services/", "invalid_token", "no_text", "hooks.slack.com")(answer)
	}))
}

// checkSlackMessage checks that the body that Slack got is a readable test
// message: JSON with the fallback text, the product name, the title and the
// summary, the fields, and no markup that can start a mention.
func checkSlackMessage(t *testing.T, body, sinkID string) {
	t.Helper()

	var msg struct {
		Text   string `json:"text"`
		Blocks []any  `json:"blocks"`
	}

	if err := json.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("the receiver got a body that is not JSON: %v", err)
	}

	if len(msg.Blocks) == 0 || !strings.Contains(msg.Text, "Info: Test message") {
		t.Errorf("message = %q with %d blocks, want a fallback text and blocks", msg.Text, len(msg.Blocks))
	}

	for _, want := range []string{
		"OpenDustpan", "Test message", "This is a test message. No event happened.", sinkID, e2e.Namespace,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the message does not hold %q: %s", want, body)
		}
	}

	if strings.ContainsAny(body, "<>") {
		t.Errorf("the message holds < or >: %s", body)
	}

	if strings.Contains(body, slackMarker) {
		t.Error("the message holds the token of the URL")
	}
}

// slackAuditStep proves that the audit log holds the test entries and no
// secret.
func slackAuditStep(t *testing.T, env *e2e.Env) {
	t.Helper()

	status, body, err := env.APIStatus(
		t.Context(),
		http.MethodGet,
		"/api/audit/entries?organization="+e2e.Namespace,
		"",
	)
	if err != nil || status != http.StatusOK {
		t.Fatalf("list audit entries = %d, %v (%s)", status, err, body)
	}

	if !strings.Contains(body, `"action":"sink:test"`) || !strings.Contains(body, "class: delivered") {
		t.Error("audit log holds no entry of the test")
	}

	for _, secret := range []string{slackMarker, "/services/", "hooks.slack.com", "invalid_token"} {
		if strings.Contains(body, secret) {
			t.Errorf("audit log holds %q", secret)
		}
	}
}
