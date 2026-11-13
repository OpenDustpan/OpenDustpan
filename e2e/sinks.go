//go:build e2e

package e2e

import (
	"bufio"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/otlp"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// The audit sink receivers of the suite. They run in the test process and
// speak the wire formats of the audit sinks the server binary ships to:
// syslog over TLS, a signed webhook and OTLP over HTTP. Each receiver decodes
// with its own small structures and never with the code of the sink, so a
// test checks the bytes on the wire. The server binary never links them.

const (
	// receiverMaxBytes caps one syslog frame and one request body that a
	// receiver reads, so a wrong peer cannot make the test process grow.
	receiverMaxBytes = 16 << 20
	// webhookMaxSkew is the age at which the webhook receiver refuses a
	// signed request, in both directions.
	webhookMaxSkew = 5 * time.Minute
	// secretBytes is the size of a random secret of the receivers.
	secretBytes = 32
	// syslogAuditFacility is the facility the server uses for the audit log.
	syslogAuditFacility = 13
	// syslogHeaderFields counts the space-separated fields before the
	// message of an RFC 5424 line as the sink writes it: PRI and version,
	// time, host name, app name, process ID, MSGID and STRUCTURED-DATA.
	syslogHeaderFields = 7
	// otlpHeaderName is the header the OTLP receiver requires.
	otlpHeaderName = "X-E2e-Audit-Key"
	// webhookTimestampHeader and webhookSignatureHeader are the headers of a
	// signed webhook request.
	webhookTimestampHeader = "Dustpan-Timestamp"
	webhookSignatureHeader = "Dustpan-Signature"
)

// AuditSinkSettings holds the audit sinks of a server binary: the --audit-sink
// items, and the environment variables that hold their secrets. No flag takes
// a secret, because flags show in process lists.
type AuditSinkSettings struct {
	// Items are the values of --audit-sink, one for each sink.
	Items []string
	// Env holds NAME=value pairs for the server process.
	Env []string
}

// args returns the --audit-sink flags. It answers nothing for nil.
func (s *AuditSinkSettings) args() []string {
	if s == nil {
		return nil
	}

	var out []string

	for _, item := range s.Items {
		out = append(out, "--audit-sink", item)
	}

	return out
}

// env returns the variables of the sinks. It answers nothing for nil.
func (s *AuditSinkSettings) env() []string {
	if s == nil {
		return nil
	}

	return append([]string(nil), s.Env...)
}

// SetAuditSinks sets the audit sinks that the next StartServer passes to the
// server binary. It needs E2E_SERVER=binary.
func (e *Env) SetAuditSinks(tb testing.TB, settings AuditSinkSettings) {
	tb.Helper()

	if e.binPath == "" {
		tb.Fatalf("SetAuditSinks needs E2E_SERVER=binary")
	}

	e.backendCfg.auditSinks = &settings
}

// ReceivedEntry is one audit entry as a receiver read it from the wire.
type ReceivedEntry struct {
	// Scope is the namespace of the entry, or the scope of the server.
	Scope string
	// Action, Object, Kind and Hash repeat the fields of the entry that the
	// receiver compares with the export.
	Action string
	Object string
	Kind   string
	Hash   string
	// Sequence is the position of the entry in its scope.
	Sequence int64
}

// entryLog holds what one receiver read.
type entryLog struct {
	raw       strings.Builder
	entries   []ReceivedEntry
	malformed []string
	refused   int
	mu        sync.Mutex
}

// newEntryLog returns an empty log.
func newEntryLog() *entryLog {
	return &entryLog{raw: strings.Builder{}, entries: nil, malformed: nil, refused: 0, mu: sync.Mutex{}}
}

// Entries returns a copy of the entries that the receiver read, oldest first.
func (l *entryLog) Entries() []ReceivedEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]ReceivedEntry(nil), l.entries...)
}

// Raw returns every byte of text that the receiver accepted, so a test can
// search it for a secret.
func (l *entryLog) Raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.raw.String()
}

