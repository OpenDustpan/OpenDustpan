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

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/objectstore"
	"opendustpan.org/opendustpan/pkg/logsink/slogsink"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// Canary values for the never-shipped test. They are test values only and
// look like no real token format.
//
//nolint:gosec // Canary values for the never-shipped test, not credentials.
const (
	canaryCred = "canary-cred-7f3e9a1b"
	canaryPath = "/canary/path/do-not-log"
	canaryBody = "canary-body-5d2c"
)

// canaries holds every canary value the test checks for.
var canaries = []string{canaryCred, canaryPath, canaryBody} //nolint:gochecknoglobals // Shared by the canary subtests.

// errCanaryTarget reports a failed target in the canary test. One test
// double fails with it, so the text that a careless adapter echoes would
// hold every canary value.
var errCanaryTarget = errors.New(
	"target down: " + canaryCred + " " + canaryPath + " " + canaryBody,
)

// errCanaryStore reports a failed object store in the canary test.
var errCanaryStore = errors.New("object store unavailable")

// requireNoCanary fails when haystack holds any canary value. where names
// the output under check, so a failure points at the leaking channel.
func requireNoCanary(t *testing.T, where, haystack string) {
	t.Helper()

	for _, c := range canaries {
		if strings.Contains(haystack, c) {
			t.Fatalf("%s holds canary value %q", where, c)
		}
	}
}

// benignRecord returns a record with no canary value in it.
func benignRecord() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 1),
		},
	}
}

// canaryRecord returns a record whose message and attribute hold the body
// and path canaries. The message carries padding past the smallest syslog
// cap, so the capped syslog sinks skip it. A Write that fails must not echo
// them in its error, and a Write that the test drops must not deliver them.
func canaryRecord() logsink.Record {
	return logsink.Record{
		Time:    time.Date(2026, time.October, 15, 12, 0, 1, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: canaryBody + " " + strings.Repeat("x", 600),
		Attrs: []logsink.Attr{
			logsink.String("path", canaryPath),
		},
	}
}

// TestCanaryNoAdapterLeaksSecrets proves no adapter ships a secret it holds
// only in its configuration, in a target failure, or in a dropped request
// body. Each row uses the test double of one adapter: a temp dir, a local
// UDP or TCP listener, a local HTTPS server, or the directory blob store.
// No row calls any outside network.
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

// canaryFile writes a benign record to a file sink whose directory name
// holds the credential canary, then drops a canary record on a closed sink.
// Neither the shipped lines nor the returned errors hold any canary.
// Adapters return errors instead of logging, so no log line can hold a
// canary here.
func canaryFile(t *testing.T) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), canaryCred)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create dir: %v", err)
	}

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

	data, readErr := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if readErr != nil {
		t.Fatalf("read: %v", readErr)
	}

	// The directory name holds the credential canary, but the shipped lines
	// hold only the benign record.
	requireNoCanary(t, "shipped file lines", string(data))

	if !bytes.Contains(data, []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped file lines lack the benign record: %q", data)
	}

	closed, openErr := file.Open(filepath.Join(t.TempDir(), "closed.jsonl"))
	if openErr != nil {
		t.Fatalf("open closed sink: %v", openErr)
	}

	if closeErr := closed.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}

	err = closed.Write(t.Context(), []logsink.Record{canaryRecord()})
	if !errors.Is(err, logsink.ErrClosed) {
		t.Fatalf("write to a closed file sink: got %v, want ErrClosed", err)
	}

	requireNoCanary(t, "closed file error", err.Error())
}

// canarySyslogUDP writes a benign record to a local UDP receiver, then
// drops a canary record above the message cap. The delivered message and
// the skip error hold no canary.
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

	// The record above the cap holds the body and path canaries. The sink
	// skips it with a fixed reason that echoes neither the record nor any
	// target detail.
	capErr = capped.Write(t.Context(), []logsink.Record{canaryRecord()})

	skip, ok := errors.AsType[*logsink.SkipError](capErr)
	if !ok || !errors.Is(capErr, logsink.ErrSkipped) {
		t.Fatalf("write above the cap: got %v, want a skip error", capErr)
	}

	requireNoCanary(t, "syslog skip reason", skip.Reason)
	requireNoCanary(t, "syslog skip error", capErr.Error())
}

// canarySyslogTCP writes a benign record to a local TCP receiver, then
// drops a canary record above the message cap. The delivered message and
// the skip error hold no canary.
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

	capErr = capped.Write(t.Context(), []logsink.Record{canaryRecord()})

	skip, ok := errors.AsType[*logsink.SkipError](capErr)
	if !ok || !errors.Is(capErr, logsink.ErrSkipped) {
		t.Fatalf("write above the cap: got %v, want a skip error", capErr)
	}

	requireNoCanary(t, "syslog skip reason", skip.Reason)
	requireNoCanary(t, "syslog skip error", capErr.Error())
}

