package logsink_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob/s3"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/objectstore"
	"opendustpan.org/opendustpan/pkg/logsink/slogsink"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// Canary values for the never-shipped test. They are test values only and
// look like no real token format. No record field ever holds one: every
// record below holds ordinary values only, so a shipped canary proves the
// sink added data that was not in the record.
//
//nolint:gosec // Canary values for the never-shipped test, not credentials.
const (
	canaryCred = "canary-cred-7f3e9a1b"
	canaryPath = "/canary-data/do-not-log/key.pem"
	canaryBody = "canary-body-5d2c"
)

// canaries holds every canary value the test checks for.
var canaries = []string{canaryCred, canaryPath, canaryBody} //nolint:gochecknoglobals // Shared by the canary subtests.

// errCanaryTarget reports a failed target in the canary test. The text that
// a careless adapter echoes would hold every canary value.
var errCanaryTarget = errors.New(
	"target down: " + canaryCred + " " + canaryPath + " " + canaryBody,
)

// requireNoCanary fails when haystack holds any canary value. where names
// the output under check, so a failure points at the leaking channel.
func requireNoCanary(t *testing.T, where, haystack string) {
	t.Helper()

	requireNoValue(t, where, haystack, canaries...)
}

// requireNoValue fails the test when haystack holds any of the values.
func requireNoValue(t *testing.T, where, haystack string, values ...string) {
	t.Helper()

	for _, v := range values {
		if strings.Contains(haystack, v) {
			t.Fatalf("%s holds canary value %q", where, v)
		}
	}
}

// canaryErrText answers err.Error, or "" when err is nil, so a canary
// check can read an error without a nil dereference.
func canaryErrText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// benignRecord returns a record with ordinary values only: the path and the
// message the server puts in an audit or application record.
func benignRecord() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("path", "/api/stacks"),
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 1),
		},
	}
}

// longRecord returns a record with an ordinary long message that sits above
// the smallest syslog cap. The syslog sink refuses it and never sends it.
func longRecord() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 15, 12, 0, 1, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: strings.Repeat("audit ", 100),
		Attrs: []logsink.Attr{
			logsink.String("path", "/api/stacks"),
			logsink.String("scope", "acme"),
		},
	}
}

// skipRecord returns a record that cannot fit under the smallest syslog cap.
// Its long scope value overflows the cap and the sink skips the record. It
// holds no canary: the skip reason must prove that.
func skipRecord() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 15, 12, 0, 2, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("path", "/api/stacks"),
			logsink.String("scope", strings.Repeat("acme-", 120)),
		},
	}
}

// TestCanaryNoAdapterLeaksSecrets proves no adapter ships data that was not
// in the record, and no adapter echoes its own secrets or its inputs into a
// shipped record or an error. Each row uses the test double of one adapter:
// a temp dir, a local UDP or TCP listener, a local HTTPS server, a local S3
// endpoint, or a memory sink. No row calls any outside network. The records
// hold ordinary values only. The canaries sit where a sink holds data that
// must never ship: the webhook secret, the object storage secret, the file
// sink path, a refused answer body, or a failed target error.
func TestCanaryNoAdapterLeaksSecrets(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{name: "file", run: canaryFile},
		{name: "syslog udp", run: canarySyslogUDP},
		{name: "syslog tcp", run: canarySyslogTCP},
		{name: "webhook", run: canaryWebhook},
		{name: "objectstore", run: canaryObjectStore},
		{name: "slogsink", run: canarySlogsink},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tc.run(t)
		})
	}
}