// Refused returns how many requests the receiver refused.
func (l *entryLog) Refused() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.refused
}

// Malformed returns the reasons of the messages that broke the wire format.
func (l *entryLog) Malformed() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.malformed...)
}

// add keeps entries and the text they came in.
func (l *entryLog) add(wire string, entries []ReceivedEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.raw.WriteString(wire)
	l.raw.WriteByte('\n')

	l.entries = append(l.entries, entries...)
}

// reject counts one request that the receiver refused.
func (l *entryLog) reject() {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.refused++
}

// malformedf keeps the reason of one message that breaks the wire format.
func (l *entryLog) malformedf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.malformed = append(l.malformed, fmt.Sprintf(format, args...))
}

// wireEntry decodes the JSON object of one audit record. It holds the fields
// that a receiver compares, and no more.
type wireEntry struct {
	Scope    *string `json:"scope"`
	Sequence *int64  `json:"sequence"`
	Action   string  `json:"action"`
	Object   string  `json:"object"`
	Kind     string  `json:"kind"`
	Hash     string  `json:"hash"`
}

// received maps a decoded record to a ReceivedEntry. It fails when the record
// has no scope or no sequence.
func (w wireEntry) received() (ReceivedEntry, error) {
	if w.Scope == nil || *w.Scope == "" || w.Sequence == nil {
		return ReceivedEntry{}, errNoScopeOrSequence
	}

	return ReceivedEntry{
		Scope:    *w.Scope,
		Action:   w.Action,
		Object:   w.Object,
		Kind:     w.Kind,
		Hash:     w.Hash,
		Sequence: *w.Sequence,
	}, nil
}

// errNoScopeOrSequence reports a record that names no scope or no sequence.
var errNoScopeOrSequence = errors.New("record has no scope or no sequence")

// AuditReceivers groups the three receivers and the secrets the server holds
// for them.
type AuditReceivers struct {
	Syslog  *SyslogReceiver
	Webhook *WebhookReceiver
	OTLP    *OTLPReceiver
	// WebhookSecret keys the signature of the webhook sink.
	WebhookSecret string
	// OTLPKey is the value of the header the OTLP sink sends.
	OTLPKey string
	// CAFile is a PEM file with the certificate of the receivers.
	CAFile string
}

// NewAuditReceivers starts the three receivers on loopback ports, with one
// certificate of the suite. It stops them when the test ends.
func NewAuditReceivers(tb testing.TB) *AuditReceivers {
	tb.Helper()

	files := writeBackendCert(tb, tb.TempDir())

	cert, err := tls.LoadX509KeyPair(files.cert, files.key)
	if err != nil {
		tb.Fatalf("load receiver certificate: %v", err)
	}

	secret := randomHex(tb)
	key := randomHex(tb)

	return &AuditReceivers{
		Syslog:        newSyslogReceiver(tb, cert),
		Webhook:       newWebhookReceiver(tb, cert, []byte(secret)),
		OTLP:          newOTLPReceiver(tb, cert, key),
		WebhookSecret: secret,
		OTLPKey:       key,
		CAFile:        files.cert,
	}
}

// Settings returns the audit sinks that send to the receivers. The secrets
// travel in the environment of the server only. SSL_CERT_FILE makes the
// server trust the certificate of the receivers for the webhook and OTLP
// sinks, which have no setting for a CA file.
func (r *AuditReceivers) Settings() AuditSinkSettings {
	return AuditSinkSettings{
		Items: []string{
			"syslog=syslog+tls://" + r.Syslog.Address() + "?ca-file=" + r.CAFile,
			"webhook=webhook:" + r.Webhook.URL(),
			"otlp=otlp:" + r.OTLP.URL(),
		},
		Env: []string{
			"DUSTPAN_AUDIT_WEBHOOK_SECRET=" + r.WebhookSecret,
			"DUSTPAN_AUDIT_OTLP_HEADERS=" + strings.ToLower(otlpHeaderName) + "=" + r.OTLPKey,
			"SSL_CERT_FILE=" + r.CAFile,
		},
	}
}

