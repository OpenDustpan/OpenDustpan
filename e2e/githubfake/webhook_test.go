package githubfake_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/e2e/githubfake"
)

// receiver is a server that keeps the requests it gets and answers status.
type receiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	header []http.Header
	body   [][]byte
}

func newReceiver(t *testing.T, status int) *receiver {
	t.Helper()

	r := new(receiver)
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		r.mu.Lock()
		r.header, r.body = append(r.header, req.Header.Clone()), append(r.body, data)
		r.mu.Unlock()

		w.WriteHeader(status)
	}))

	t.Cleanup(r.srv.Close)

	return r
}

func (r *receiver) got() ([]http.Header, [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]http.Header(nil), r.header...), append([][]byte(nil), r.body...)
}

// wantSignature computes the signature without the code of the fake.
func wantSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const secretValue = "s3cret-s3cret-s3cret-s3cret-s3cret"

func testHook() githubfake.Hook {
	return githubfake.Hook{ID: 55, Secret: []byte(secretValue)}
}

func testSender() githubfake.Sender {
	return githubfake.Sender{ID: 7, Login: "ada"}
}

func testPullRequest(head githubfake.Commit) githubfake.PullRequest {
	return githubfake.PullRequest{Number: 3, Head: head, HeadBranch: "dustpan/j-1", BaseBranch: "main", Merged: false}
}

var uuidForm = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func TestDeliverySignature(t *testing.T) {
	t.Parallel()

	f, head := newFake(t)
	hook, pr := testHook(), testPullRequest(head)

	tests := []struct {
		name  string
		event githubfake.Event
		kind  string
	}{
		{"a ping", f.Ping(hook, testSender()), "ping"},
		{
			"an opened pull request",
			f.PullRequestEvent(githubfake.ReviewOpened, "acme/infra", pr, testSender()),
			"pull_request",
		},
		{
			"a closed pull request",
			f.PullRequestEvent(githubfake.ReviewClosed, "acme/infra", pr, testSender()),
			"pull_request",
		},
		{"an event of a name that no consumer knows", githubfake.RawEvent("push", []byte(`{"ref":"x"}`)), "push"},
		{"a body that does not parse", githubfake.RawEvent("ping", []byte("not json {")), "ping"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := f.NewDelivery(hook, tc.event)

			if got, want := d.Header.Get(githubfake.HeaderSignature), wantSignature(hook.Secret, d.Body); got != want {
				t.Fatalf("signature = %q, want %q", got, want)
			}

			if d.Header.Get(githubfake.HeaderEvent) != tc.kind {
				t.Fatalf("event = %q, want %q", d.Header.Get(githubfake.HeaderEvent), tc.kind)
			}

			if id := d.Header.Get(githubfake.HeaderDelivery); !uuidForm.MatchString(id) || d.ID != id {
				t.Fatalf("delivery ID = %q", id)
			}
		})
	}
}

func TestDeliveryWithNoSecretHasNoSignature(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	d := f.NewDelivery(githubfake.Hook{ID: 1, Secret: nil}, f.Ping(testHook(), testSender()))

	if _, found := d.Header[http.CanonicalHeaderKey(githubfake.HeaderSignature)]; found {
		t.Fatal("a signature is present")
	}
}

type pullRequestPayload struct {
	Action       string          `json:"action"`
	Number       int             `json:"number"`
	Installation installationRef `json:"installation"`
}

type installationRef struct {
	ID int `json:"id"`
}

type headRef struct {
	SHA string `json:"sha"`
}

type pullRequestDetails struct {
	State string  `json:"state"`
	Head  headRef `json:"head"`
}

func TestPullRequestEventPayload(t *testing.T) {
	t.Parallel()

	f, head := newFake(t)
	d := f.NewDelivery(
		testHook(),
		f.PullRequestEvent(githubfake.ReviewOpened, "acme/infra", testPullRequest(head), testSender()),
	)
	out := decode[pullRequestPayload](t, d.Body)
	raw := decode[map[string]json.RawMessage](t, d.Body)
	pr := decode[pullRequestDetails](t, raw["pull_request"])
	repo := decode[map[string]json.RawMessage](t, raw["repository"])

	if out.Action != "opened" || out.Number != 3 || out.Installation.ID != 9001 ||
		pr.State != "open" || pr.Head.SHA != head.ID || string(repo["full_name"]) != `"acme/infra"` {
		t.Fatalf("payload = %s", d.Body)
	}
}

func sendPing(t *testing.T, f *githubfake.Fake, target string) (githubfake.Delivery, githubfake.Sent) {
	t.Helper()

	d := f.NewDelivery(testHook(), f.Ping(testHook(), testSender()))

	sent, err := f.Send(t.Context(), target, d)
	if err != nil {
		t.Fatal(err)
	}

	return d, sent
}

func TestSend(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	rcv := newReceiver(t, http.StatusNoContent)
	target := rcv.srv.URL + "/webhooks/h1"

	d, sent := sendPing(t, f, target)
	if sent.Status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", sent.Status)
	}

	headers, bodies := rcv.got()
	if len(headers) != 1 || !bytes.Equal(bodies[0], d.Body) {
		t.Fatalf("the receiver got headers %v and body %q", headers, bodies)
	}

	for _, name := range []string{githubfake.HeaderSignature, githubfake.HeaderDelivery, githubfake.HeaderEvent} {
		if headers[0].Get(name) != d.Header.Get(name) {
			t.Errorf("header %s = %q, want %q", name, headers[0].Get(name), d.Header.Get(name))
		}
	}

	if got := f.Deliveries(); len(got) != 1 || got[0].URL != target || got[0].Status != http.StatusNoContent {
		t.Fatalf("deliveries = %+v", got)
	}
}

func TestRedeliveryKeepsIDAndBody(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	d := f.NewDelivery(testHook(), f.Ping(testHook(), testSender()))
	again := f.Redeliver(testHook(), d)

	if again.ID != d.ID || !bytes.Equal(again.Body, d.Body) || again.Header.Get(githubfake.HeaderEvent) != "ping" ||
		again.Header.Get(githubfake.HeaderSignature) != d.Header.Get(githubfake.HeaderSignature) {
		t.Fatalf("redelivery = %+v", again)
	}

	if other := f.NewDelivery(testHook(), f.Ping(testHook(), testSender())); other.ID == d.ID {
		t.Fatal("two deliveries have one ID")
	}
}

func TestSendToReceiverThatRefuses(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)

	rcv := newReceiver(t, http.StatusUnauthorized)

	_, sent := sendPing(t, f, rcv.srv.URL)
	if sent.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", sent.Status)
	}
}

func TestSendToReceiverThatIsGone(t *testing.T) {
	t.Parallel()

	f, _ := newFake(t)
	gone := newReceiver(t, http.StatusOK)
	gone.srv.Close()

	d := f.NewDelivery(testHook(), f.Ping(testHook(), testSender()))
	if _, err := f.Send(t.Context(), gone.srv.URL, d); err == nil {
		t.Fatal("no error")
	}
}
