package otlp_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
	"opendustpan.org/opendustpan/pkg/logsink/otlp"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		r := newReceiver(t, nil)

		return logsinktest.TestSink{
			Sink: newSink(t, r, nil),
			Delivered: func(t *testing.T, _ int) [][]byte {
				t.Helper()

				r.mu.Lock()
				defer r.mu.Unlock()

				out := make([][]byte, 0, len(r.recs))
				for _, lr := range r.recs {
					out = append(out, line(t, lr))
				}

				return out
			},
		}
	})
}

func TestURLPath(t *testing.T) {
	t.Parallel()

	for suffix, want := range map[string]string{
		"":              "POST /v1/logs",
		"/":             "POST /v1/logs",
		"/otel":         "POST /otel/v1/logs",
		"/v1/logs":      "POST /v1/logs",
		"/x/v1/logs/":   "POST /x/v1/logs/",
		"?tenant=a%20b": "POST /v1/logs?tenant=a%20b",
	} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t, nil)

			s, err := otlp.New(otlp.Config{
				Client: r.srv.Client(), Headers: nil, URL: r.srv.URL + suffix, ServiceName: "svc", Timeout: 0,
			})
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			t.Cleanup(func() { closeSink(t, s) })

			if err := s.Write(t.Context(), []logsink.Record{rec("m")}); err != nil {
				t.Fatalf("write: %v", err)
			}

			r.mu.Lock()
			defer r.mu.Unlock()

			if r.paths[0] != want {
				t.Fatalf("request = %q, want %q", r.paths[0], want)
			}

			if !strings.Contains(string(r.bodies[0]), `"stringValue":"svc"`) {
				t.Fatalf("body lacks the service name: %s", r.bodies[0])
			}
		})
	}
}

func TestHeadersAreSent(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, nil)
	s := newSink(t, r, map[string]string{"Authorization": "Bearer abc", "x-api-key": "k"})

	if err := s.Write(t.Context(), []logsink.Record{rec("m")}); err != nil {
		t.Fatalf("write: %v", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	h := r.headers[0]
	if h.Get("Authorization") != "Bearer abc" || h.Get("X-Api-Key") != "k" ||
		h.Get("Content-Type") != "application/json" {
		t.Fatalf("headers = %v", h)
	}
}

func TestStatusAndPartialSuccess(t *testing.T) {
	t.Parallel()

	const (
		partial = `{"partialSuccess":{"rejectedLogRecords":"2","errorMessage":"bad"}}`
		number  = `{"partialSuccess":{"rejectedLogRecords":1}}`
		above   = `{"partialSuccess":{"rejectedLogRecords":"99"}}`
		zero    = `{"partialSuccess":{"rejectedLogRecords":"0","errorMessage":"warn"}}`
	)

	// A count of 0 in skipped, and false in refused, means full success.
	cases := map[string]struct {
		answer  string
		status  int
		skipped int
		refused bool
	}{
		"empty 200":                  {answer: "", status: 200, skipped: 0, refused: false},
		"empty 204":                  {answer: "", status: 204, skipped: 0, refused: false},
		"503":                        {answer: "", status: 503, skipped: 0, refused: true},
		"400":                        {answer: "", status: 400, skipped: 0, refused: true},
		"partial success":            {answer: partial, status: 200, skipped: 2, refused: false},
		"partial success number":     {answer: number, status: 200, skipped: 1, refused: false},
		"partial success above size": {answer: above, status: 200, skipped: 3, refused: false},
		"partial success zero":       {answer: zero, status: 200, skipped: 0, refused: false},
		"answer is no JSON":          {answer: "ok", status: 200, skipped: 0, refused: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newReceiver(t, func(w http.ResponseWriter) {
				w.WriteHeader(tc.status)
				reply(w, tc.answer)
			})
			s := newSink(t, r, nil)

			err := s.Write(t.Context(), []logsink.Record{rec("a"), rec("b"), rec("c")})

			checkWriteResult(t, err, tc.skipped, tc.refused)
		})
	}
}

func checkWriteResult(t *testing.T, err error, skipped int, refused bool) {
	t.Helper()

	var skip *logsink.SkipError

	switch {
	case refused:
		if !errors.Is(err, otlp.ErrRefused) || !errors.Is(err, logsink.ErrRefused) {
			t.Fatalf("got %v, want ErrRefused", err)
		}
	case skipped > 0:
		if !errors.As(err, &skip) || skip.Skipped != skipped {
			t.Fatalf("got %v, want a SkipError with %d records", err, skipped)
		}
	case err != nil:
		t.Fatalf("got %v, want nil", err)
	default:
	}
}

func TestRedirectIsRefusedAndHeadersStayHome(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			var landed atomic.Int32

			other := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
				// Any request here is a leak, and so is the header.
				landed.Add(1)

				if req.Header.Get("Authorization") != "" {
					landed.Add(100)
				}
			}))
			t.Cleanup(other.Close)

			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				http.Redirect(w, req, other.URL+"/land", status)
			}))
			t.Cleanup(srv.Close)

			s, err := otlp.New(otlp.Config{
				Client: srv.Client(), Headers: map[string]string{"Authorization": "Bearer secret-token"},
				URL: srv.URL, ServiceName: "", Timeout: 0,
			})
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			t.Cleanup(func() { closeSink(t, s) })

			err = s.Write(t.Context(), []logsink.Record{rec("m")})
			if !errors.Is(err, otlp.ErrRefused) {
				t.Fatalf("got %v, want ErrRefused", err)
			}

			if n := landed.Load(); n != 0 {
				t.Fatalf("redirect target saw %d (1 per request, 100 per leaked header), want 0", n)
			}
		})
	}
}
