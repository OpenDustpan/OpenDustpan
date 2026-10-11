package s3_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/s3"
)

const (
	testAccess  = "test-access-key-1"
	testSecret  = "test-secret-value-1"
	testToken   = "test-token-value-1"
	testBucket  = "testbucket"
	testRegion  = "us-east-1"
	bodyMarker  = "super-secret-body-marker"
	queryMarker = "marker-query-value"
)

// errSimulatedRead is the failure failReader reports.
var errSimulatedRead = errors.New("simulated read failure")

// capturedRequest holds one request the fake server saw.
type capturedRequest struct {
	method        string
	path          string
	rawQuery      string
	header        http.Header
	body          []byte
	contentLength int64
}

// fakeServer records requests and answers from a script.
type fakeServer struct {
	t       *testing.T
	mu      sync.Mutex
	got     []capturedRequest
	handler func(r *http.Request, body []byte, call int) (int, string)
}

// newFakeServer returns a fakeServer that answers with handler.
func newFakeServer(
	t *testing.T,
	handler func(r *http.Request, body []byte, call int) (int, string),
) *fakeServer {
	t.Helper()

	return &fakeServer{
		t:       t,
		mu:      sync.Mutex{},
		got:     nil,
		handler: handler,
	}
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Errorf("read request body: %v", err)
	}

	_ = r.Body.Close() //nolint:errcheck // Test fake; the body was read above.

	f.mu.Lock()

	call := len(f.got)
	f.got = append(f.got, capturedRequest{
		method:        r.Method,
		path:          r.URL.Path,
		rawQuery:      r.URL.RawQuery,
		header:        r.Header.Clone(),
		body:          body,
		contentLength: r.ContentLength,
	})

	f.mu.Unlock()

	status, answer := f.handler(r, body, call)
	w.Header().Set("Content-Type", "application/xml")

	w.WriteHeader(status)

	if answer != "" {
		if _, err := io.WriteString(w, answer); err != nil {
			f.t.Errorf("write answer: %v", err)
		}
	}
}

func (f *fakeServer) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.got)
}

func (f *fakeServer) first() capturedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.got[0]
}

// bodies returns a copy of every request body the fake server saw.
func (f *fakeServer) bodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([][]byte, 0, len(f.got))
	for _, got := range f.got {
		out = append(out, got.body)
	}

	return out
}

// errorDoc builds a minimal S3 XML error document.
func errorDoc(code string) string {
	return `<?xml version="1.0" encoding="UTF-8"?><Error><Code>` + code +
		`</Code><Message>fake failure</Message></Error>`
}

// testSigner returns a signer for endpoint with fake credentials.
func testSigner(endpoint string, now func() time.Time) *s3.Signer {
	return &s3.Signer{
		Bucket:   testBucket,
		Region:   testRegion,
		Endpoint: endpoint,
		Credentials: s3.Credentials{
			AccessKeyID:     testAccess,
			SecretAccessKey: testSecret,
			SessionToken:    "",
		},
		Now: now,
	}
}

// openStore opens a Store against server with a no-op sleep.
func openStore(
	t *testing.T,
	server *httptest.Server,
	signer *s3.Signer,
	prefix string,
	opts ...s3.Option,
) *s3.Store {
	t.Helper()

	fast := make([]s3.Option, 0, 1+len(opts))
	fast = append(fast, s3.WithSleep(func(_ context.Context, _ time.Duration) error { return nil }))
	fast = append(fast, opts...)

	s, err := s3.New(signer, prefix, server.Client(), fast...)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	var _ blob.Store = s

	return s
}

func mustKey(t *testing.T, name string) blob.Key {
	t.Helper()

	key, err := blob.ParseKey(name)
	if err != nil {
		t.Fatalf("parse key %q: %v", name, err)
	}

	return key
}

// assertNoCredential fails when err holds a credential, a body marker or a query.
func assertNoCredential(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("error is nil, want an error")
	}

	msg := err.Error()

	for _, secret := range []string{testAccess, testSecret, testToken, bodyMarker, queryMarker} {
		if strings.Contains(msg, secret) {
			t.Errorf("error %q contains %q", msg, secret)
		}
	}
}