// canaryFile writes a benign record to a file sink, drops a benign record
// on a closed sink, and opens a sink whose path holds the path canary under
// a parent that is a regular file, so the open fails before any write. The
// shipped lines and the errors must hold no canary.
func canaryFile(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	s, err := file.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close sink: %v", closeErr)
		}
	})

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	// The shipped lines hold only the benign record.
	data, readErr := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	requireNoCanary(t, "shipped file lines", string(data))

	if !bytes.Contains(data, []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped file lines lack the benign record: %q", data)
	}

	// The path canary below sits in the sink configuration only. The test
	// never writes to it: the open fails first, and that failure is the
	// point, so the error path is exercised too.
	blocker := filepath.Join(t.TempDir(), "blocker")

	if writeErr := os.WriteFile(blocker, []byte("block"), 0o600); writeErr != nil {
		t.Fatalf("write blocker: %v", writeErr)
	}

	badPath := filepath.Join(blocker, canaryPath)

	_, badErr := file.Open(badPath)
	if badErr == nil {
		t.Fatal("open on an unusable directory: got nil error")
	}

	// The open error names the configured path on purpose; it reaches only
	// the operator. The credential and body canaries must still stay out.
	requireNoValue(t, "file open error", canaryErrText(badErr), canaryCred, canaryBody)

	closed, openErr := file.Open(filepath.Join(t.TempDir(), "closed.jsonl"))
	if openErr != nil {
		t.Fatalf("open closed sink: %v", openErr)
	}

	if closeErr := closed.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	err = closed.Write(t.Context(), []logsink.Record{benignRecord()})
	if !errors.Is(err, logsink.ErrClosed) {
		t.Fatalf("write to a closed file sink: got %v, want ErrClosed", err)
	}

	requireNoCanary(t, "closed file error", canaryErrText(err))
}

// canarySyslogUDP writes a benign record to a local UDP receiver, writes a
// long benign record above the cap to a capped sink, and writes a record
// that cannot fit at all. The first ships, the last two skip. The delivered
// messages, the skip reason and the skip error hold no canary. The syslog
// configuration holds no password, token or secret: the only
// secret-adjacent field is the TLS client configuration, which the UDP case
// does not use.
func canarySyslogUDP(t *testing.T) {
	t.Helper()

	r := listenCanaryUDP(t)

	s, err := syslog.New(syslog.Config{
		TLSConfig: nil,
		Network:   syslog.UDP,
		Address:   r.addr,
		Hostname:  "",
		AppName:   "",
		Facility:  0,
		Timeout:   0,

		MaxMessageBytes: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close sink: %v", closeErr)
		}
	})

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	msgs := r.waitFor(t, 1)
	requireNoCanary(t, "shipped syslog message", string(msgs[0]))

	if !bytes.Contains(msgs[0], []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped syslog message lacks the benign record: %q", msgs[0])
	}

	capped, capErr := syslog.New(syslog.Config{
		TLSConfig: nil,
		Network:   syslog.UDP,
		Address:   r.addr,
		Hostname:  "",
		AppName:   "",
		Facility:  0,
		Timeout:   0,

		MaxMessageBytes: syslog.MinMaxMessageBytes,
	})
	if capErr != nil {
		t.Fatalf("new capped sink: %v", capErr)
	}

	t.Cleanup(func() {
		if closeErr := capped.Close(); closeErr != nil {
			t.Errorf("close capped sink: %v", closeErr)
		}
	})

	// A record above the cap is refused and never sent, so the write
	// answers a skip error and the receiver keeps only the benign record.
	// The sink never cuts a field and never sends a changed entry.
	longErr := capped.Write(t.Context(), []logsink.Record{longRecord()})

	longSkip, ok := errors.AsType[*logsink.SkipError](longErr)
	if !ok || !errors.Is(longErr, logsink.ErrSkipped) {
		t.Fatalf("write above the cap: got %v, want a skip error", longErr)
	}

	requireNoCanary(t, "syslog skip reason", longSkip.Reason)
	requireNoCanary(t, "syslog skip error", canaryErrText(longErr))

	pauseCanary(t)

	msgs = r.waitFor(t, 1)
	if len(msgs) != 1 {
		t.Fatalf("syslog messages = %d, want 1", len(msgs))
	}

	// A record that cannot fit is skipped with a fixed reason that echoes
	// neither the record nor any target detail.
	capErr = capped.Write(t.Context(), []logsink.Record{skipRecord()})

	skip, ok := errors.AsType[*logsink.SkipError](capErr)
	if !ok || !errors.Is(capErr, logsink.ErrSkipped) {
		t.Fatalf("write that cannot fit: got %v, want a skip error", capErr)
	}

	requireNoCanary(t, "syslog skip reason", skip.Reason)
	requireNoCanary(t, "syslog skip error", canaryErrText(capErr))
}

