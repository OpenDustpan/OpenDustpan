package otlp_test

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/otlp"
)

// Each test below sends hostile input to one layer, as if the other layers
// had failed.

// TestNewRefusesBadConfig is the boundary layer: New refuses a URL or a
// header that is not valid, and its error repeats no secret.
func TestNewRefusesBadConfig(t *testing.T) {
	t.Parallel()

	const canary = "value-canary"

	cases := map[string]struct {
		headers map[string]string
		url     string
	}{
		"http":              {url: "http://collector:4318", headers: nil},
		"no scheme":         {url: "collector:4318", headers: nil},
		"no host":           {url: "https://", headers: nil},
		"empty":             {url: "", headers: nil},
		"ftp":               {url: "ftp://collector", headers: nil},
		"user info":         {url: "https://user:" + canary + "@collector", headers: nil},
		"bad escape":        {url: "https://collector/%zz", headers: nil},
		"CRLF in value":     {url: "https://c", headers: map[string]string{"X-A": canary + "\r\nX-Evil: 1"}},
		"NUL in value":      {url: "https://c", headers: map[string]string{"X-A": canary + "\x00"}},
		"DEL in value":      {url: "https://c", headers: map[string]string{"X-A": canary + "\x7f"}},
		"CRLF in name":      {url: "https://c", headers: map[string]string{"X-A\r\nX-B": canary}},
		"colon in name":     {url: "https://c", headers: map[string]string{"X:A": canary}},
		"space in name":     {url: "https://c", headers: map[string]string{"X A": canary}},
		"empty name":        {url: "https://c", headers: map[string]string{"": canary}},
		"content-type":      {url: "https://c", headers: map[string]string{"Content-Type": canary}},
		"host":              {url: "https://c", headers: map[string]string{"Host": canary}},
		"content-length":    {url: "https://c", headers: map[string]string{"content-length": canary}},
		"transfer-encoding": {url: "https://c", headers: map[string]string{"Transfer-Encoding": canary}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s, err := otlp.New(otlp.Config{Client: nil, Headers: tc.headers, URL: tc.url, ServiceName: "", Timeout: 0})
			if !errors.Is(err, logsink.ErrInvalidConfig) {
				t.Fatalf("got %v, want ErrInvalidConfig", err)
			}

			if s != nil {
				t.Fatal("a sink came back with an error")
			}

			if strings.Contains(err.Error(), canary) {
				t.Fatalf("the error repeats a secret: %v", err)
			}
		})
	}
}

// TestCheckHeader is the layer at the point of use: Write calls CheckHeader
// again, so it must stand alone.
func TestCheckHeader(t *testing.T) {
	t.Parallel()

	for _, ok := range [][2]string{{"Authorization", "Bearer x"}, {"x-api-key", "a\tb"}, {"X_1.2-3", ""}} {
		if err := otlp.CheckHeader(ok[0], ok[1]); err != nil {
			t.Errorf("CheckHeader(%q): %v", ok[0], err)
		}
	}

	for _, bad := range [][2]string{{"X-A", "secret\n"}, {"X-A", "se\rcret"}, {"X\nA", "v"}, {"Content-Length", "1"}, {"", "v"}} {
		err := otlp.CheckHeader(bad[0], bad[1])
		if err == nil {
			t.Errorf("CheckHeader(%q, %q): got nil error", bad[0], bad[1])

			continue
		}

		if strings.Contains(err.Error(), "secret") {
			t.Errorf("the error repeats the value: %v", err)
		}
	}
}

// TestHostileAttributesAreRefused is the encoding layer: a value that JSON
// cannot hold, or that nests too deep, fails the write and ships nothing.
func TestHostileAttributesAreRefused(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, nil)
	s := newSink(t, r, nil)

	cases := map[string]logsink.Attr{
		"deep array": logsink.RawJSON("a", json.RawMessage(strings.Repeat("[", 200)+strings.Repeat("]", 200))),
		"deep object": logsink.RawJSON(
			"a",
			json.RawMessage(strings.Repeat(`{"a":`, 200)+"1"+strings.Repeat("}", 200)),
		),
		"trailing":     logsink.RawJSON("a", json.RawMessage(`1 2`)),
		"broken":       logsink.RawJSON("a", json.RawMessage(`{"a":`)),
		"not JSON":     logsink.RawJSON("a", json.RawMessage(`x`)),
		"NaN":          {Key: "a", Value: math.NaN()},
		"infinity":     {Key: "a", Value: math.Inf(1)},
		"unsupported":  {Key: "a", Value: make(chan int)},
		"raw infinity": logsink.RawJSON("a", json.RawMessage(`1e999`)),
	}

	for name, attr := range cases {
		if err := s.Write(t.Context(), []logsink.Record{recWith("m", attr)}); err == nil {
			t.Errorf("%s: got nil error", name)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.bodies) != 0 {
		t.Fatalf("receiver got %d requests, want 0: a bad record must not ship", len(r.bodies))
	}
}

// TestHostileTextIsEscaped is the encoding layer for text: quotes, control
// characters and a fake JSON member in a message or a key stay data.
func TestHostileTextIsEscaped(t *testing.T) {
	t.Parallel()

	r := newReceiver(t, nil)
	s := newSink(t, r, nil)

	msg := "\"}]}]}],\"resourceLogs\":[{\"x\":\"\n \x00"
	key := "k\"},{\"key\":\"evil"

	if err := s.Write(t.Context(), []logsink.Record{recWith(msg, logsink.String(key, msg))}); err != nil {
		t.Fatalf("write: %v", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.recs) != 1 || len(r.recs[0].Attributes) != 1 {
		t.Fatalf("records = %+v, want one record with one attribute", r.recs)
	}

	got := r.recs[0]
	if *got.Body.StringValue != msg || got.Attributes[0].Key != key || *got.Attributes[0].Value.StringValue != msg {
		t.Fatalf("text did not survive: %+v", got)
	}
}

// TestErrorsHoldNoSecret is the layer of error text: a refusal that echoes
// the secret, and a dial failure whose url.Error would repeat the URL, leave
// the secret out of the error.
func TestErrorsHoldNoSecret(t *testing.T) {
	t.Parallel()

	const secret = "s3cr3t-canary"

	r := newReceiver(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		reply(w, secret)
	})

	s, err := otlp.New(otlp.Config{
		Client: r.srv.Client(), Headers: map[string]string{"Authorization": "Bearer " + secret},
		URL: r.srv.URL + "?token=" + secret, ServiceName: "", Timeout: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { closeSink(t, s) })

	err = s.Write(t.Context(), []logsink.Record{rec("m")})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("refusal error = %v", err)
	}

	dead := httptest.NewTLSServer(http.NotFoundHandler())
	addr := dead.URL
	client := dead.Client()

	dead.Close()

	s2, err := otlp.New(otlp.Config{
		Client: client, Headers: map[string]string{"Authorization": "Bearer " + secret},
		URL: addr + "?token=" + secret, ServiceName: "", Timeout: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { closeSink(t, s2) })

	err = s2.Write(t.Context(), []logsink.Record{rec("m")})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("dial error = %v", err)
	}
}