// Secrets returns the secret values the server holds for the receivers. No
// receiver may read one in an entry.
func (r *AuditReceivers) Secrets() []string {
	return []string{r.WebhookSecret, r.OTLPKey}
}

// Sinks returns the audit sinks that send from the in-process server to the
// receivers. The webhook and OTLP clients allow the loopback for the test, as
// chat.NewLoopback does, and trust the certificate of the receivers. It
// closes the sinks when the test ends.
func (r *AuditReceivers) Sinks(tb testing.TB) []audit.ShipSink {
	tb.Helper()

	roots := r.certPool(tb)
	rules := egress.Rules{Allow: nil, LoopbackForTests: true}
	client := egresshttp.ClientWith(rules, egress.NoRedirect, egress.HTTPSOnly,
		egresshttp.Options{RootCAs: roots, Resolver: nil})

	tb.Cleanup(client.CloseIdleConnections)

	webhookSink, err := webhook.New(webhook.Config{
		Client: client, URL: r.Webhook.URL(), Secret: []byte(r.WebhookSecret), Timeout: 0,
	})
	if err != nil {
		tb.Fatalf("new webhook sink: %v", err)
	}

	tb.Cleanup(func() { closeSink(tb, "webhook", webhookSink) })

	otlpSink, err := otlp.New(otlp.Config{
		Client: client, Headers: map[string]string{strings.ToLower(otlpHeaderName): r.OTLPKey},
		URL: r.OTLP.URL(), ServiceName: "", Timeout: 0,
	})
	if err != nil {
		tb.Fatalf("new OTLP sink: %v", err)
	}

	tb.Cleanup(func() { closeSink(tb, "otlp", otlpSink) })

	syslogSink, err := syslog.New(syslog.Config{
		TLSConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		Network:   syslog.TLS,
		Address:   r.Syslog.Address(),
		Hostname:  "",
		AppName:   "",
		Facility:  syslog.FacilityLogAudit,
		Timeout:   0,

		MaxMessageBytes: 0,
	})
	if err != nil {
		tb.Fatalf("new syslog sink: %v", err)
	}

	tb.Cleanup(func() { closeSink(tb, "syslog", syslogSink) })

	return []audit.ShipSink{
		{Sink: syslogSink, Name: "syslog", Kind: "syslog", Interval: 0},
		{Sink: webhookSink, Name: "webhook", Kind: "webhook", Interval: 0},
		{Sink: otlpSink, Name: "otlp", Kind: "otlp", Interval: 0},
	}
}

// certPool returns the roots that trust the certificate of the receivers.
func (r *AuditReceivers) certPool(tb testing.TB) *x509.CertPool {
	tb.Helper()

	pemBytes, err := os.ReadFile(r.CAFile)
	if err != nil {
		tb.Fatalf("read the receiver CA file: %v", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		tb.Fatal("the receiver CA file holds no certificate")
	}

	return pool
}

// closeSink closes a sink of the suite and reports a failure.
func closeSink(tb testing.TB, name string, sink logsink.Sink) {
	tb.Helper()

	if err := sink.Close(); err != nil {
		tb.Errorf("close the %s sink: %v", name, err)
	}
}

// randomHex returns secretBytes random bytes as hex text.
func randomHex(tb testing.TB) string {
	tb.Helper()

	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		tb.Fatalf("random bytes: %v", err)
	}

	return hex.EncodeToString(raw)
}

// SyslogReceiver is a syslog server over TLS (RFC 5425). It reads
// octet-counted RFC 5424 messages.
type SyslogReceiver struct {
	*entryLog

	tb     testing.TB
	ln     net.Listener
	conns  map[net.Conn]struct{}
	wg     sync.WaitGroup
	cmu    sync.Mutex
	closed bool // guarded by cmu; set by close before it walks conns
}

