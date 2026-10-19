package webhook_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/webhook"
)

func TestDeliveryVerifies(t *testing.T) {
	t.Parallel()

	a, b := []byte(canary), []byte(canarySpare)

	tests := []struct {
		name    string
		sign    [][]byte // the secrets of the sink
		verify  [][]byte // the secrets of the receiver
		wantSig int      // the signatures in the header
		wantErr error
	}{
		{"one secret", [][]byte{a}, [][]byte{a}, 1, nil},
		{"two secrets, the receiver holds the first", [][]byte{a, b}, [][]byte{a}, 2, nil},
		{"two secrets, the receiver holds the second", [][]byte{a, b}, [][]byte{b}, 2, nil},
		{"two secrets, the receiver holds both", [][]byte{a, b}, [][]byte{a, b}, 2, nil},
		{"one secret, the receiver holds another", [][]byte{a}, [][]byte{b}, 1, notify.ErrSignature},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)

			if err := r.sender(t).Send(t.Context(), message(tt.sign...)); err != nil {
				t.Fatalf("Send: %v", err)
			}

			reqs := r.requests()
			if len(reqs) != 1 {
				t.Fatalf("deliveries = %d, want 1", len(reqs))
			}

			got := reqs[0]
			h := notify.Headers{
				NotificationID: got.header.Get(notify.HeaderNotificationID),
				Timestamp:      got.header.Get(notify.HeaderTimestamp),
				Signature:      got.header.Get(notify.HeaderSignature),
			}

			if n := strings.Count(h.Signature, "v1="); n != tt.wantSig {
				t.Fatalf("signatures in %q = %d, want %d", h.Signature, n, tt.wantSig)
			}

			err := notify.Verify(tt.verify, h, got.body, now())
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Verify = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRequestShape(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)

	if err := r.sender(t).Send(t.Context(), message([]byte(canary))); err != nil {
		t.Fatalf("Send: %v", err)
	}

	got := r.requests()[0]

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"method", got.method, http.MethodPost},
		{"path", got.path, "/ops/" + canaryToken},
		{"content type", got.header.Get("Content-Type"), "application/json"},
		{"notification ID", got.header.Get(notify.HeaderNotificationID), "ntf-1"},
		{"timestamp", got.header.Get(notify.HeaderTimestamp), strconv.FormatInt(fixedNow.Unix(), 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("%s = %q, want %q", tt.name, tt.got, tt.want)
			}
		})
	}
}