// canarySyslogTCP writes a benign record to a local TCP receiver, writes a
// long benign record above the cap to a capped sink, and writes a record
// that cannot fit at all. The first ships, the last two skip. The delivered
// messages, the skip reason and the skip error hold no canary. The syslog
// configuration holds no password, token or secret: the only
// secret-adjacent field is the TLS client configuration, which the TCP case
// does not use.
func canarySyslogTCP(t *testing.T) {
	t.Helper()

	r := listenCanaryStream(t)

	s, err := syslog.New(syslog.Config{
		TLSConfig: nil,
		Network:   syslog.TCP,
		Address:   r.addr,
		Hostname:  "",
		AppName:   "",
		Facility:  0,
		Timeout:   0,

		MaxMessageBytes: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close sink: %v", closeErr)
		}
	})

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	msgs := r.waitFor(t, 1)
	requireNoCanary(t, "shipped syslog message", string(msgs[0]))

	if !bytes.Contains(canaryBodyOf(t, msgs[0]), []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped syslog message lacks the benign record: %q", msgs[0])
	}

	capped, capErr := syslog.New(syslog.Config{
		TLSConfig: nil,
		Network:   syslog.TCP,
		Address:   r.addr,
		Hostname:  "",
		AppName:   "",
		Facility:  0,
		Timeout:   0,

		MaxMessageBytes: syslog.MinMaxMessageBytes,
	})
	if capErr != nil {
		t.Fatalf("new capped sink: %v", capErr)
	}

	t.Cleanup(func() {
		if closeErr := capped.Close(); closeErr != nil {
			t.Errorf("close capped sink: %v", closeErr)
		}
	})

	// A record above the cap is refused and never sent, so the write
	// answers a skip error and the receiver keeps only the benign record.
	// The sink never cuts a field and never sends a changed entry.
	longErr := capped.Write(t.Context(), []logsink.Record{longRecord()})

	longSkip, ok := errors.AsType[*logsink.SkipError](longErr)
	if !ok || !errors.Is(longErr, logsink.ErrSkipped) {
		t.Fatalf("write above the cap: got %v, want a skip error", longErr)
	}

	requireNoCanary(t, "syslog skip reason", longSkip.Reason)
	requireNoCanary(t, "syslog skip error", canaryErrText(longErr))

	pauseCanary(t)

	msgs = r.waitFor(t, 1)
	if len(msgs) != 1 {
		t.Fatalf("syslog messages = %d, want 1", len(msgs))
	}

	// A record that cannot fit is skipped with a fixed reason that echoes
	// neither the record nor any target detail.
	capErr = capped.Write(t.Context(), []logsink.Record{skipRecord()})

	skip, ok := errors.AsType[*logsink.SkipError](capErr)
	if !ok || !errors.Is(capErr, logsink.ErrSkipped) {
		t.Fatalf("write that cannot fit: got %v, want a skip error", capErr)
	}

	requireNoCanary(t, "syslog skip reason", skip.Reason)
	requireNoCanary(t, "syslog skip error", canaryErrText(capErr))
}