// newSyslogReceiver listens on a loopback port and stops when the test ends.
func newSyslogReceiver(tb testing.TB, cert tls.Certificate) *SyslogReceiver {
	tb.Helper()

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		tb.Fatalf("listen for syslog: %v", err)
	}

	r := &SyslogReceiver{
		entryLog: newEntryLog(),
		tb:       tb,
		ln:       ln,
		conns:    map[net.Conn]struct{}{},
		wg:       sync.WaitGroup{},
		cmu:      sync.Mutex{},
		closed:   false,
	}

	r.wg.Go(r.accept)

	tb.Cleanup(r.close)

	return r
}

// Address returns host:port of the receiver.
func (r *SyslogReceiver) Address() string {
	return r.ln.Addr().String()
}

// accept serves connections until the listener closes.
func (r *SyslogReceiver) accept() {
	for {
		conn, err := r.ln.Accept()
		if err != nil {
			return
		}

		r.cmu.Lock()
		if r.closed {
			// close has already walked conns, so this connection would
			// never be closed by it. Close it here and stop.
			r.cmu.Unlock()
			closeLogged(r.tb, conn)

			return
		}

		r.conns[conn] = struct{}{}
		r.cmu.Unlock()

		r.wg.Go(func() { r.serve(conn) })
	}
}

// close stops the listener and every connection, and waits for the readers.
func (r *SyslogReceiver) close() {
	closeLogged(r.tb, r.ln)

	r.cmu.Lock()
	r.closed = true
	for conn := range r.conns {
		closeLogged(r.tb, conn)
	}
	r.cmu.Unlock()

	r.wg.Wait()
}

// serve reads the frames of one connection until it ends.
func (r *SyslogReceiver) serve(conn net.Conn) {
	defer closeLogged(r.tb, conn)

	reader := bufio.NewReader(conn)

	for {
		frame, err := readOctetFrame(reader)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				r.malformedf("frame: %v", err)
			}

			return
		}

		r.handle(frame)
	}
}

// errFrame reports a frame that breaks RFC 6587 octet counting.
var errFrame = errors.New("bad octet-counted frame")

// closeLogged closes c and logs an error other than a close of a closed
// connection.
func closeLogged(tb testing.TB, c io.Closer) {
	tb.Helper()

	if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		tb.Logf("close receiver connection: %v", err)
	}
}

// readOctetFrame reads "<length> <message>" and answers the message.
func readOctetFrame(reader *bufio.Reader) (string, error) {
	prefix, err := reader.ReadString(' ')
	if err != nil {
		return "", fmt.Errorf("read length: %w", err)
	}

	length, err := strconv.Atoi(strings.TrimSuffix(prefix, " "))
	if err != nil || length <= 0 || length > receiverMaxBytes {
		return "", fmt.Errorf("%w: length %q", errFrame, prefix)
	}

	msg := make([]byte, length)
	if _, err = io.ReadFull(reader, msg); err != nil {
		return "", fmt.Errorf("read message of %d bytes: %w", length, err)
	}

	return string(msg), nil
}

// handle checks the RFC 5424 header of one message and keeps its entry.
func (r *SyslogReceiver) handle(msg string) {
	fields := strings.SplitN(msg, " ", syslogHeaderFields+1)
	if len(fields) != syslogHeaderFields+1 {
		r.malformedf("header of %d fields, want %d", len(fields), syslogHeaderFields)

		return
	}

	pri, ok := strings.CutSuffix(strings.TrimPrefix(fields[0], "<"), ">1")
	if !ok {
		r.malformedf("first field %q, want <PRI>1", fields[0])

		return
	}

	code, err := strconv.Atoi(pri)
	if err != nil || code/8 != syslogAuditFacility {
		r.malformedf("priority %q, want facility %d", pri, syslogAuditFacility)

		return
	}

	if _, err = time.Parse(time.RFC3339, fields[1]); err != nil {
		r.malformedf("time %q: %v", fields[1], err)

		return
	}

	var wire wireEntry
	if err = json.Unmarshal([]byte(fields[syslogHeaderFields]), &wire); err != nil {
		r.malformedf("message is not JSON: %v", err)

		return
	}

	entry, err := wire.received()
	if err != nil {
		r.malformedf("%v", err)

		return
	}

	r.add(msg, []ReceivedEntry{entry})
}

