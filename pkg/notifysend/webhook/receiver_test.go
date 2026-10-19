package webhook_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
	"opendustpan.org/opendustpan/pkg/notifysend/webhook"
)

// canary is a secret value that no error text may repeat.
const (
	canary      = "canary-signing-secret-7f3a9c"
	canarySpare = "canary-spare-secret-2b81d4"
	// canaryToken stands for a token in the path of the URL.
	canaryToken = "canary-url-token-91ce05" //nolint:gosec // A fake token used as test data.
)

//nolint:gochecknoglobals // A fixed clock shared by the cases.
var fixedNow = time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

func now() time.Time { return fixedNow }

// request is one delivery that the receiver got.
type request struct {
	method  string
	path    string
	header  http.Header
	body    []byte
	arrived time.Time
}

// receiver is a TLS test server that answers as the case says.
type receiver struct {
	srv     *httptest.Server
	mu      sync.Mutex
	reqs    []request
	handler atomic.Pointer[http.HandlerFunc]
}

// answer replies with status and, when not empty, a Retry-After header.
func answer(status int, retryAfter string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}

		w.WriteHeader(status)
	}
}

func newReceiver(t *testing.T) *receiver {
	t.Helper()

	r := new(receiver)
	r.set(answer(http.StatusNoContent, ""))

	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}

		r.mu.Lock()
		r.reqs = append(r.reqs, request{
			method: req.Method, path: req.URL.Path, header: req.Header.Clone(), body: b, arrived: time.Now(),
		})
		r.mu.Unlock()

		(*r.handler.Load())(w, req)
	}))
	t.Cleanup(r.srv.Close)

	return r
}

func (r *receiver) set(h http.HandlerFunc) { r.handler.Store(&h) }

func (r *receiver) requests() []request {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]request(nil), r.reqs...)
}

// sender returns a Sender that reaches r for any host name.
func (r *receiver) sender(t *testing.T) *webhook.Sender {
	t.Helper()

	s, err := webhook.NewForTest(r.srv, now)
	if err != nil {
		t.Fatalf("NewForTest: %v", err)
	}

	t.Cleanup(s.Close)

	return s
}

// notification is a valid notification with every field set.
func notification() notify.Notification {
	return notify.Notification{
		ID:       "ntf-1",
		Type:     notify.JobFailed,
		Scope:    "shop",
		Object:   "opentofu:shop/network",
		ObjectID: "u-1",
		Subject:  "alice",
		Agent:    "dusty",
		Severity: notify.SeverityWarning,
		Summary:  "The apply of network failed.",
		Links: []notify.Link{
			{Title: "Job", URL: "https://console.example.com/jobs/j-1", Kind: notify.LinkConsole},
			{Title: "Runbook", URL: "https://docs.example.com/runbooks/network", Kind: notify.LinkRunbook},
		},
		Attributes: map[string]string{"job": "j-1", "attempt": "2"},
		Recipients: nil,
		DedupeKey:  "j-1/failed",
		At:         time.Date(2026, 10, 20, 9, 59, 0, 123000000, time.UTC),
		Sequence:   42,
	}
}

// message is a message for the webhook URL of r, with two secrets.
func message(secrets ...[]byte) notify.Message {
	return notify.Message{
		Notification: notification(),
		Sink: notify.Sink{
			ID: "snk-1", Scope: "shop", Kind: notify.SinkWebhook, Name: "Ops",
			Target: "https://hooks.example.com/ops/" + canaryToken, SecretRefs: []string{"itm-1"},
			CreatedBy: "alice", Version: 1,
		},
		Secrets: secrets,
		To:      "https://hooks.example.com/ops/" + canaryToken,
		Actions: nil,
	}
}

func TestSenderConformance(t *testing.T) {
	t.Parallel()

	notifytest.RunSender(t, func(t *testing.T) notifytest.SenderHarness {
		t.Helper()

		r := newReceiver(t)

		return notifytest.SenderHarness{
			Sender:  r.sender(t),
			Message: message([]byte(canary), []byte(canarySpare)),
			Behave: func(b notifytest.Behavior) {
				switch b {
				case notifytest.ReceiverAccepts:
					r.set(answer(http.StatusNoContent, ""))
				case notifytest.ReceiverRefusesForGood:
					r.set(answer(http.StatusNotFound, ""))
				case notifytest.ReceiverFailsForNow:
					r.set(answer(http.StatusBadGateway, ""))
				case notifytest.ReceiverAsksToWait:
					r.set(answer(http.StatusTooManyRequests, strconv.Itoa(int(notifytest.AskedWait.Seconds()))))
				default:
					t.Errorf("unknown behavior %d", b)
				}
			},
			Received: func() int { return len(r.requests()) },
		}
	})
}