// checkPutAddress checks the method, path and query of a PUT request.
func checkPutAddress(t *testing.T, got capturedRequest, wantPath string) {
	t.Helper()

	if got.method != http.MethodPut {
		t.Errorf("method = %q, want PUT", got.method)
	}

	if got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}

	if got.rawQuery != "" {
		t.Errorf("query = %q, want empty", got.rawQuery)
	}

	if got.header.Get("If-None-Match") != "*" {
		t.Errorf("If-None-Match = %q, want *", got.header.Get("If-None-Match"))
	}
}

// checkPutAuth checks the signature headers of a PUT request.
func checkPutAuth(t *testing.T, got capturedRequest, want []byte) {
	t.Helper()

	if !bytes.Equal(got.body, want) {
		t.Errorf("body = %q, want %q", got.body, want)
	}

	if got.contentLength != int64(len(want)) {
		t.Errorf("content length = %d, want %d", got.contentLength, len(want))
	}

	if wantHash := s3.PayloadHash(want); got.header.Get("X-Amz-Content-Sha256") != wantHash {
		t.Errorf("payload hash = %q, want %q", got.header.Get("X-Amz-Content-Sha256"), wantHash)
	}

	auth := got.header.Get("Authorization")
	if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+testAccess+"/") {
		t.Errorf("authorization lacks the access key ID: %q", auth)
	}

	for _, secret := range []string{testSecret, testToken} {
		if strings.Contains(auth, secret) {
			t.Error("authorization contains credential material")
		}
	}
}

func TestPutSuccess(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	signer := testSigner(server.URL, nil)
	s := openStore(t, server, signer, "pfx")
	key := mustKey(t, "a/b")
	want := []byte(`{"version":3}`)

	if err := s.Put(t.Context(), key, bytes.NewReader(want)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if got := fake.calls(); got != 1 {
		t.Fatalf("server saw %d calls, want 1", got)
	}

	got := fake.first()
	checkPutAddress(t, got, "/"+testBucket+"/pfx/a/b")
	checkPutAuth(t, got, want)
}

func TestPutSignatureMatchesSigner(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, time.October, 11, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return fixed }

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	signer := testSigner(server.URL, now)
	s := openStore(t, server, signer, "")
	key := mustKey(t, "signed/object")
	body := []byte("signed body")

	if err := s.Put(t.Context(), key, bytes.NewReader(body)); err != nil {
		t.Fatalf("put: %v", err)
	}

	got := fake.first()

	objectURL, err := signer.URL("signed/object")
	if err != nil {
		t.Fatalf("signer URL: %v", err)
	}

	expected, err := http.NewRequestWithContext(t.Context(), http.MethodPut, objectURL.String(), http.NoBody)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if err := signer.Sign(expected, s3.PayloadHash(body)); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if gotAuth, wantAuth := got.header.Get("Authorization"), expected.Header.Get("Authorization"); gotAuth != wantAuth {
		t.Errorf("authorization = %q, want %q", gotAuth, wantAuth)
	}
}