// canaryWebhook posts a benign batch with the credential canary as the sink
// secret, then posts a benign batch to a receiver that refuses with a body
// that holds every canary. Neither the shipped body, the signature headers,
// nor the refusal error hold any canary in plain text. The webhook
// configuration holds no other secret: it has no auth header or token field
// besides the secret.
func canaryWebhook(t *testing.T) {
	t.Helper()

	r := newCanaryReceiver(t, http.StatusNoContent)

	s, err := webhook.New(webhook.Config{
		URL:     r.srv.URL,
		Secret:  []byte(canaryCred),
		Client:  r.srv.Client(),
		Timeout: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close sink: %v", closeErr)
		}
	})

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	bodies, headers := r.received(t)
	if len(bodies) != 1 {
		t.Fatalf("received bodies = %d, want 1", len(bodies))
	}

	// The configuration holds the credential canary as the HMAC secret, but
	// the shipped body holds only the benign record and the signature
	// headers hold only the timestamp and the hex signature.
	requireNoCanary(t, "shipped webhook body", string(bodies[0]))
	requireNoCanary(t, "webhook headers", headers)

	if !bytes.Contains(bodies[0], []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped webhook body lacks the benign record: %q", bodies[0])
	}

	// The refusing receiver answers with a body that holds every canary.
	// The refusal error keeps the status only, never the answer body and
	// never the request body.
	refusing := newCanaryReceiver(t, http.StatusServiceUnavailable)

	bad, badErr := webhook.New(webhook.Config{
		URL:     refusing.srv.URL,
		Secret:  []byte(canaryCred),
		Client:  refusing.srv.Client(),
		Timeout: 0,
	})
	if badErr != nil {
		t.Fatalf("new refusing sink: %v", badErr)
	}

	t.Cleanup(func() {
		if closeErr := bad.Close(); closeErr != nil {
			t.Errorf("close refusing sink: %v", closeErr)
		}
	})

	writeErr := bad.Write(t.Context(), []logsink.Record{benignRecord()})
	if !errors.Is(writeErr, webhook.ErrRefused) {
		t.Fatalf("write to a refusing receiver: got %v, want ErrRefused", writeErr)
	}

	requireNoCanary(t, "webhook refusal error", canaryErrText(writeErr))
}

// canaryObjectStore writes a benign record through a sink backed by an S3
// store that signs with the credential canary as its secret access key,
// then writes a benign record to a store whose endpoint refuses with an
// error body that holds every canary. The shipped objects, the signature
// headers and the refusal error hold no canary. The access key ID is an
// ordinary value because it travels in the signature header by design. The
// session token stays empty.
func canaryObjectStore(t *testing.T) {
	t.Helper()

	fake := newCanaryS3(t, http.StatusOK)
	s := canaryS3Sink(t, fake.srv)

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	bodies, headers := fake.received(t)
	if len(bodies) != 1 {
		t.Fatalf("shipped objects = %d, want 1", len(bodies))
	}

	// The store signs with the credential canary, but each shipped object
	// holds only the benign record and the signature header holds no
	// secret. The token header stays empty.
	lines := gunzipLines(t, bodies[0])
	if len(lines) != 1 {
		t.Fatalf("shipped object lines = %d, want 1", len(lines))
	}

	requireNoCanary(t, "shipped object lines", string(lines[0]))
	requireNoCanary(t, "object store signature", headers.auth)

	if headers.token != "" {
		t.Fatalf("session token header = %q, want empty", headers.token)
	}

	if !bytes.Contains(lines[0], []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped object lines lack the benign record: %q", lines[0])
	}

	// The refusing endpoint answers with an error body that holds every
	// canary. The failure error keeps the status and code only, never the
	// answer body and never the record.
	refusing := newCanaryS3(t, http.StatusForbidden)
	bad := canaryS3Sink(t, refusing.srv)

	writeErr := bad.Write(t.Context(), []logsink.Record{benignRecord()})
	if writeErr == nil {
		t.Fatal("write to a refusing object store: got nil error")
	}

	requireNoCanary(t, "object store failure error", writeErr.Error())
}

