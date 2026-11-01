package otlp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/otlp"
)

// The types below hold the fields of the OTLP logs JSON schema that the
// sink writes. The test receiver decodes a request into them.

type arrayValue struct {
	Values []anyValue `json:"values"`
}

type kvlistValue struct {
	Values []keyValue `json:"values"`
}

type anyValue struct {
	StringValue *string      `json:"stringValue"`
	BoolValue   *bool        `json:"boolValue"`
	IntValue    *string      `json:"intValue"`
	DoubleValue *float64     `json:"doubleValue"`
	ArrayValue  *arrayValue  `json:"arrayValue"`
	KvlistValue *kvlistValue `json:"kvlistValue"`
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

type logRecord struct {
	TimeUnixNano   string     `json:"timeUnixNano"`
	SeverityText   string     `json:"severityText"`
	Body           anyValue   `json:"body"`
	Attributes     []keyValue `json:"attributes"`
	SeverityNumber int        `json:"severityNumber"`
}

type resource struct {
	Attributes []keyValue `json:"attributes"`
}

type scope struct {
	Name string `json:"name"`
}

type scopeLogs struct {
	Scope      scope       `json:"scope"`
	LogRecords []logRecord `json:"logRecords"`
}

type resourceLogs struct {
	Resource  resource    `json:"resource"`
	ScopeLogs []scopeLogs `json:"scopeLogs"`
}

type exportRequest struct {
	ResourceLogs []resourceLogs `json:"resourceLogs"`
}

// receiver is a test OTLP collector. It checks the shape of each request
// and keeps the records, the headers and the bodies.
type receiver struct {
	srv     *httptest.Server
	answer  func(w http.ResponseWriter)
	recs    []logRecord
	headers []http.Header
	paths   []string
	bodies  [][]byte
	mu      sync.Mutex
}

// newReceiver starts a collector. answer writes the response; nil means an
// empty export response with status 200.
func newReceiver(t *testing.T, answer func(w http.ResponseWriter)) *receiver {
	t.Helper()

	r := &receiver{
		srv: nil, answer: answer, recs: nil, headers: nil, paths: nil, bodies: nil, mu: sync.Mutex{},
	}
	r.srv = httptest.NewTLSServer(http.HandlerFunc(r.handle))

	t.Cleanup(r.srv.Close)

	return r
}

func (r *receiver) handle(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.paths = append(r.paths, req.Method+" "+req.URL.RequestURI())
	r.headers = append(r.headers, req.Header.Clone())
	r.bodies = append(r.bodies, body)

	if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
		w.WriteHeader(http.StatusUnsupportedMediaType)

		return
	}

	var exp exportRequest
	if err := json.Unmarshal(body, &exp); err != nil || len(exp.ResourceLogs) != 1 ||
		len(exp.ResourceLogs[0].ScopeLogs) != 1 {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	r.recs = append(r.recs, exp.ResourceLogs[0].ScopeLogs[0].LogRecords...)

	if r.answer != nil {
		r.answer(w)

		return
	}

	reply(w, `{}`)
}

// newSink returns a sink for r that closes with the test.
func newSink(t *testing.T, r *receiver, headers map[string]string) *otlp.Sink {
	t.Helper()

	s, err := otlp.New(otlp.Config{
		Client: r.srv.Client(), Headers: headers, URL: r.srv.URL, ServiceName: "", Timeout: 0,
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	t.Cleanup(func() { closeSink(t, s) })

	return s
}

func closeSink(t *testing.T, s *otlp.Sink) {
	t.Helper()

	if err := s.Close(); err != nil {
		t.Errorf("close sink: %v", err)
	}
}

// rec returns a record with a message, no attributes and the level info.
func rec(message string) logsink.Record {
	return logsink.Record{Time: time.Unix(1, 0), Level: logsink.LevelInfo, Message: message, Attrs: nil}
}

// recWith returns rec(message) with attributes.
func recWith(message string, attrs ...logsink.Attr) logsink.Record {
	r := rec(message)
	r.Attrs = attrs

	return r
}

// attrValue converts an AnyValue back to the Go value of an Attr.
func attrValue(t *testing.T, v anyValue) any {
	t.Helper()

	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.BoolValue != nil:
		return *v.BoolValue
	case v.IntValue != nil:
		n, err := strconv.ParseInt(*v.IntValue, 10, 64)
		if err != nil {
			t.Fatalf("intValue %q is not an int64 string: %v", *v.IntValue, err)
		}

		return n
	case v.DoubleValue != nil:
		return *v.DoubleValue
	default:
		return nil
	}
}

// line rebuilds the line that logsink.MarshalJSON writes from a LogRecord,
// so the conformance suite can compare it.
func line(t *testing.T, lr logRecord) []byte {
	t.Helper()

	ns, err := strconv.ParseInt(lr.TimeUnixNano, 10, 64)
	if err != nil {
		t.Fatalf("timeUnixNano %q is not an int64 string: %v", lr.TimeUnixNano, err)
	}

	if lr.Body.StringValue == nil {
		t.Fatal("body holds no stringValue")
	}

	levels := map[string]logsink.Level{
		"DEBUG": logsink.LevelDebug, "INFO": logsink.LevelInfo, "WARN": logsink.LevelWarn, "ERROR": logsink.LevelError,
	}

	out := logsink.Record{
		Time:    time.Unix(0, ns),
		Level:   levels[lr.SeverityText],
		Message: *lr.Body.StringValue,
		Attrs:   nil,
	}
	for _, a := range lr.Attributes {
		out.Attrs = append(out.Attrs, logsink.Attr{Key: a.Key, Value: attrValue(t, a.Value)})
	}

	b, err := logsink.MarshalJSON(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	return b
}

// reply writes text as the body of an answer. The client sees a failed
// write as a failed request, so the test needs no more.
func reply(w http.ResponseWriter, text string) {
	if _, err := io.WriteString(w, text); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

// isEmpty reports whether v is an AnyValue with no value set.
func isEmpty(v anyValue) bool {
	return v.StringValue == nil && v.BoolValue == nil && v.IntValue == nil && v.DoubleValue == nil &&
		v.ArrayValue == nil && v.KvlistValue == nil
}
