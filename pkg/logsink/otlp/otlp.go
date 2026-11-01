// Package otlp is a logsink.Sink adapter that posts each batch of records
// to an OpenTelemetry collector over OTLP/HTTP with the JSON encoding. It
// uses the standard library only.
//
// The sink posts one ExportLogsServiceRequest to <base>/v1/logs for each
// batch. One resource holds all records. Its attribute service.name names
// the sender. One LogRecord stands for one record:
//
//   - timeUnixNano is the time of the record. A zero time, or a time outside
//     the range of int64 nanoseconds, becomes the time the sink got the batch.
//   - severityNumber and severityText follow the level (debug 5, info 9,
//     warn 13, error 17).
//   - body holds the message as a stringValue.
//   - attributes hold the attributes of the record in order. A string, an
//     integer, a float and a boolean keep their type. A JSON object becomes
//     a kvlistValue, a JSON array becomes an arrayValue, and a nil value
//     becomes an empty AnyValue.
//
// The sink can send extra request headers, for example an API key. The
// caller reads them from a secret source and passes them in Config. The
// sink never writes a header value to an error. It sends the headers only
// to the URL of Config, and it never follows a redirect, so a header never
// reaches another host.
//
// A 2xx answer means the receiver holds the batch. Any other answer, or no
// answer, is an error. A 2xx answer with a partial success that rejects
// records is a logsink.SkipError: the receiver holds the other records, and
// a retry cannot deliver the rejected ones.
package otlp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// ErrRefused reports a receiver that answered a status other than 2xx. An
// error that wraps it also wraps logsink.ErrRefused.
var ErrRefused = errors.New("OTLP receiver refused the batch")

// DefaultServiceName is the value of service.name when Config sets none.
const DefaultServiceName = "opendustpan"

// logsPath is the path of the OTLP/HTTP logs signal.
const logsPath = "/v1/logs"

// scopeName names the instrumentation scope of the records.
const scopeName = "opendustpan.org/opendustpan/pkg/logsink/otlp"

// defaultTimeout bounds one request when Config sets no client.
const defaultTimeout = 10 * time.Second

// maxAnswerBytes caps the answer body the sink reads. The body can hold a
// partial success, which is small.
const maxAnswerBytes = 64 << 10

// maxNesting caps the depth of a JSON attribute, so a hostile value cannot
// exhaust the stack.
const maxNesting = 32

// Severity numbers of the OTLP data model for the four levels of the port.
const (
	severityDebug = 5
	severityInfo  = 9
	severityWarn  = 13
	severityError = 17
)

// Config configures a Sink.
type Config struct {
	// Client sends the requests. Nil means a client with Timeout. The sink
	// uses a copy of Client that never follows a redirect.
	Client *http.Client
	// Headers are extra request headers, for example an API key. A name
	// must be a valid header name. Content-Type and the other headers that
	// the sink or the HTTP client sets are not allowed. A value must hold
	// no control character.
	Headers map[string]string
	// URL is the base HTTPS URL of the receiver, for example
	// https://collector.example:4318. The sink adds /v1/logs, unless the
	// path of the URL already ends with it.
	URL string
	// ServiceName is the value of the resource attribute service.name. Empty
	// means DefaultServiceName.
	ServiceName string
	// Timeout bounds one request when Client is nil. Zero means 10 seconds.
	Timeout time.Duration
}

// Sink posts records to one receiver. It is safe for concurrent use.
type Sink struct {
	client  *http.Client
	headers map[string]string
	url     string
	service string
	now     func() time.Time
	mu      sync.RWMutex
	closed  bool
}

var _ logsink.Sink = (*Sink)(nil)

// New checks cfg and returns a sink. The URL must use https, so a header
// never travels in clear text.
func New(cfg Config) (*Sink, error) {
	endpoint, err := endpointURL(cfg.URL)
	if err != nil {
		return nil, err
	}

	headers := make(map[string]string, len(cfg.Headers))

	for name, value := range cfg.Headers {
		if err := CheckHeader(name, value); err != nil {
			// The error names the header, never its value.
			return nil, fmt.Errorf("%w: header %q: %w", logsink.ErrInvalidConfig, name, err)
		}

		headers[name] = value
	}

	service := cfg.ServiceName
	if service == "" {
		service = DefaultServiceName
	}

	return &Sink{
		client:  noRedirectClient(cfg),
		headers: headers,
		url:     endpoint,
		service: service,
		now:     time.Now,
		mu:      sync.RWMutex{},
		closed:  false,
	}, nil
}

// endpointURL checks raw and answers the URL of the logs signal.
func endpointURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		// A url.Error repeats the URL, which can hold a token. Keep only
		// the cause.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return "", fmt.Errorf("%w: OTLP URL: %w", logsink.ErrInvalidConfig, err)
	}

	if u.Scheme != "https" || u.Host == "" {
		return "", fmt.Errorf("%w: OTLP URL must be an https URL with a host", logsink.ErrInvalidConfig)
	}

	if u.User != nil {
		return "", fmt.Errorf("%w: OTLP URL must hold no user name or password: send a header instead",
			logsink.ErrInvalidConfig)
	}

	u.Fragment = ""

	if !strings.HasSuffix(strings.TrimRight(u.Path, "/"), logsPath) {
		u = u.JoinPath(logsPath)
	}

	return u.String(), nil
}