// canaryS3Sink returns an object store sink backed by an S3 store at srv
// that signs with the credential canary as its secret access key. The
// access key ID is an ordinary value because it travels in the signature
// header by design. The session token stays empty.
func canaryS3Sink(t *testing.T, srv *httptest.Server) *objectstore.Sink {
	t.Helper()

	signer := &s3.Signer{
		Bucket:   "canary-bucket",
		Region:   "us-east-1",
		Endpoint: srv.URL,
		Credentials: s3.Credentials{
			AccessKeyID:     "test-access-key",
			SecretAccessKey: canaryCred,
			SessionToken:    "",
		},
		Now: nil,
	}

	store, err := s3.New(signer, "audit", srv.Client())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	s, newErr := objectstore.New(store, "audit")
	if newErr != nil {
		t.Fatalf("new: %v", newErr)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close sink: %v", closeErr)
		}
	})

	return s
}

// canarySlogsink logs a benign record through the handler, then logs a
// benign record through a handler whose sink fails with an error that holds
// every canary. The handler drops the records and answers nil, so neither
// the delivered records nor the returned errors hold any canary. The
// handler ships every attribute and drops none by key, so the body canary
// has no placement in this case: no rule drops an attribute or a context
// value, and the records stay benign.
func canarySlogsink(t *testing.T) {
	t.Helper()

	mem := &canaryMemSink{fail: nil, recs: nil, mu: sync.Mutex{}}

	var drops atomic.Int64

	h := slogsink.New(mem, slogsink.Options{
		Level:         slog.LevelInfo,
		OnDrop:        func(n int) { drops.Add(int64(n)) },
		QueueSize:     0,
		BatchSize:     0,
		FlushInterval: 5 * time.Millisecond,
		WriteTimeout:  time.Second,
	})

	slog.New(h).Info("audit entry", slog.String("path", "/api/stacks"), slog.String("scope", "acme"))

	if err := h.Close(t.Context()); err != nil {
		t.Fatalf("close handler: %v", err)
	}

	recs := mem.records()
	if len(recs) != 1 {
		t.Fatalf("delivered records = %d, want 1", len(recs))
	}

	line, err := logsink.MarshalJSON(recs[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	requireNoCanary(t, "shipped slog record", string(line))

	if drops.Load() != 0 {
		t.Fatalf("drops = %d, want 0", drops.Load())
	}

	// The inner sink fails with an error that holds every canary. The
	// handler is best effort: it drops the batch, counts it, and Handle
	// still answers nil, so the canaries reach neither a shipped record nor
	// a returned error. The handler emits no log lines itself.
	failing := &canaryMemSink{fail: errCanaryTarget, recs: nil, mu: sync.Mutex{}}

	var failedDrops atomic.Int64

	fh := slogsink.New(failing, slogsink.Options{
		Level:         slog.LevelInfo,
		OnDrop:        func(n int) { failedDrops.Add(int64(n)) },
		QueueSize:     0,
		BatchSize:     0,
		FlushInterval: 5 * time.Millisecond,
		WriteTimeout:  time.Second,
	})

	logger := slog.New(fh)
	logger.Info("audit entry", slog.String("path", "/api/stacks"), slog.String("scope", "acme"))

	closeErr := fh.Close(t.Context())
	requireNoCanary(t, "slog close error", canaryErrText(closeErr))

	if closeErr != nil {
		t.Fatalf("close failing handler: %v", closeErr)
	}

	if got := failing.records(); len(got) != 0 {
		t.Fatalf("failing sink delivered %d records, want 0", len(got))
	}

	if failedDrops.Load() == 0 {
		t.Fatal("drops of a failing sink = 0, want the dropped batch")
	}
}

// canaryMemSink keeps every record it accepts, or fails with fail.
type canaryMemSink struct {
	fail error
	recs []logsink.Record
	mu   sync.Mutex
}

func (s *canaryMemSink) Write(_ context.Context, records []logsink.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail != nil {
		return s.fail
	}

	s.recs = append(s.recs, records...)

	return nil
}

func (s *canaryMemSink) Close() error { return nil }

func (s *canaryMemSink) records() []logsink.Record {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]logsink.Record(nil), s.recs...)
}