func TestPutPrefixInEveryObjectName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		prefix string
		key    string
		want   string
	}{
		{"no prefix", "", "a/b", "/" + testBucket + "/a/b"},
		{"prefix", "pfx", "a/b", "/" + testBucket + "/pfx/a/b"},
		{"nested prefix", "pfx/sub", "a/b", "/" + testBucket + "/pfx/sub/a/b"},
		{"prefix slashes trimmed", "/pfx/", "a/b", "/" + testBucket + "/pfx/a/b"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
				return http.StatusOK, ""
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			s := openStore(t, server, testSigner(server.URL, nil), tc.prefix)

			if err := s.Put(t.Context(), mustKey(t, tc.key), strings.NewReader("x")); err != nil {
				t.Fatalf("put: %v", err)
			}

			if got := fake.first().path; got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPutExistsMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		code   string
		exists bool
	}{
		{"412 without code", http.StatusPreconditionFailed, "", true},
		{"412 with code", http.StatusPreconditionFailed, "PreconditionFailed", true},
		{"409 conditional conflict", http.StatusConflict, "ConditionalRequestConflict", true},
		{"409 other code", http.StatusConflict, "Conflict", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answer := ""
			if tc.code != "" {
				answer = errorDoc(tc.code)
			}

			fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
				return tc.status, answer
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			s := openStore(t, server, testSigner(server.URL, nil), "")

			err := s.Put(t.Context(), mustKey(t, "a/b"), strings.NewReader("x"))
			if err == nil {
				t.Fatal("put succeeded, want an error")
			}

			assertNoCredential(t, err)

			if tc.exists && !errors.Is(err, blob.ErrExists) {
				t.Errorf("err = %v, want ErrExists", err)
			}

			if !tc.exists && errors.Is(err, blob.ErrExists) {
				t.Errorf("err = %v, want a plain error, not ErrExists", err)
			}
		})
	}
}

func TestPutRetriesThenSucceeds(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, call int) (int, string) {
		if call < 2 {
			return http.StatusInternalServerError, errorDoc("InternalError")
		}

		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	var sleeps int

	s := openStore(t, server, testSigner(server.URL, nil), "",
		s3.WithSleep(func(_ context.Context, _ time.Duration) error {
			sleeps++

			return nil
		}),
	)

	if err := s.Put(t.Context(), mustKey(t, "a/b"), strings.NewReader("x")); err != nil {
		t.Fatalf("put: %v", err)
	}

	if got := fake.calls(); got != 3 {
		t.Fatalf("server saw %d calls, want 3", got)
	}

	bodies := fake.bodies()
	for i := 1; i < len(bodies); i++ {
		if !bytes.Equal(bodies[i], bodies[0]) {
			t.Errorf("retry body %d differs from the first attempt", i)
		}
	}

	if !bytes.Equal(bodies[0], []byte("x")) {
		t.Errorf("body = %q, want %q", bodies[0], "x")
	}

	if sleeps != 2 {
		t.Errorf("sleeps = %d, want 2", sleeps)
	}
}

func TestPutRetryLimit(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusServiceUnavailable, errorDoc("ServiceUnavailable")
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	var sleeps int

	s := openStore(t, server, testSigner(server.URL, nil), "",
		s3.WithMaxAttempts(3),
		s3.WithSleep(func(_ context.Context, _ time.Duration) error {
			sleeps++

			return nil
		}),
	)

	err := s.Put(t.Context(), mustKey(t, "a/b"), strings.NewReader("x"))
	if err == nil {
		t.Fatal("put succeeded, want an error")
	}

	assertNoCredential(t, err)

	if got := fake.calls(); got != 3 {
		t.Errorf("server saw %d calls, want 3", got)
	}

	if sleeps != 2 {
		t.Errorf("sleeps = %d, want 2", sleeps)
	}

	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error %q lacks the status", err.Error())
	}
}

func TestPutRetries429AndSlowDown(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status int
		code   string
	}{
		{"429 without code", 429, ""},
		{"400 SlowDown", http.StatusBadRequest, "SlowDown"},
		{"503 SlowDown", http.StatusServiceUnavailable, "SlowDown"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			answer := ""
			if tc.code != "" {
				answer = errorDoc(tc.code)
			}

			fake := newFakeServer(t, func(_ *http.Request, _ []byte, call int) (int, string) {
				if call == 0 {
					return tc.status, answer
				}

				return http.StatusOK, ""
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			s := openStore(t, server, testSigner(server.URL, nil), "")

			if err := s.Put(t.Context(), mustKey(t, "a/b"), strings.NewReader("x")); err != nil {
				t.Fatalf("put: %v", err)
			}

			if got := fake.calls(); got != 2 {
				t.Errorf("server saw %d calls, want 2", got)
			}
		})
	}
}

