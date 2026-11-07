package proxy_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher/proxy"
)

// oddProbe has characters that make each encoded form differ.
const oddProbe = `ZQ9"se/cr+et<&>ZQ9 ?`

// formsOfProbe returns each form that a response could hold, by name.
func formsOfProbe(t *testing.T, value string) map[string]string {
	t.Helper()

	j, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	return map[string]string{
		"plain":      value,
		"std":        base64.StdEncoding.EncodeToString([]byte(value)),
		"url":        base64.URLEncoding.EncodeToString([]byte(value)),
		"raw std":    base64.RawStdEncoding.EncodeToString([]byte(value)),
		"raw url":    base64.RawURLEncoding.EncodeToString([]byte(value)),
		"json":       string(j[1 : len(j)-1]),
		"query":      url.QueryEscape(value),
		"path":       url.PathEscape(value),
		"query low":  lowerEscapes(url.QueryEscape(value)),
		"basic pair": "svc:" + value,
	}
}

// lowerEscapes lowers the hex digits of each percent escape.
func lowerEscapes(s string) string {
	b := []byte(s)

	for i := 0; i+2 < len(b); i++ {
		if b[i] != '%' {
			continue
		}

		for j := i + 1; j <= i+2; j++ {
			if b[j] >= 'A' && b[j] <= 'F' {
				b[j] += 'a' - 'A'
			}
		}
	}

	return string(b)
}

// chunkReader gives its chunks one by one.
type chunkReader struct {
	chunks []string
}

func (c *chunkReader) Read(p []byte) (int, error) {
	for len(c.chunks) > 0 && c.chunks[0] == "" {
		c.chunks = c.chunks[1:]
	}

	if len(c.chunks) == 0 {
		return 0, io.EOF
	}

	n := copy(p, c.chunks[0])
	c.chunks[0] = c.chunks[0][n:]

	return n, nil
}

// TestScanFormsAndBorders is the scan layer on its own: each form of the
// secret, split at every byte of the stream, breaks the stream, and no part
// of the form reaches the reader before the break.
func TestScanFormsAndBorders(t *testing.T) {
	t.Parallel()

	secrets := []string{oddProbe, "svc:" + oddProbe}

	for name, form := range formsOfProbe(t, oddProbe) {
		body := strings.Repeat(".", 50) + form + strings.Repeat(".", 50)

		for split := 0; split <= len(body); split++ {
			r := proxy.ProbeScanReader(secrets, &chunkReader{chunks: []string{body[:split], body[split:]}})

			got, err := io.ReadAll(r)
			if !errors.Is(err, proxy.ErrAuthInResponse) {
				t.Fatalf("%s split %d: error %v, want ErrAuthInResponse", name, split, err)
			}

			// The filler is dots, so any other byte in the output is part of
			// the form.
			if strings.Trim(string(got), ".") != "" {
				t.Fatalf("%s split %d: the reader released %q before the break", name, split, got)
			}
		}
	}
}

// TestScanCleanStreamPasses checks that a stream with no form passes intact,
// also when it is longer than the window and ends inside the window.
func TestScanCleanStreamPasses(t *testing.T) {
	t.Parallel()

	body := bytes.Repeat([]byte("clean data 0123456789 "), 5000)

	for _, cut := range []int{0, 1, 7, 100, len(body) - 3, len(body)} {
		r := proxy.ProbeScanReader(
			[]string{oddProbe},
			&chunkReader{chunks: []string{string(body[:cut]), string(body[cut:])}},
		)

		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("cut %d: err %v, %d of %d bytes", cut, err, len(got), len(body))
		}
	}
}

// TestScanWindow checks that the window is the longest form.
func TestScanWindow(t *testing.T) {
	t.Parallel()

	longest := 0
	for _, f := range proxy.ProbeForms(oddProbe) {
		longest = max(longest, len(f))
	}

	if got := proxy.ProbeScanWindow([]string{oddProbe}); got != longest {
		t.Errorf("window %d, want %d", got, longest)
	}

	if proxy.ProbeScanWindow(nil) != 0 || len(proxy.ProbeForms("")) != 0 {
		t.Error("an empty secret must give no form")
	}
}

// TestResponseScanAtTheProxy sends each form through the proxy. The body
// must break, and the client must not get the form.
func TestResponseScanAtTheProxy(t *testing.T) {
	t.Parallel()

	for name, form := range formsOfProbe(t, probeValue) {
		if name == "basic pair" || name == "query low" {
			continue
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, withHandler(func(w http.ResponseWriter, _ *http.Request) {
				half := len(form) / 2

				write(w, strings.Repeat(".", 20)+form[:half])
				flush(w)
				write(w, form[half:]+strings.Repeat(".", 20))
			}))

			rep, err := tryGet(t, e.client(t, http1), e.base()+"/v1/x", "")
			if err == nil {
				t.Errorf("the body ended without an error: %q", rep.body)
			}

			if strings.Contains(rep.body, form[:len(form)/2]) {
				t.Errorf("the client got a part of the credential: %q", rep.body)
			}

			if rec := e.sink.lastReq(t, 1); rec.Reason != proxy.ReasonAuthInResponse {
				t.Errorf("record %+v, want credential-in-response", rec)
			}
		})
	}
}

// write writes s to w. A write error ends the handler of the fake host.
func write(w http.ResponseWriter, s string) {
	if _, err := w.Write([]byte(s)); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// flush flushes w.
func flush(w http.ResponseWriter) {
	if err := http.NewResponseController(w).Flush(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

// TestResponseHeaderScan checks a credential in a response header, and an
// encoded response that the scan cannot read.
func TestResponseHeaderScan(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		name   string
		value  string
		reason proxy.Reason
	}{
		"credential in a header": {name: "X-Echo", value: "v=" + probeValue, reason: proxy.ReasonAuthInResponse},
		"gzip":                   {name: "Content-Encoding", value: "gzip", reason: proxy.ReasonEncodedResponse},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t, withHandler(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(tt.name, tt.value)
				write(w, "body")
			}))

			rep := e.get(t, http1, "/v1/x")
			if rep.status != http.StatusBadGateway {
				t.Errorf("status %d, want 502", rep.status)
			}

			assertNoProbe(t, "the body", rep.body)

			if rec := e.sink.lastReq(t, 1); rec.Reason != tt.reason {
				t.Errorf("record %+v, want %q", rec, tt.reason)
			}
		})
	}
}

// TestRequestSendsIdentityEncoding checks that the proxy asks for an
// unencoded response when it scans.
func TestRequestSendsIdentityEncoding(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	c, br := e.rawTLS(t)

	rawRequest(t, c, br, "GET /v1/x HTTP/1.1\r\n"+hostHeader(e)+"Accept-Encoding: gzip, br\r\n\r\n")

	if got := e.up.requests()[0].Header.Get("Accept-Encoding"); got != "identity" {
		t.Errorf("Accept-Encoding = %q, want identity", got)
	}
}