func TestEncode(t *testing.T) {
	t.Parallel()

	full := notification()

	empty := notify.Notification{
		ID: "ntf-2", Type: notify.SubscriptionSuspended, Scope: "@server", Object: "", ObjectID: "", Subject: "",
		Agent: "", Severity: notify.SeverityCritical, Summary: "Suspended.", Links: nil, Attributes: nil,
		Recipients: nil, DedupeKey: "ntf-2", At: time.UnixMilli(1_700_000_000_001).UTC(), Sequence: 0,
	}

	withRecipients := notification()
	withRecipients.Recipients = []notify.Recipient{{Principal: "bob", Group: "", Admins: false}}

	text := notification()
	text.Summary = `Quote " and <tag> & é` + "\n"
	text.Links = nil

	tests := []struct {
		name string
		n    notify.Notification
		want string
	}{
		{
			"every field", full,
			`{"schemaVersion":1,"id":"ntf-1","type":"job.failed","scope":"shop","object":"opentofu:shop/network",` +
				`"objectId":"u-1","subject":"alice","agent":"dusty","severity":"warning",` +
				`"summary":"The apply of network failed.",` +
				`"links":[{"title":"Job","kind":"console","url":"https://console.example.com/jobs/j-1"},` +
				`{"title":"Runbook","kind":"runbook","url":"https://docs.example.com/runbooks/network"}],` +
				`"attributes":{"attempt":"2","job":"j-1"},"dedupeKey":"j-1/failed","at":1792490340123,"sequence":42}`,
		},
		{
			"empty lists are an array and an object", empty,
			`{"schemaVersion":1,"id":"ntf-2","type":"subscription.suspended","scope":"@server","object":"",` +
				`"objectId":"","subject":"","agent":"","severity":"critical","summary":"Suspended.","links":[],` +
				`"attributes":{},"dedupeKey":"ntf-2","at":1700000000001,"sequence":0}`,
		},
		{
			"recipients are not in the body", withRecipients,
			`{"schemaVersion":1,"id":"ntf-1","type":"job.failed","scope":"shop","object":"opentofu:shop/network",` +
				`"objectId":"u-1","subject":"alice","agent":"dusty","severity":"warning",` +
				`"summary":"The apply of network failed.",` +
				`"links":[{"title":"Job","kind":"console","url":"https://console.example.com/jobs/j-1"},` +
				`{"title":"Runbook","kind":"runbook","url":"https://docs.example.com/runbooks/network"}],` +
				`"attributes":{"attempt":"2","job":"j-1"},"dedupeKey":"j-1/failed","at":1792490340123,"sequence":42}`,
		},
		{
			"text is escaped as JSON, not as HTML", text,
			`{"schemaVersion":1,"id":"ntf-1","type":"job.failed","scope":"shop","object":"opentofu:shop/network",` +
				`"objectId":"u-1","subject":"alice","agent":"dusty","severity":"warning",` +
				`"summary":"Quote \" and \u003ctag\u003e \u0026 é\n","links":[],` +
				`"attributes":{"attempt":"2","job":"j-1"},"dedupeKey":"j-1/failed","at":1792490340123,"sequence":42}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := webhook.Encode(tt.n)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}

			if string(got) != tt.want {
				t.Fatalf("Encode =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestBodyIsTheEncodedNotification(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)

	if err := r.sender(t).Send(t.Context(), message([]byte(canary))); err != nil {
		t.Fatalf("Send: %v", err)
	}

	want, err := webhook.Encode(notification())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if got := r.requests()[0].body; !bytes.Equal(got, want) {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

// statusCase is one answer of the receiver and the error that Send returns.
type statusCase struct {
	name       string
	status     int
	retryAfter string
	wantNil    bool
	want       error
	wantRA     bool
	wantWait   time.Duration
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()

	httpDate := fixedNow.Add(2 * time.Minute).UTC().Format(http.TimeFormat)
	pastDate := fixedNow.Add(-time.Hour).UTC().Format(http.TimeFormat)

	tests := []statusCase{
		{"200", http.StatusOK, "", true, nil, false, 0},
		{"202", http.StatusAccepted, "", true, nil, false, 0},
		{"204", http.StatusNoContent, "", true, nil, false, 0},
		{"2xx ignores Retry-After", http.StatusOK, "30", true, nil, false, 0},
		{"304 without a location", http.StatusNotModified, "", false, notify.ErrPermanent, false, 0},
		{"400", http.StatusBadRequest, "", false, notify.ErrPermanent, false, 0},
		{"401", http.StatusUnauthorized, "", false, notify.ErrPermanent, false, 0},
		{"403", http.StatusForbidden, "", false, notify.ErrPermanent, false, 0},
		{"404", http.StatusNotFound, "", false, notify.ErrPermanent, false, 0},
		{"410", http.StatusGone, "", false, notify.ErrPermanent, false, 0},
		{"413", http.StatusRequestEntityTooLarge, "", false, notify.ErrPermanent, false, 0},
		{"4xx ignores Retry-After", http.StatusForbidden, "30", false, notify.ErrPermanent, false, 0},
		{"408", http.StatusRequestTimeout, "", false, notify.ErrTemporary, false, 0},
		{"429", http.StatusTooManyRequests, "", false, notify.ErrTemporary, false, 0},
		{"500", http.StatusInternalServerError, "", false, notify.ErrTemporary, false, 0},
		{"502", http.StatusBadGateway, "", false, notify.ErrTemporary, false, 0},
		{"503", http.StatusServiceUnavailable, "", false, notify.ErrTemporary, false, 0},
		{"429 with seconds", http.StatusTooManyRequests, "120", false, notify.ErrTemporary, true, 2 * time.Minute},
		{"503 with seconds", http.StatusServiceUnavailable, "7", false, notify.ErrTemporary, true, 7 * time.Second},
		{"408 with seconds", http.StatusRequestTimeout, "5", false, notify.ErrTemporary, true, 5 * time.Second},
		{"503 with a date", http.StatusServiceUnavailable, httpDate, false, notify.ErrTemporary, true, 2 * time.Minute},
		{"429 with a past date", http.StatusTooManyRequests, pastDate, false, notify.ErrTemporary, true, 0},
		{
			"503 with a very long wait",
			http.StatusServiceUnavailable,
			"999999999",
			false,
			notify.ErrTemporary,
			true,
			notify.SuspendAfter,
		},
		{"503 with a negative wait", http.StatusServiceUnavailable, "-5", false, notify.ErrTemporary, false, 0},
		{"503 with text", http.StatusServiceUnavailable, "soon", false, notify.ErrTemporary, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)
			r.set(answer(tt.status, tt.retryAfter))

			err := r.sender(t).Send(t.Context(), message([]byte(canary)))
			checkStatus(t, err, tt)
		})
	}
}

// checkStatus compares the error of a Send with the wanted class, and the
// wait of a RetryAfterError when one is wanted.
func checkStatus(t *testing.T, err error, c statusCase) {
	t.Helper()

	if c.wantNil {
		if err != nil {
			t.Fatalf("Send = %v, want nil", err)
		}

		return
	}

	if !errors.Is(err, c.want) {
		t.Fatalf("Send = %v, want %v", err, c.want)
	}

	var ra *notify.RetryAfterError

	gotRA := errors.As(err, &ra)
	if gotRA != c.wantRA {
		t.Fatalf("Send = %v, RetryAfterError = %t, want %t", err, gotRA, c.wantRA)
	}

	if ra != nil && ra.Wait != c.wantWait {
		t.Fatalf("Wait = %s, want %s", ra.Wait, c.wantWait)
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	t.Parallel()

	for _, status := range []int{
		http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect,
	} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			var landed atomic.Int32

			r := newReceiver(t)
			r.set(redirectTo("/land", status, &landed))

			err := r.sender(t).Send(t.Context(), message([]byte(canary)))
			checkRefused(t, err)

			if n := landed.Load(); n != 0 {
				t.Fatalf("the redirect target got %d requests, want 0", n)
			}

			if n := len(r.requests()); n != 1 {
				t.Fatalf("the receiver got %d requests, want 1", n)
			}
		})
	}
}

// redirectTo answers every path but target with a redirect of status to
// target, and counts the requests that reach target.
func redirectTo(target string, status int, landed *atomic.Int32) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == target {
			landed.Add(1)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		http.Redirect(w, req, "https://hooks.example.com"+target, status)
	}
}

// checkRefused fails the test unless err is of the class refused.
func checkRefused(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send = %v, want ErrRefused", err)
	}

	if got := notify.Class(err); got != notify.ClassRefused {
		t.Fatalf("Class = %q, want %q", got, notify.ClassRefused)
	}
}

// TestProductionSenderRefuses uses the sender of New, with the dial guard
// and the trust roots of production. No case reaches the network: the URL
// check or the dial guard refuses each address.
func TestProductionSenderRefuses(t *testing.T) {
	t.Parallel()

	r := newReceiver(t)

	tests := []struct {
		name string
		to   string
	}{
		{"the loopback test server", r.srv.URL + "/ops"},
		{"loopback", "https://127.0.0.1/ops"},
		{"loopback IPv6", "https://[::1]/ops"},
		{"IPv4-mapped loopback", "https://[::ffff:127.0.0.1]/ops"},
		{"private 10/8", "https://10.0.0.5/ops"},
		{"private 172.16/12", "https://172.16.0.5/ops"},
		{"private 192.168/16", "https://192.168.1.1/ops"},
		{"link-local metadata address", "https://169.254.169.254/latest/meta-data"},
		{"unique local IPv6", "https://[fd00::1]/ops"},
		{"shared address space", "https://100.64.0.1/ops"},
		{"unspecified", "https://0.0.0.0/ops"},
		{"localhost", "https://localhost/ops"},
		{"a subdomain of localhost", "https://hooks.localhost/ops"},
		{"hexadecimal address", "https://0x7f000001/ops"},
		{"decimal address", "https://2130706433/ops"},
		{"short address", "https://127.1/ops"},
		{"http", "http://hooks.example.com/ops"},
		{"user information", "https://user:" + canary + "@hooks.example.com/ops"},
		{"fragment", "https://hooks.example.com/ops#frag"},
		{"no host", "https:///ops"},
		{"another scheme", "ftp://hooks.example.com/ops"},
		{"does not parse", "https://hooks.example.com/%zz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := webhook.New()
			t.Cleanup(s.Close)

			m := message([]byte(canary))
			m.To = tt.to

			checkRefused(t, s.Send(t.Context(), m))

			if n := len(r.requests()); n != 0 {
				t.Fatalf("the test server got %d requests, want 0", n)
			}
		})
	}
}

func TestSecretsAreChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		secrets [][]byte
	}{
		{"no secret", nil},
		{"an empty secret", [][]byte{{}}},
		{"one empty of two", [][]byte{[]byte(canary), {}}},
		{"three secrets", [][]byte{[]byte("a"), []byte("b"), []byte("c")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)

			err := r.sender(t).Send(t.Context(), message(tt.secrets...))
			if !errors.Is(err, notify.ErrPermanent) || !errors.Is(err, notify.ErrSecrets) {
				t.Fatalf("Send = %v, want ErrPermanent and ErrSecrets", err)
			}

			if n := len(r.requests()); n != 0 {
				t.Fatalf("the receiver got %d requests, want 0", n)
			}
		})
	}
}

func TestLargeAnswerIsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		size   int
		want   error
	}{
		{"a small body", http.StatusOK, 10, nil},
		{"a body over the limit still gives the status", http.StatusOK, egress.MaxBodyBytes + 1024, nil},
		{"a large body of an error", http.StatusNotFound, egress.MaxBodyBytes + 1024, notify.ErrPermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)
			r.set(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(strings.Repeat("x", tt.size))) //nolint:errcheck // The client may close early.
			})

			err := r.sender(t).Send(t.Context(), message([]byte(canary)))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Send = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestNoCanaryInErrors sends under each failure and checks that the text
// of the error repeats no secret, no token of the URL, no address and no
// text of the answer.
func TestNoCanaryInErrors(t *testing.T) {
	t.Parallel()

	const bodyCanary = "canary-answer-body-5d2e"

	tests := []struct {
		name string
		run  func(t *testing.T) error
	}{
		{"status 404", statusSend(http.StatusNotFound, "")},
		{"status 500", statusSend(http.StatusInternalServerError, "")},
		{"status 429 with Retry-After", statusSend(http.StatusTooManyRequests, "30")},
		{"a redirect", statusSend(http.StatusFound, "")},
		{"a server that is gone", func(t *testing.T) error {
			t.Helper()

			r := newReceiver(t)
			s := r.sender(t)
			r.srv.Close()

			return s.Send(t.Context(), message([]byte(canary), []byte(canarySpare)))
		}},
		{"a canceled context", func(t *testing.T) error {
			t.Helper()

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			return newReceiver(t).sender(t).Send(ctx, message([]byte(canary), []byte(canarySpare)))
		}},
		{"a private address", func(t *testing.T) error {
			t.Helper()

			s := webhook.New()
			t.Cleanup(s.Close)

			m := message([]byte(canary), []byte(canarySpare))
			m.To = "https://10.0.0.5/ops/" + canaryToken

			return s.Send(t.Context(), m)
		}},
		{"an address that does not parse", func(t *testing.T) error {
			t.Helper()

			s := webhook.New()
			t.Cleanup(s.Close)

			m := message([]byte(canary), []byte(canarySpare))
			m.To = "https://hooks.example.com/" + canaryToken + "/%zz"

			return s.Send(t.Context(), m)
		}},
		{"a wrong secret list", func(t *testing.T) error {
			t.Helper()

			return newReceiver(t).sender(t).Send(t.Context(), message([]byte(canary), []byte(canarySpare), []byte("c")))
		}},
		{"the body of the answer", func(t *testing.T) error {
			t.Helper()

			r := newReceiver(t)
			r.set(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(bodyCanary)) //nolint:errcheck // A failed write shows in the status.
			})

			return r.sender(t).Send(t.Context(), message([]byte(canary), []byte(canarySpare)))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run(t)
			if err == nil {
				t.Fatal("Send = nil error")
			}

			for _, c := range []string{canary, canarySpare, canaryToken, bodyCanary, "hooks.example.com", "127.0.0.1", "10.0.0.5"} {
				if strings.Contains(err.Error(), c) {
					t.Fatalf("the error text repeats %q: %v", c, err)
				}
			}
		})
	}
}

// statusSend returns a case that sends to a receiver that answers status.
func statusSend(status int, retryAfter string) func(t *testing.T) error {
	return func(t *testing.T) error {
		t.Helper()

		r := newReceiver(t)
		r.set(answer(status, retryAfter))

		return r.sender(t).Send(t.Context(), message([]byte(canary), []byte(canarySpare)))
	}
}

func TestWrongKindSendsNothing(t *testing.T) {
	t.Parallel()

	for _, kind := range []notify.SinkKind{notify.SinkEmail, notify.SinkChat, ""} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t)
			m := message([]byte(canary))
			m.Sink.Kind = kind

			if err := r.sender(t).Send(t.Context(), m); !errors.Is(err, notify.ErrPermanent) {
				t.Fatalf("Send = %v, want ErrPermanent", err)
			}

			if n := len(r.requests()); n != 0 {
				t.Fatalf("the receiver got %d requests, want 0", n)
			}
		})
	}
}

func TestKind(t *testing.T) {
	t.Parallel()

	if got := webhook.New().Kind(); got != notify.SinkWebhook {
		t.Fatalf("Kind = %q, want %q", got, notify.SinkWebhook)
	}
}