// canaryWebhook posts a benign batch with the credential canary as the sink
// secret, then posts to a receiver that refuses with a body that holds every
// canary. Neither the shipped body, the signature headers, nor the refusal
// error hold any canary in plain text.
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

	requireNoCanary(t, "webhook refusal error", writeErr.Error())

	// A canary record that a refusing receiver refuses must not appear in
	// the returned error either: the error keeps the status only.
	writeErr = bad.Write(t.Context(), []logsink.Record{canaryRecord()})
	if !errors.Is(writeErr, webhook.ErrRefused) {
		t.Fatalf("write of a canary record to a refusing receiver: got %v, want ErrRefused", writeErr)
	}

	requireNoCanary(t, "webhook canary refusal error", writeErr.Error())
}

// canaryObjectStore writes a benign record to the directory blob store, then
// drops a canary record on a store whose Put fails with a generic error.
// The shipped objects and the failure error hold no canary: the error never
// echoes the record.
func canaryObjectStore(t *testing.T) {
	t.Helper()

	store, err := fs.Open(t.TempDir())
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

	if writeErr := s.Write(t.Context(), []logsink.Record{benignRecord()}); writeErr != nil {
		t.Fatalf("write: %v", writeErr)
	}

	lines := readCanaryObjects(t, store, "audit")
	if len(lines) != 1 {
		t.Fatalf("shipped object lines = %d, want 1", len(lines))
	}

	requireNoCanary(t, "shipped object lines", string(lines[0]))

	if !bytes.Contains(lines[0], []byte(`"msg":"audit entry"`)) {
		t.Fatalf("shipped object lines lack the benign record: %q", lines[0])
	}

	failing := &canaryBlobStore{err: errCanaryStore}

	bad, badErr := objectstore.New(failing, "audit")
	if badErr != nil {
		t.Fatalf("new failing sink: %v", badErr)
	}

	t.Cleanup(func() {
		if closeErr := bad.Close(); closeErr != nil {
			t.Errorf("close failing sink: %v", closeErr)
		}
	})

	// The record holds the body and path canaries, but the failure error
	// reports the store failure only, never the record.
	writeErr := bad.Write(t.Context(), []logsink.Record{canaryRecord()})
	if writeErr == nil {
		t.Fatal("write to a failing object store: got nil error")
	}

	requireNoCanary(t, "object store failure error", writeErr.Error())
}

// canarySlogsink logs a benign record through the handler, then logs
// through a handler whose sink fails with an error that holds every canary.
// The handler drops the records and answers nil, so neither the delivered
// records nor the returned errors hold any canary.
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

	slog.New(h).Info("audit entry", slog.String("scope", "acme"))

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
	logger.Info(canaryBody, slog.String("path", canaryPath))

	if err := fh.Close(t.Context()); err != nil {
		t.Fatalf("close failing handler: %v", err)
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

// canaryBlobStore fails every Put with err. Every other method answers the
// zero value, so a failed Write ships nothing.
type canaryBlobStore struct {
	err error
}

func (s *canaryBlobStore) Put(_ context.Context, _ blob.Key, _ io.Reader) error {
	return s.err
}

func (s *canaryBlobStore) Get(_ context.Context, _ blob.Key) (io.ReadCloser, error) {
	return nil, blob.ErrNotFound
}

func (s *canaryBlobStore) Delete(_ context.Context, _ blob.Key) error {
	return nil
}

func (s *canaryBlobStore) List(_ context.Context, _ string) ([]blob.Object, error) {
	return nil, nil
}

func (s *canaryBlobStore) Ping(_ context.Context) error {
	return nil
}

// readCanaryObjects answers the lines of every object under prefix.
func readCanaryObjects(t *testing.T, store blob.Store, prefix string) [][]byte {
	t.Helper()

	objs, err := store.List(t.Context(), prefix)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var out [][]byte

	for _, o := range objs {
		rc, err := store.Get(t.Context(), o.Key)
		if err != nil {
			t.Fatalf("get %s: %v", o.Key, err)
		}

		zr, err := gzip.NewReader(rc)
		if err != nil {
			_ = rc.Close() //nolint:errcheck // The gzip error decides the test result.

			t.Fatalf("gzip %s: %v", o.Key, err)
		}

		data, err := io.ReadAll(zr)

		if closeErr := rc.Close(); closeErr != nil {
			t.Fatalf("close %s: %v", o.Key, closeErr)
		}

		if err != nil {
			t.Fatalf("read %s: %v", o.Key, err)
		}

		for line := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
			out = append(out, line)
		}
	}

	return out
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