// Reasons that CheckHeader gives.
var (
	errHeaderNameEmpty    = errors.New("the name is empty")
	errHeaderNameChar     = errors.New("the name holds a character that is not allowed in a header name")
	errHeaderNameReserved = errors.New("the name is reserved: the sink sets this header")
	errHeaderValueControl = errors.New("the value holds a control character")
)

// reserved reports whether name is a header that the sink or the HTTP
// client sets. A caller cannot override it. The comparison ignores case.
func reserved(name string) bool {
	switch strings.ToLower(name) {
	case "content-type", "content-length", "content-encoding", "host", "connection",
		"transfer-encoding", "te", "trailer", "upgrade", "expect":
		return true
	default:
		return false
	}
}

// CheckHeader checks one extra request header: an allowlist for the name
// (the characters of an HTTP token), no reserved name, and no control
// character in the value. It never repeats the value in its error.
func CheckHeader(name, value string) error {
	if name == "" {
		return errHeaderNameEmpty
	}

	for i := range len(name) {
		if !isTokenByte(name[i]) {
			return errHeaderNameChar
		}
	}

	if reserved(name) {
		return errHeaderNameReserved
	}

	for i := range len(value) {
		if c := value[i]; (c < ' ' && c != '\t') || c == 0x7f {
			return errHeaderValueControl
		}
	}

	return nil
}

// isTokenByte reports whether c may appear in an HTTP token (RFC 9110).
func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	default:
		return strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0
	}
}

// noRedirectClient returns a copy of the client of cfg, or a new client
// with the timeout of cfg, that answers the first response and never
// follows a redirect.
func noRedirectClient(cfg Config) *http.Client {
	var client http.Client

	if cfg.Client != nil {
		client = *cfg.Client
	} else {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}

		client.Timeout = timeout
	}

	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return &client
}

// Write implements logsink.Sink: it posts the batch in one request.
func (s *Sink) Write(ctx context.Context, records []logsink.Record) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("post OTLP logs: %w", err)
	}

	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()

	if closed {
		return logsink.ErrClosed
	}

	if len(records) == 0 {
		return nil
	}

	body, err := encode(s.service, s.now(), records)
	if err != nil {
		return err
	}

	return s.post(ctx, body, len(records))
}

// Close implements logsink.Sink. It closes the idle connections of the
// client.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		s.client.CloseIdleConnections()
	}

	return nil
}

// post sends body and checks the answer.
func (s *Sink) post(ctx context.Context, body []byte, count int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build OTLP request: %w", err)
	}

	// Defense in depth: New checked each header, and this check stays so
	// that a later change of the header table cannot send a bad header. It
	// does not rely on the check in New or on the HTTP client.
	for name, value := range s.headers {
		if checkErr := CheckHeader(name, value); checkErr != nil {
			return fmt.Errorf("%w: header %q: %w", logsink.ErrInvalidConfig, name, checkErr)
		}

		req.Header.Set(name, value)
	}

	// The fixed headers come last, so a configured header never replaces
	// them.
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		// A url.Error repeats the URL, which can hold a token. The log line
		// of the shipper must never hold it, so keep only the cause.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return fmt.Errorf("post OTLP logs: %w", err)
	}

	// A failed read or close does not change the result: the status already
	// tells whether the receiver holds the batch.
	answer, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if readErr != nil {
		answer = nil
	}

	if closeErr := resp.Body.Close(); closeErr != nil {
		answer = nil
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		// The answer body can echo the request or a secret, so the error
		// holds the status only.
		return fmt.Errorf("%w (%w): status %d", ErrRefused, logsink.ErrRefused, resp.StatusCode)
	}

	return partialSuccess(answer, count)
}

// partialSuccess reads the answer of a 2xx status. A receiver that rejects
// records says so in partialSuccess.rejectedLogRecords. The receiver holds
// the other records, so the result is a SkipError. An answer that is empty
// or not JSON means full success.
func partialSuccess(answer []byte, count int) error {
	rejected := rejectedCount(answer)
	if rejected <= 0 {
		return nil
	}

	if rejected > int64(count) {
		rejected = int64(count)
	}

	// The error message of the receiver is not part of the reason: it can
	// echo data.
	return &logsink.SkipError{Reason: "the OTLP receiver rejected records", Skipped: int(rejected), Indexes: nil}
}

// partialSuccessBody is the partialSuccess member of an export response.
type partialSuccessBody struct {
	// The JSON mapping writes an int64 as a string, but some receivers
	// write a number. json.Number takes both.
	Rejected json.Number `json:"rejectedLogRecords"`
}

// exportResponse is the part of an ExportLogsServiceResponse that the sink
// reads.
type exportResponse struct {
	PartialSuccess *partialSuccessBody `json:"partialSuccess"`
}

// rejectedCount answers partialSuccess.rejectedLogRecords of an export
// response, or 0 when the answer holds none.
func rejectedCount(answer []byte) int64 {
	var parsed exportResponse

	if json.Unmarshal(answer, &parsed) != nil || parsed.PartialSuccess == nil {
		return 0
	}

	n, err := strconv.ParseInt(parsed.PartialSuccess.Rejected.String(), 10, 64)
	if err != nil {
		return 0
	}

	return n
}