// WebhookReceiver is an HTTPS endpoint that checks the HMAC of each request
// before it keeps the entries.
type WebhookReceiver struct {
	*entryLog

	srv    *httptest.Server
	secret []byte
}

// newWebhookReceiver starts the endpoint and stops it when the test ends.
func newWebhookReceiver(tb testing.TB, cert tls.Certificate, secret []byte) *WebhookReceiver {
	tb.Helper()

	r := &WebhookReceiver{entryLog: newEntryLog(), srv: nil, secret: secret}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook", r.serve)

	r.srv = startTLSServer(tb, cert, mux)

	return r
}

// URL returns the address that the webhook sink posts to.
func (r *WebhookReceiver) URL() string {
	return r.srv.URL + "/hook"
}

// Sign returns the signature header value for a request that a test sends to
// the receiver itself.
func (r *WebhookReceiver) Sign(timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, r.secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)

	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// serve answers one request: 401 for a bad or old signature, 400 for a body
// that breaks the format, else 204.
func (r *WebhookReceiver) serve(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, receiverMaxBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)

		return
	}

	if !r.signatureValid(req.Header.Get(webhookTimestampHeader), req.Header.Get(webhookSignatureHeader), body) {
		r.reject()
		http.Error(w, "bad signature", http.StatusUnauthorized)

		return
	}

	var wires []wireEntry
	if err = json.Unmarshal(body, &wires); err != nil {
		r.malformedf("body is not a JSON array: %v", err)
		http.Error(w, "bad body", http.StatusBadRequest)

		return
	}

	entries := make([]ReceivedEntry, 0, len(wires))

	for i := range wires {
		entry, recErr := wires[i].received()
		if recErr != nil {
			r.malformedf("record %d: %v", i, recErr)
			http.Error(w, "bad record", http.StatusBadRequest)

			return
		}

		entries = append(entries, entry)
	}

	r.add(string(body), entries)
	w.WriteHeader(http.StatusNoContent)
}

// signatureValid computes the HMAC-SHA256 of the timestamp, a ".", and the
// body, as the sink documents it, and compares it in constant time. It
// refuses a timestamp more than webhookMaxSkew from now.
func (r *WebhookReceiver) signatureValid(timestamp, signature string, body []byte) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}

	if skew := time.Since(time.Unix(seconds, 0)); skew > webhookMaxSkew || skew < -webhookMaxSkew {
		return false
	}

	return hmac.Equal([]byte(r.Sign(timestamp, body)), []byte(signature))
}

// OTLPReceiver is an OTLP/HTTP collector for logs: POST /v1/logs with the
// JSON encoding.
type OTLPReceiver struct {
	*entryLog

	srv *httptest.Server
	key string
}

// newOTLPReceiver starts the collector and stops it when the test ends.
func newOTLPReceiver(tb testing.TB, cert tls.Certificate, key string) *OTLPReceiver {
	tb.Helper()

	r := &OTLPReceiver{entryLog: newEntryLog(), srv: nil, key: key}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/logs", r.serve)

	r.srv = startTLSServer(tb, cert, mux)

	return r
}

// URL returns the base address of the collector. The sink adds /v1/logs.
func (r *OTLPReceiver) URL() string {
	return r.srv.URL
}

// otlpValue is the AnyValue of an attribute. The JSON mapping of OTLP writes
// an int64 as a string.
type otlpValue struct {
	StringValue *string `json:"stringValue"`
	IntValue    *string `json:"intValue"`
}

// otlpAttribute is one key and value pair.
type otlpAttribute struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

// otlpLogRecord is one LogRecord.
type otlpLogRecord struct {
	TimeUnixNano string          `json:"timeUnixNano"`
	SeverityText string          `json:"severityText"`
	Attributes   []otlpAttribute `json:"attributes"`
}