// gunzipLines answers the lines of one gzip object body.
func gunzipLines(t *testing.T, data []byte) [][]byte {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}

	body, err := io.ReadAll(zr)

	if closeErr := zr.Close(); closeErr != nil {
		t.Fatalf("close gzip reader: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}

	var out [][]byte

	for line := range bytes.SplitSeq(bytes.TrimSuffix(body, []byte("\n")), []byte("\n")) {
		out = append(out, line)
	}

	return out
}

// canaryS3Headers holds the signature headers and the token headers that
// one fake S3 endpoint saw.
type canaryS3Headers struct {
	auth  string
	token string
}

// canaryS3 is a local S3 endpoint that keeps the bodies it receives and the
// signature headers of each request.
type canaryS3 struct {
	srv    *httptest.Server
	bodies [][]byte
	auth   []string
	tokens []string
	mu     sync.Mutex
	status int
}

// newCanaryS3 starts a local S3 endpoint that answers status. A refusal
// carries an error body that holds every canary, so the test proves the
// failure error never echoes it. Only the code travels into the error.
func newCanaryS3(t *testing.T, status int) *canaryS3 {
	t.Helper()

	f := &canaryS3{srv: nil, bodies: nil, auth: nil, tokens: nil, mu: sync.Mutex{}, status: status}

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.auth = append(f.auth, req.Header.Get("Authorization"))
		f.tokens = append(f.tokens, req.Header.Get("X-Amz-Security-Token"))
		f.mu.Unlock()

		if status != http.StatusOK {
			w.WriteHeader(status)

			refused := `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>refused: ` +
				canaryCred + " " + canaryPath + " " + canaryBody + `</Message></Error>`
			_, _ = w.Write([]byte(refused)) //nolint:errcheck // The status decides the result.
		} else {
			w.WriteHeader(status)
		}
	}))

	t.Cleanup(f.srv.Close)

	return f
}

// received answers the bodies, the signature headers and the token headers
// seen so far.
func (f *canaryS3) received(t *testing.T) ([][]byte, canaryS3Headers) {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	return append([][]byte(nil), f.bodies...), canaryS3Headers{
		auth:  strings.Join(f.auth, "\n"),
		token: strings.Join(f.tokens, "\n"),
	}
}

// canaryReceiver is a local HTTPS server that keeps the bodies it receives
// and the signature headers of each request.
type canaryReceiver struct {
	srv     *httptest.Server
	bodies  [][]byte
	headers []string
	mu      sync.Mutex
	status  int
}

// newCanaryReceiver starts a local HTTPS server that answers status. A
// refusal carries a body that holds every canary, so the test proves the
// refusal error never echoes it.
func newCanaryReceiver(t *testing.T, status int) *canaryReceiver {
	t.Helper()

	r := &canaryReceiver{srv: nil, bodies: nil, headers: nil, mu: sync.Mutex{}, status: status}

	r.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)

			return
		}

		r.mu.Lock()
		r.bodies = append(r.bodies, body)
		r.headers = append(r.headers,
			req.Header.Get(webhook.HeaderTimestamp)+"\n"+req.Header.Get(webhook.HeaderSignature))
		r.mu.Unlock()

		if status < http.StatusOK || status >= http.StatusMultipleChoices {
			w.WriteHeader(status)

			refused := "refused: " + canaryCred + " " + canaryPath + " " + canaryBody
			_, _ = w.Write([]byte(refused)) //nolint:errcheck // The status decides the result.
		} else {
			w.WriteHeader(status)
		}
	}))

	t.Cleanup(r.srv.Close)

	return r
}

// received answers the bodies and headers received so far.
func (r *canaryReceiver) received(t *testing.T) ([][]byte, string) {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	return append([][]byte(nil), r.bodies...), strings.Join(r.headers, "\n")
}

