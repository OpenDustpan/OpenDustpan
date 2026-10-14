// Package webhook is a logsink.Sink adapter that posts each batch of
// records to an HTTPS endpoint (ADR 60 decision 12). The body is a JSON
// array that holds one object for each record, as logsink.MarshalJSON
// writes it.
//
// Each request carries two headers, so the receiver can check the sender
// and refuse a replayed request:
//
//   - Dustpan-Timestamp: the time of the request in Unix seconds.
//   - Dustpan-Signature: "v1=" and the hex HMAC-SHA256, keyed with the
//     shared secret, over the timestamp, a ".", and the body.
//
// A 2xx answer means the receiver holds the batch. Any other answer, or no
// answer, is an error. The sink never follows a redirect: a 3xx answer is
// an error too, so a batch never goes to another host, and a redirect that
// turns the POST into a GET never reads as delivered.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// ErrRefused reports a receiver that answered a status other than 2xx. An
// error that wraps it also wraps logsink.ErrRefused.
var ErrRefused = errors.New("webhook receiver refused the batch")

// Header names of a request.
const (
	HeaderTimestamp = "Dustpan-Timestamp"
	HeaderSignature = "Dustpan-Signature"
)

// signaturePrefix names the version of the signature scheme.
const signaturePrefix = "v1="

// defaultTimeout bounds one request when Config sets none.
const defaultTimeout = 10 * time.Second

// maxDrainBytes caps the answer body the sink reads and discards, so a
// large answer never holds the sink.
const maxDrainBytes = 64 << 10

// Config configures a Sink.
type Config struct {
	// Client sends the requests. Nil means a client with Timeout. The sink
	// uses a copy of Client that never follows a redirect.
	Client *http.Client
	// URL is the HTTPS endpoint.
	URL string
	// Secret keys the HMAC signature. It must not be empty.
	Secret []byte
	// Timeout bounds one request when Client is nil. Zero means 10 seconds.
	Timeout time.Duration
}

// Sink posts records to one endpoint. It is safe for concurrent use.
type Sink struct {
	client *http.Client
	now    func() time.Time
	url    string
	secret []byte
	mu     sync.RWMutex
	closed bool
}

var _ logsink.Sink = (*Sink)(nil)

// New checks cfg and returns a sink. The URL must use https.
func New(cfg Config) (*Sink, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		// A url.Error repeats the URL, which can hold credentials. Keep
		// only the cause.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return nil, fmt.Errorf("%w: webhook URL: %w", logsink.ErrInvalidConfig, err)
	}

	if u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("%w: webhook URL must be an https URL with a host", logsink.ErrInvalidConfig)
	}

	if len(cfg.Secret) == 0 {
		return nil, fmt.Errorf("%w: webhook secret is empty", logsink.ErrInvalidConfig)
	}

	return &Sink{
		client: noRedirectClient(cfg),
		now:    time.Now,
		url:    u.String(),
		secret: append([]byte(nil), cfg.Secret...),
		mu:     sync.RWMutex{},
		closed: false,
	}, nil
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

// Sign returns the Dustpan-Signature value for a request with timestamp
// and body, keyed with secret. A receiver computes it and compares it with
// hmac.Equal.
func Sign(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte{'.'})
	mac.Write(body)

	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// Write implements logsink.Sink: it posts the batch in one request.
func (s *Sink) Write(ctx context.Context, records []logsink.Record) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("post webhook: %w", err)
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

	body, err := encode(records)
	if err != nil {
		return err
	}

	return s.post(ctx, body)
}

// encode writes records as one JSON array.
func encode(records []logsink.Record) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteByte('[')

	for i := range records {
		if i > 0 {
			buf.WriteByte(',')
		}

		line, err := logsink.MarshalJSON(records[i])
		if err != nil {
			return nil, err
		}

		buf.Write(line)
	}

	buf.WriteByte(']')

	return buf.Bytes(), nil
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

// post sends body with the signature headers and checks the answer.
func (s *Sink) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}

	timestamp := strconv.FormatInt(s.now().Unix(), 10)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderSignature, Sign(s.secret, timestamp, body))

	resp, err := s.client.Do(req)
	if err != nil {
		// A url.Error repeats the URL, which can hold a token. The log line
		// of the shipper must never hold it (ADR 32 decision 7), so keep
		// only the cause.
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return fmt.Errorf("post webhook: %w", err)
	}

	// Drain a bounded part of the answer, so the client can reuse the
	// connection. A failed drain or close does not change the result: the
	// status already tells whether the receiver holds the batch.
	drained := io.LimitReader(resp.Body, maxDrainBytes)
	_, _ = io.Copy(io.Discard, drained) //nolint:errcheck // The status decides the result.
	_ = resp.Body.Close()               //nolint:errcheck // The status decides the result.

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("%w (%w): status %d", ErrRefused, logsink.ErrRefused, resp.StatusCode)
	}

	return nil
}