// otlpScopeLogs holds the records of one instrumentation scope.
type otlpScopeLogs struct {
	LogRecords []otlpLogRecord `json:"logRecords"`
}

// otlpResourceLogs holds the scopes of one resource.
type otlpResourceLogs struct {
	ScopeLogs []otlpScopeLogs `json:"scopeLogs"`
}

// otlpRequest is the part of an ExportLogsServiceRequest that the collector
// reads.
type otlpRequest struct {
	ResourceLogs []otlpResourceLogs `json:"resourceLogs"`
}

// serve answers one export: 401 without the header key, 415 for a body that
// is not JSON, 400 for a record that breaks the format, else 200.
func (r *OTLPReceiver) serve(w http.ResponseWriter, req *http.Request) {
	if !hmac.Equal([]byte(req.Header.Get(otlpHeaderName)), []byte(r.key)) {
		r.reject()
		http.Error(w, "bad key", http.StatusUnauthorized)

		return
	}

	if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "want application/json", http.StatusUnsupportedMediaType)

		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, receiverMaxBytes))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)

		return
	}

	var export otlpRequest
	if err = json.Unmarshal(body, &export); err != nil {
		r.malformedf("body is not an export request: %v", err)
		http.Error(w, "bad body", http.StatusBadRequest)

		return
	}

	entries, err := r.entriesOf(&export)
	if err != nil {
		r.malformedf("%v", err)
		http.Error(w, "bad record", http.StatusBadRequest)

		return
	}

	r.add(string(body), entries)
	w.Header().Set("Content-Type", "application/json")

	if _, err = io.WriteString(w, "{}"); err != nil {
		r.malformedf("write the answer: %v", err)
	}
}

// errOTLPRecord reports an OTLP record that breaks the wire format.
var errOTLPRecord = errors.New("bad OTLP record")

// entriesOf maps each log record of export to an entry.
func (r *OTLPReceiver) entriesOf(export *otlpRequest) ([]ReceivedEntry, error) {
	var out []ReceivedEntry

	for _, resource := range export.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			for i := range scope.LogRecords {
				entry, err := otlpEntry(&scope.LogRecords[i])
				if err != nil {
					return nil, err
				}

				out = append(out, entry)
			}
		}
	}

	return out, nil
}

// otlpEntry checks the time and the severity of one record and reads its
// attributes. A string attribute is a stringValue, and the sequence is an
// intValue, which the JSON mapping writes as a string.
func otlpEntry(rec *otlpLogRecord) (ReceivedEntry, error) {
	if _, err := strconv.ParseInt(rec.TimeUnixNano, 10, 64); err != nil {
		return ReceivedEntry{}, fmt.Errorf("%w: timeUnixNano %q", errOTLPRecord, rec.TimeUnixNano)
	}

	if rec.SeverityText != "INFO" {
		return ReceivedEntry{}, fmt.Errorf("%w: severity %q", errOTLPRecord, rec.SeverityText)
	}

	texts := map[string]string{}
	ints := map[string]string{}

	for _, a := range rec.Attributes {
		if a.Value.StringValue != nil {
			texts[a.Key] = *a.Value.StringValue
		}

		if a.Value.IntValue != nil {
			ints[a.Key] = *a.Value.IntValue
		}
	}

	sequence, err := strconv.ParseInt(ints["sequence"], 10, 64)
	if err != nil || texts["scope"] == "" {
		return ReceivedEntry{}, fmt.Errorf("%w: %w", errOTLPRecord, errNoScopeOrSequence)
	}

	return ReceivedEntry{
		Scope:    texts["scope"],
		Action:   texts["action"],
		Object:   texts["object"],
		Kind:     texts["kind"],
		Hash:     texts["hash"],
		Sequence: sequence,
	}, nil
}

// startTLSServer starts handler on a loopback port with cert and stops it
// when the test ends.
func startTLSServer(tb testing.TB, cert tls.Certificate, handler http.Handler) *httptest.Server {
	tb.Helper()

	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	srv.StartTLS()

	tb.Cleanup(srv.Close)

	return srv
}