// canaryUDPReceiver collects the datagrams of a local UDP listener.
type canaryUDPReceiver struct {
	addr string
	msgs [][]byte
	mu   sync.Mutex
}

// listenCanaryUDP starts a UDP receiver on 127.0.0.1 that stops with the
// test. It calls no outside network.
func listenCanaryUDP(t *testing.T) *canaryUDPReceiver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	r := &canaryUDPReceiver{addr: pc.LocalAddr().String(), msgs: nil, mu: sync.Mutex{}}

	var wg sync.WaitGroup

	wg.Go(func() {
		buf := make([]byte, 64*1024)

		for {
			n, _, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}

			r.mu.Lock()
			r.msgs = append(r.msgs, append([]byte(nil), buf[:n]...))
			r.mu.Unlock()
		}
	})

	t.Cleanup(func() {
		if err := pc.Close(); err != nil {
			t.Errorf("close udp listener: %v", err)
		}

		wg.Wait()
	})

	return r
}

// pauseCanary waits a short time, or fails the test when it ends first.
func pauseCanary(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test ended while it waited for syslog messages")
	}
}

// waitFor waits until want messages arrived, then answers a copy of them.
func (r *canaryUDPReceiver) waitFor(t *testing.T, want int) [][]byte {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		r.mu.Lock()
		msgs := append([][]byte(nil), r.msgs...)
		r.mu.Unlock()

		if len(msgs) >= want || time.Now().After(deadline) {
			if len(msgs) < want {
				t.Fatalf("syslog messages = %d, want %d", len(msgs), want)
			}

			return msgs
		}

		pauseCanary(t)
	}
}

// canaryStreamReceiver collects the octet-counted messages of a local TCP
// listener.
type canaryStreamReceiver struct {
	addr string
	msgs [][]byte
	mu   sync.Mutex
}

// listenCanaryStream starts a TCP receiver on 127.0.0.1 that stops with the
// test. It calls no outside network.
func listenCanaryStream(t *testing.T) *canaryStreamReceiver {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}

	r := &canaryStreamReceiver{addr: ln.Addr().String(), msgs: nil, mu: sync.Mutex{}}

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}

			wg.Go(func() { readCanaryFrames(conn, r) })
		}
	})

	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close tcp listener: %v", err)
		}

		wg.Wait()
	})

	return r
}

// readCanaryFrames reads octet-counted frames from conn until it closes.
func readCanaryFrames(conn net.Conn, r *canaryStreamReceiver) {
	defer func() {
		_ = conn.Close() //nolint:errcheck // The test ends with the listener.
	}()

	br := bufio.NewReader(conn)

	for {
		lenText, err := br.ReadString(' ')
		if err != nil {
			return
		}

		n, convErr := strconv.Atoi(lenText[:len(lenText)-1])
		if convErr != nil || n <= 0 {
			return
		}

		msg := make([]byte, n)
		if _, err := io.ReadFull(br, msg); err != nil {
			return
		}

		r.mu.Lock()
		r.msgs = append(r.msgs, msg)
		r.mu.Unlock()
	}
}

// waitFor waits until want messages arrived, then answers a copy of them.
func (r *canaryStreamReceiver) waitFor(t *testing.T, want int) [][]byte {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		r.mu.Lock()
		msgs := append([][]byte(nil), r.msgs...)
		r.mu.Unlock()

		if len(msgs) >= want || time.Now().After(deadline) {
			if len(msgs) < want {
				t.Fatalf("syslog messages = %d, want %d", len(msgs), want)
			}

			return msgs
		}

		pauseCanary(t)
	}
}

// canaryBodyOf returns the MSG part of an RFC 5424 message: everything after
// the seventh space.
func canaryBodyOf(t *testing.T, msg []byte) []byte {
	t.Helper()

	parts := bytes.SplitN(msg, []byte(" "), 8)
	if len(parts) != 8 {
		t.Fatalf("not an RFC 5424 message: %q", msg)
	}

	return parts[7]
}