func TestPutLargeStreamsThroughTempFile(t *testing.T) {
	t.Parallel()

	const size = 2 << 20

	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	wantHash := s3.PayloadHash(data)

	fake := newFakeServer(t, func(_ *http.Request, body []byte, _ int) (int, string) {
		if len(body) != size {
			t.Errorf("body = %d bytes, want %d", len(body), size)
		}

		if got := s3.PayloadHash(body); got != wantHash {
			t.Errorf("body hash = %s, want %s", got, wantHash)
		}

		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")

	if err := s.Put(t.Context(), mustKey(t, "large/object"), bytes.NewReader(data)); err != nil {
		t.Fatalf("put: %v", err)
	}

	if got := fake.first().contentLength; got != size {
		t.Errorf("content length = %d, want %d", got, size)
	}
}

func TestPutTooLargeUsesNoRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "", s3.WithMaxObjectSize(10))

	err := s.Put(t.Context(), mustKey(t, "a/b"), strings.NewReader("eleven-bytes"))
	if err == nil {
		t.Fatal("put succeeded, want ErrTooLarge")
	}

	if !errors.Is(err, s3.ErrTooLarge) {
		t.Errorf("err = %v, want ErrTooLarge", err)
	}

	assertNoCredential(t, err)

	if got := fake.calls(); got != 0 {
		t.Errorf("server saw %d calls, want none", got)
	}
}

func TestPutFailingReaderSendsNoRequest(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")

	if err := s.Put(t.Context(), mustKey(t, "a/b"), &failReader{off: 0, failAfter: 4}); err == nil {
		t.Fatal("put succeeded, want an error")
	} else {
		assertNoCredential(t, err)
	}

	if got := fake.calls(); got != 0 {
		t.Errorf("server saw %d calls, want none", got)
	}
}

func TestGetRoundTrip(t *testing.T) {
	t.Parallel()

	want := []byte(`{"version":3}`)

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, string(want)
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "pfx")

	rc, err := s.Get(t.Context(), mustKey(t, "a/b"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("body = %q, want %q", got, want)
	}

	seen := fake.first()
	if seen.method != http.MethodGet {
		t.Errorf("method = %q, want GET", seen.method)
	}

	if wantPath := "/" + testBucket + "/pfx/a/b"; seen.path != wantPath {
		t.Errorf("path = %q, want %q", seen.path, wantPath)
	}

	if auth := seen.header.Get("Authorization"); !strings.HasPrefix(
		auth,
		"AWS4-HMAC-SHA256 Credential="+testAccess+"/",
	) {
		t.Errorf("authorization lacks the access key ID: %q", auth)
	}
}

func TestGetNotFoundMapping(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusNotFound, errorDoc("NoSuchKey")
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")

	_, err := s.Get(t.Context(), mustKey(t, "missing"))
	if !errors.Is(err, blob.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}

	assertNoCredential(t, err)
}

func TestGetUnknownErrorHoldsStatusAndCode(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusForbidden, errorDoc("AccessDenied") + bodyMarker
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")

	_, err := s.Get(t.Context(), mustKey(t, "a/b"))
	if err == nil {
		t.Fatal("get succeeded, want an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "403") || !strings.Contains(msg, "AccessDenied") {
		t.Errorf("error %q lacks the status and the code", msg)
	}

	assertNoCredential(t, err)
}

func TestDeleteSucceedsOnMissingKey(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusOK} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			answer := ""
			if status == http.StatusNotFound {
				answer = errorDoc("NoSuchKey")
			}

			fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
				return status, answer
			})
			server := httptest.NewServer(fake)
			t.Cleanup(server.Close)

			s := openStore(t, server, testSigner(server.URL, nil), "pfx")

			if err := s.Delete(t.Context(), mustKey(t, "a/b")); err != nil {
				t.Fatalf("delete answers %d: %v", status, err)
			}

			got := fake.first()
			checkDeleteAddress(t, got, "/"+testBucket+"/pfx/a/b")
		})
	}
}

