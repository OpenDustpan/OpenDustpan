package webhook_test

import (
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// testSecret keys the signatures of the test receivers.
var testSecret = []byte("test webhook secret") //nolint:gochecknoglobals // A fixed test key shared by the receivers.

func TestConformance(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		r := newReceiver(t, http.StatusNoContent)

		s, err := webhook.New(webhook.Config{URL: r.srv.URL, Secret: testSecret, Client: r.srv.Client(), Timeout: 0})
		if err != nil {
			t.Fatalf("new: %v", err)
		}

		return logsinktest.TestSink{
			Sink: s,
			Delivered: func(t *testing.T, _ int) [][]byte {
				t.Helper()

				return r.records(t)
			},
		}
	})
}

func TestWebhookSinkRefusesBlockedURL(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, http.StatusNoContent)

	client := egresshttp.ClientWith(
		egress.Rules{Allow: nil, LoopbackForTests: false},
		egress.NoRedirect, egress.HTTPSOnly,
		egresshttp.Options{RootCAs: nil, Resolver: nil},
	)
	t.Cleanup(client.CloseIdleConnections)

	s, err := webhook.New(webhook.Config{Client: client, URL: r.srv.URL, Secret: testSecret, Timeout: 0})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "m", Attrs: nil}

	err = s.Write(t.Context(), []logsink.Record{rec})
	if !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("write to a loopback URL: got %v, want %v", err, egress.ErrRefused)
	}

	if errors.Is(err, egresshttp.ErrDialRefused) {
		t.Fatalf("write to a loopback URL: got %v, want the URL check, not the dial guard", err)
	}
}

func TestNon2xxAnswerIsAnError(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, http.StatusServiceUnavailable)

	s, err := webhook.New(webhook.Config{URL: r.srv.URL, Secret: testSecret, Client: r.srv.Client(), Timeout: 0})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "m", Attrs: nil}

	if err := s.Write(t.Context(), []logsink.Record{rec}); err == nil {
		t.Fatal("write to a receiver that answers 503: got nil error")
	}
}

func TestARedirectIsRefusedAndNotFollowed(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			var landed atomic.Int32

			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/land" {
					landed.Add(1)
					w.WriteHeader(http.StatusOK)

					return
				}

				http.Redirect(w, req, "/land", status)
			}))
			t.Cleanup(srv.Close)

			s, err := webhook.New(webhook.Config{
				URL: srv.URL + "/hook", Secret: testSecret, Client: srv.Client(), Timeout: 0,
			})
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			closeOnCleanup(t, s)

			rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "m", Attrs: nil}

			if err := s.Write(t.Context(), []logsink.Record{rec}); !errors.Is(err, webhook.ErrRefused) {
				t.Fatalf("write to a receiver that redirects: got %v, want ErrRefused", err)
			}

			if n := landed.Load(); n != 0 {
				t.Fatalf("the sink followed the redirect %d times", n)
			}
		})
	}
}

func TestABadURLErrorHoldsNoCredentials(t *testing.T) {
	t.Parallel()

	// The test builds the URL from parts, so no credential URL is in the
	// source for the secret scanner to report.
	userinfo := url.UserPassword("user", "canary-password").String()

	_, err := webhook.New(config("https://"+userinfo+"@exa mple.com/hook", testSecret))
	if err == nil {
		t.Fatal("new with a URL that does not parse: got nil error")
	}

	if strings.Contains(err.Error(), "canary-password") {
		t.Fatalf("error holds the credentials: %v", err)
	}
}

func TestATransportErrorHoldsNoURL(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, http.StatusNoContent)
	target := r.srv.URL + "/hook?token=canary-token-value"
	r.srv.Close()

	s, err := webhook.New(webhook.Config{URL: target, Secret: testSecret, Client: r.srv.Client(), Timeout: 0})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "m", Attrs: nil}

	err = s.Write(t.Context(), []logsink.Record{rec})
	if err == nil {
		t.Fatal("write to a closed receiver: got nil error")
	}

	if strings.Contains(err.Error(), "canary-token-value") {
		t.Fatalf("error holds the URL: %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  webhook.Config
	}{
		{name: "plain http", cfg: config("http://example.com/hook", testSecret)},
		{name: "no host", cfg: config("https:///hook", testSecret)},
		{name: "empty secret", cfg: config("https://example.com/hook", nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := webhook.New(tc.cfg); err == nil {
				t.Fatal("new: got nil error")
			}
		})
	}
}

func TestSignIsHMACOverTimestampAndBody(t *testing.T) {
	t.Parallel()

	a := webhook.Sign(testSecret, "100", []byte(`[]`))
	b := webhook.Sign(testSecret, "101", []byte(`[]`))
	c := webhook.Sign([]byte("other"), "100", []byte(`[]`))

	if a == b || a == c {
		t.Fatalf("signatures do not depend on the timestamp and the key: %s %s %s", a, b, c)
	}

	if a != webhook.Sign(testSecret, "100", []byte(`[]`)) {
		t.Fatal("signature is not stable")
	}
}

// config returns a configuration with the default client and timeout.
func config(rawURL string, secret []byte) webhook.Config {
	return webhook.Config{URL: rawURL, Secret: secret, Client: nil, Timeout: 0}
}

// receiver is a TLS test server that checks each signature and keeps the
// records it receives.
type receiver struct {
	srv  *httptest.Server
	recs [][]byte
	bad  int
	mu   sync.Mutex
}

func newReceiver(t *testing.T, status int) *receiver {
	t.Helper()

	r := &receiver{srv: nil, recs: nil, bad: 0, mu: sync.Mutex{}}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		r.mu.Lock()
		defer r.mu.Unlock()

		ts := req.Header.Get(webhook.HeaderTimestamp)
		sig := req.Header.Get(webhook.HeaderSignature)

		if _, err := strconv.ParseInt(ts, 10, 64); err != nil ||
			!hmac.Equal([]byte(sig), []byte(webhook.Sign(testSecret, ts, body))) {
			r.bad++

			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		var batch []json.RawMessage
		if err := json.Unmarshal(body, &batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		for _, m := range batch {
			r.recs = append(r.recs, m)
		}

		w.WriteHeader(status)
	}))

	t.Cleanup(r.srv.Close)

	return r
}

// records answers the records received so far. It fails the test when a
// request carried a bad signature.
func (r *receiver) records(t *testing.T) [][]byte {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.bad > 0 {
		t.Fatalf("receiver refused %d requests with a bad signature", r.bad)
	}

	return append([][]byte(nil), r.recs...)
}

// closeOnCleanup closes s when the test ends and reports a failed close.
func closeOnCleanup(t *testing.T, s logsink.Sink) {
	t.Helper()

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sink: %v", err)
		}
	})
}