// checkDeleteAddress checks the method, path and query of a DELETE request.
func checkDeleteAddress(t *testing.T, got capturedRequest, wantPath string) {
	t.Helper()

	if got.method != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", got.method)
	}

	if got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}

	if got.rawQuery != "" {
		t.Errorf("query = %q, want empty", got.rawQuery)
	}
}

func TestPingProvesBucketAnswers(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "ignored-prefix")

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping: %v", err)
	}

	got := fake.first()
	if got.method != http.MethodHead {
		t.Errorf("method = %q, want HEAD", got.method)
	}

	if wantPath := "/" + testBucket; got.path != wantPath {
		t.Errorf("path = %q, want %q", got.path, wantPath)
	}

	if got.rawQuery != "" {
		t.Errorf("query = %q, want empty", got.rawQuery)
	}

	if auth := got.header.Get("Authorization"); !strings.HasPrefix(
		auth,
		"AWS4-HMAC-SHA256 Credential="+testAccess+"/",
	) {
		t.Errorf("authorization lacks the access key ID: %q", auth)
	}
}

func TestPingErrorHoldsNoCredentialOrQuery(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusForbidden, errorDoc("AccessDenied")
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")

	err := s.Ping(t.Context())
	if err == nil {
		t.Fatal("ping succeeded, want an error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "403") {
		t.Errorf("error %q lacks the status", msg)
	}

	assertNoCredential(t, err)

	if strings.Contains(msg, "?") {
		t.Errorf("error %q holds a query string", msg)
	}

	if got := fake.first(); got.rawQuery != "" {
		t.Errorf("ping sent query %q, want empty", got.rawQuery)
	}
}

func TestPingTransportErrorHoldsNoPath(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	endpoint := server.URL
	server.Close()

	s := openStore(t, server, testSigner(endpoint, nil), "")

	err := s.Ping(t.Context())
	if err == nil {
		t.Fatal("ping succeeded, want an error")
	}

	if !errors.Is(err, s3.ErrPingFailed) {
		t.Errorf("err = %v, want ErrPingFailed", err)
	}

	assertNoCredential(t, err)

	msg := err.Error()
	if strings.Contains(msg, testBucket) {
		t.Errorf("error %q holds the bucket path", msg)
	}

	if strings.Contains(msg, "?") {
		t.Errorf("error %q holds a query string", msg)
	}
}

func TestPingWithoutCredentialFailsRedacted(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	signer := testSigner(server.URL, nil)
	signer.Credentials.SecretAccessKey = ""
	s := openStore(t, server, signer, "")

	if err := s.Ping(t.Context()); err == nil {
		t.Fatal("ping succeeded, want an error")
	} else {
		assertNoCredential(t, err)
	}

	if got := fake.calls(); got != 0 {
		t.Errorf("server saw %d calls, want none", got)
	}
}

func TestCanceledContextFails(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, _ int) (int, string) {
		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s := openStore(t, server, testSigner(server.URL, nil), "")
	key := mustKey(t, "a/b")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := s.Put(ctx, key, strings.NewReader("x")); err == nil {
		t.Error("put with canceled context succeeded, want an error")
	}

	if _, err := s.Get(ctx, key); err == nil {
		t.Error("get with canceled context succeeded, want an error")
	}

	if err := s.Delete(ctx, key); err == nil {
		t.Error("delete with canceled context succeeded, want an error")
	}

	if err := s.Ping(ctx); err == nil {
		t.Error("ping with canceled context succeeded, want an error")
	}
}

// failReader fails after failAfter bytes.
type failReader struct {
	off       int
	failAfter int
}

func (r *failReader) Read(p []byte) (int, error) {
	if r.off >= r.failAfter {
		return 0, errSimulatedRead
	}

	n := min(len(p), r.failAfter-r.off)
	for i := range n {
		p[i] = 'x'
	}

	r.off += n

	return n, nil
}

// probeServer is a fake S3 server with a real object map. When honor is true,
// a PUT with If-None-Match: * on an existing object answers 412. When honor
// is false, the PUT overwrites and answers 200. When failPut is true, every
// PUT answers 403.
type probeServer struct {
	t     *testing.T
	mu    sync.Mutex
	holds map[string][]byte
	honor bool
	stuck bool
	seen  []capturedRequest
}

// newProbeServer returns a probeServer that records every request.
func newProbeServer(t *testing.T, honor, failPut bool) *probeServer {
	t.Helper()

	return &probeServer{t: t, mu: sync.Mutex{}, holds: map[string][]byte{}, honor: honor, stuck: failPut, seen: nil}
}

func (p *probeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		p.t.Errorf("read request body: %v", err)
	}

	_ = r.Body.Close() //nolint:errcheck // Test fake; the body was read above.

	p.mu.Lock()
	p.seen = append(p.seen, capturedRequest{
		method:        r.Method,
		path:          r.URL.Path,
		rawQuery:      r.URL.RawQuery,
		header:        r.Header.Clone(),
		body:          body,
		contentLength: r.ContentLength,
	})
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/xml")

	switch r.Method {
	case http.MethodPut:
		p.servePut(w, r, body)
	case http.MethodDelete:
		p.serveDelete(w, r)
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// servePut answers one PUT request against the object map.
func (p *probeServer) servePut(w http.ResponseWriter, r *http.Request, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.stuck {
		w.WriteHeader(http.StatusForbidden)
		if _, err := io.WriteString(w, errorDoc("AccessDenied")); err != nil {
			p.t.Errorf("write answer: %v", err)
		}

		return
	}

	if _, exists := p.holds[r.URL.Path]; exists && p.honor && r.Header.Get("If-None-Match") == "*" {
		w.WriteHeader(http.StatusPreconditionFailed)

		return
	}

	p.holds[r.URL.Path] = body

	w.WriteHeader(http.StatusOK)
}

// serveDelete removes one path from the object map. A missing path is success.
func (p *probeServer) serveDelete(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.holds, r.URL.Path)

	w.WriteHeader(http.StatusNoContent)
}

// putPaths returns the path of every PUT request the server saw.
func (p *probeServer) putPaths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []string

	for _, got := range p.seen {
		if got.method == http.MethodPut {
			out = append(out, got.path)
		}
	}

	return out
}

// deletePaths returns the path of every DELETE request the server saw.
func (p *probeServer) deletePaths() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	var out []string

	for _, got := range p.seen {
		if got.method == http.MethodDelete {
			out = append(out, got.path)
		}
	}

	return out
}

// objectCount returns the number of objects the server still holds.
func (p *probeServer) objectCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.holds)
}

// openProbe opens a Store through Open against server.
func openProbe(t *testing.T, server *httptest.Server, signer *s3.Signer) (*s3.Store, error) {
	t.Helper()

	return s3.Open(
		t.Context(),
		signer,
		"pfx",
		server.Client(),
		s3.WithSleep(func(_ context.Context, _ time.Duration) error { return nil }),
	)
}

func TestOpenProbeSuccess(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s, err := openProbe(t, server, testSigner(server.URL, nil))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if s == nil {
		t.Fatal("open returned a nil store")
	}

	puts := fake.putPaths()

	if len(puts) != 2 {
		t.Fatalf("server saw %d PUTs, want 2", len(puts))
	}

	if puts[0] != puts[1] {
		t.Errorf("probe PUTs went to %q and %q, want one key", puts[0], puts[1])
	}

	if want := "/" + testBucket + "/pfx/probe-"; !strings.HasPrefix(puts[0], want) {
		t.Errorf("probe path = %q, want prefix %q", puts[0], want)
	}

	deletes := fake.deletePaths()

	if len(deletes) != 1 || deletes[0] != puts[0] {
		t.Errorf("deletes = %v, want one delete of %q", deletes, puts[0])
	}

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server still holds %d objects, want none", got)
	}
}

func TestOpenProbeWithoutConditionalWrites(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, false, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	signer := testSigner(server.URL, nil)

	s, err := openProbe(t, server, signer)
	if err == nil {
		t.Fatal("open succeeded, want an error")
	}

	if s != nil {
		t.Error("open returned a store, want nil")
	}

	// A server without conditional writes stops at start with a clear error.
	if !errors.Is(err, s3.ErrNoConditionalWrites) {
		t.Errorf("err = %v, want ErrNoConditionalWrites", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "the store does not support conditional writes; use a store that does") {
		t.Errorf("error %q lacks the fix", msg)
	}

	if !strings.Contains(msg, testBucket) || !strings.Contains(msg, signer.Endpoint) {
		t.Errorf("error %q lacks the endpoint and the bucket", msg)
	}

	assertNoCredential(t, err)

	if got := len(fake.putPaths()); got != 2 {
		t.Errorf("server saw %d PUTs, want 2", got)
	}

	if got := len(fake.deletePaths()); got != 1 {
		t.Errorf("server saw %d DELETEs, want 1", got)
	}

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server still holds %d objects, want none", got)
	}
}

func TestOpenProbeFirstWriteFails(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, true)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s, err := openProbe(t, server, testSigner(server.URL, nil))
	if err == nil {
		t.Fatal("open succeeded, want an error")
	}

	if s != nil {
		t.Error("open returned a store, want nil")
	}

	if errors.Is(err, s3.ErrNoConditionalWrites) {
		t.Errorf("err = %v, want a first-write error, not ErrNoConditionalWrites", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "probe first write") {
		t.Errorf("error %q lacks the probe detail", msg)
	}

	if !strings.Contains(msg, "403") || !strings.Contains(msg, "AccessDenied") {
		t.Errorf("error %q lacks the status and the code", msg)
	}

	assertNoCredential(t, err)

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server still holds %d objects, want none", got)
	}
}

func TestOpenProbeSecondWriteFails(t *testing.T) {
	t.Parallel()

	fake := newFakeServer(t, func(_ *http.Request, _ []byte, call int) (int, string) {
		if call == 1 {
			return http.StatusForbidden, errorDoc("AccessDenied")
		}

		return http.StatusOK, ""
	})
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	s, err := s3.Open(
		t.Context(),
		testSigner(server.URL, nil),
		"pfx",
		server.Client(),
		s3.WithSleep(func(_ context.Context, _ time.Duration) error { return nil }),
	)
	if err == nil {
		t.Fatal("open succeeded, want an error")
	}

	if s != nil {
		t.Error("open returned a store, want nil")
	}

	if errors.Is(err, s3.ErrNoConditionalWrites) {
		t.Errorf("err = %v, want a second-write error, not ErrNoConditionalWrites", err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "probe second write") {
		t.Errorf("error %q lacks the probe detail", msg)
	}

	assertNoCredential(t, err)

	if got := fake.calls(); got != 3 {
		t.Errorf("server saw %d calls, want 2 PUTs and 1 DELETE", got)
	}
}

func TestOpenProbeKeysAreUniqueUnderPrefix(t *testing.T) {
	t.Parallel()

	fake := newProbeServer(t, true, false)
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	signer := testSigner(server.URL, nil)

	if _, err := openProbe(t, server, signer); err != nil {
		t.Fatalf("first open: %v", err)
	}

	if _, err := openProbe(t, server, signer); err != nil {
		t.Fatalf("second open: %v", err)
	}

	puts := fake.putPaths()

	if len(puts) != 4 {
		t.Fatalf("server saw %d PUTs, want 4", len(puts))
	}

	first, second := puts[0], puts[2]
	if first == second {
		t.Errorf("both opens used %q, want unique keys", first)
	}

	for _, got := range []string{first, second} {
		if want := "/" + testBucket + "/pfx/probe-"; !strings.HasPrefix(got, want) {
			t.Errorf("probe path = %q, want prefix %q", got, want)
		}
	}

	if got := fake.objectCount(); got != 0 {
		t.Errorf("server still holds %d objects, want none", got)
	}
}
