// Package syslog is a logsink.Sink adapter that sends RFC 5424 messages
// over UDP, TCP or TLS (ADR 60 decision 12). It writes the RFC 5424 format
// itself, because the standard log/syslog package writes the older RFC 3164
// format.
//
// Each record is one message. The header holds the priority, version 1,
// the time of the record (UTC, milliseconds), the host name, the app name
// and the process ID. MSGID and STRUCTURED-DATA are the nil value "-". MSG
// is the JSON line that logsink.MarshalJSON writes, so a receiver reads
// every attribute without a private enterprise number for structured data.
//
// Over UDP each message is one datagram (RFC 5426). UDP has no
// acknowledgment, so a message can be lost without an error. Over TCP and
// TLS each message carries an octet count first (RFC 6587 section 3.4.1,
// RFC 5425 section 4.3).
package syslog

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
)

// Network names the transport of a Sink.
type Network string

// Networks known to the adapter.
const (
	// UDP sends each message in one datagram (RFC 5426).
	UDP Network = "udp"
	// TCP sends octet-counted messages over one connection (RFC 6587).
	TCP Network = "tcp"
	// TLS sends octet-counted messages over one TLS connection (RFC 5425).
	TLS Network = "tls"
)

// Facility codes of RFC 5424 section 6.2.1 that an operator is likely to
// pick. Any value from 0 to 23 is valid.
const (
	FacilityUser     = 1
	FacilityLogAudit = 13
	FacilityLocal0   = 16
	FacilityLocal7   = 23
)

// maxFacility is the highest facility code of RFC 5424.
const maxFacility = 23

// defaultTimeout bounds one dial and one batch write when Config sets none.
const defaultTimeout = 10 * time.Second

// Message size limits. RFC 5424 section 6.1 makes every receiver accept
// 480 octets and advises 2048. Common receivers accept 8192.
const (
	DefaultMaxMessageBytes = 8192
	MinMaxMessageBytes     = 480
	// MaxMaxMessageBytes is the largest UDP payload over IPv4.
	MaxMaxMessageBytes = 65507
)

// defaultAppName is the APP-NAME of a message when Config sets none.
const defaultAppName = "opendustpan"

// Header field limits of RFC 5424 section 6.
const (
	maxHostname = 255
	maxAppName  = 48
)

// Severity codes of RFC 5424 section 6.2.1.
const (
	severityError   = 3
	severityWarning = 4
	severityInfo    = 6
	severityDebug   = 7
)

// Config configures a Sink.
type Config struct {
	// TLSConfig configures the TLS client for Network TLS. Nil means the
	// system roots, with the host of Address as the server name.
	TLSConfig *tls.Config
	// Network is UDP, TCP or TLS.
	Network Network
	// Address is host:port of the syslog receiver.
	Address string
	// Hostname is the HOSTNAME field. Empty means the host name of the
	// machine.
	Hostname string
	// AppName is the APP-NAME field. Empty means "opendustpan".
	AppName string
	// Facility is the facility code, from 0 to 23. FacilityLogAudit (13)
	// suits the audit log. FacilityLocal0 (16) suits the application log.
	Facility int
	// Timeout bounds one dial and one batch write. Zero means 10 seconds.
	Timeout time.Duration
	// MaxMessageBytes caps one message, header included. Write trims the
	// long string fields of a record above the cap and marks it truncated,
	// so a record that the receiver cannot hold never blocks the records
	// after it. It skips only a record that cannot fit even with every
	// trimmable field empty, and answers a *logsink.SkipError for it. Zero
	// means 8192. The value must be from 480 to 65507.
	MaxMessageBytes int
}

// Sink sends records to one syslog receiver. It dials on the first Write
// and dials again after a failed write. It is safe for concurrent use.
type Sink struct {
	conn     net.Conn
	cfg      Config
	header   string
	mu       sync.Mutex
	closed   bool
	facility int
}

var _ logsink.Sink = (*Sink)(nil)

// New checks cfg and returns a sink. It does not dial: the first Write
// dials, so a receiver that is down at start does not stop the server.
func New(cfg Config) (*Sink, error) {
	switch cfg.Network {
	case UDP, TCP, TLS:
	default:
		return nil, fmt.Errorf("%w: syslog network %q: want udp, tcp or tls", logsink.ErrInvalidConfig, cfg.Network)
	}

	if _, _, err := net.SplitHostPort(cfg.Address); err != nil {
		return nil, fmt.Errorf("%w: syslog address %q: %w", logsink.ErrInvalidConfig, cfg.Address, err)
	}

	if cfg.Facility < 0 || cfg.Facility > maxFacility {
		return nil, fmt.Errorf(
			"%w: syslog facility %d: want 0 to %d",
			logsink.ErrInvalidConfig,
			cfg.Facility,
			maxFacility,
		)
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}

	maxBytes, err := messageCap(cfg.MaxMessageBytes)
	if err != nil {
		return nil, err
	}

	cfg.MaxMessageBytes = maxBytes

	host := cfg.Hostname
	if host == "" {
		// A machine without a host name sends the nil value "-".
		if name, err := os.Hostname(); err == nil {
			host = name
		}
	}

	app := cfg.AppName
	if app == "" {
		app = defaultAppName
	}

	return &Sink{
		conn:     nil,
		cfg:      cfg,
		header:   headerField(host, maxHostname) + " " + headerField(app, maxAppName) + " " + strconv.Itoa(os.Getpid()),
		mu:       sync.Mutex{},
		closed:   false,
		facility: cfg.Facility,
	}, nil
}

// messageCap answers the message cap for the configured value n: 8192 for
// zero, n when it is from 480 to 65507, else an error.
func messageCap(n int) (int, error) {
	if n == 0 {
		return DefaultMaxMessageBytes, nil
	}

	if n < MinMaxMessageBytes || n > MaxMaxMessageBytes {
		return 0, fmt.Errorf("%w: syslog message cap %d: want %d to %d",
			logsink.ErrInvalidConfig, n, MinMaxMessageBytes, MaxMaxMessageBytes)
	}

	return n, nil
}

// headerField returns s as an RFC 5424 header field: printable US-ASCII
// only (33 to 126), at most limit bytes, and "-" when nothing is left.
func headerField(s string, limit int) string {
	out := make([]byte, 0, len(s))

	for i := range len(s) {
		if c := s[i]; c >= '!' && c <= '~' {
			out = append(out, c)
		}

		if len(out) == limit {
			break
		}
	}

	if len(out) == 0 {
		return "-"
	}

	return string(out)
}

// Format returns the RFC 5424 message for r, without transport framing.
// A record above the message cap arrives trimmed to fit, with the long
// string fields shortened and a truncated mark. It answers a
// *logsink.SkipError only for a record that cannot fit even with every
// trimmable field empty.
func (s *Sink) Format(r logsink.Record) ([]byte, error) {
	var prefix bytes.Buffer

	prefix.WriteByte('<')
	prefix.WriteString(strconv.Itoa(s.facility*8 + severity(r.Level)))
	prefix.WriteString(">1 ")
	prefix.WriteString(r.Time.UTC().Format(logsink.TimeFormat))
	prefix.WriteByte(' ')
	prefix.WriteString(s.header)
	prefix.WriteString(" - - ")

	budget := s.cfg.MaxMessageBytes - prefix.Len()

	trimmed, ok, err := logsink.TrimToFit(r, budget)
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, &logsink.SkipError{
			Reason:  fmt.Sprintf("syslog message above %d bytes", s.cfg.MaxMessageBytes),
			Skipped: 1,
		}
	}

	msg, err := logsink.MarshalJSON(trimmed)
	if err != nil {
		return nil, err
	}

	return append(prefix.Bytes(), msg...), nil
}

// severity maps a level to the RFC 5424 severity code.
func severity(l logsink.Level) int {
	switch {
	case l >= logsink.LevelError:
		return severityError
	case l >= logsink.LevelWarn:
		return severityWarning
	case l >= logsink.LevelInfo:
		return severityInfo
	default:
		return severityDebug
	}
}

// Write implements logsink.Sink. Over TCP and TLS it writes the batch in
// one call on one connection. A failed write closes the connection, so the
// next Write dials again.
func (s *Sink) Write(ctx context.Context, records []logsink.Record) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("write syslog: %w", err)
	}

	msgs := make([][]byte, 0, len(records))
	skipped := 0

	for i := range records {
		m, err := s.Format(records[i])
		if err != nil {
			skip, ok := errors.AsType[*logsink.SkipError](err)
			if ok {
				skipped += skip.Skipped

				continue
			}

			return err
		}

		if len(m) > s.cfg.MaxMessageBytes {
			skipped++

			continue
		}

		msgs = append(msgs, m)
	}

	if err := s.deliver(ctx, msgs); err != nil {
		return err
	}

	if skipped > 0 {
		return &logsink.SkipError{
			Reason:  fmt.Sprintf("syslog message above %d bytes", s.cfg.MaxMessageBytes),
			Skipped: skipped,
		}
	}

	return nil
}

// Close implements logsink.Sink.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true

	if s.conn == nil {
		return nil
	}

	err := s.conn.Close()
	s.conn = nil

	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close syslog connection: %w", err)
	}

	return nil
}

// deliver sends msgs over the connection, and dials first when it has
// none. After a failed send it closes the connection, so the next Write
// dials again.
func (s *Sink) deliver(ctx context.Context, msgs [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return logsink.ErrClosed
	}

	if len(msgs) == 0 {
		return nil
	}

	if err := s.dial(ctx); err != nil {
		return err
	}

	if err := s.send(ctx, msgs); err != nil {
		closeErr := s.conn.Close()
		s.conn = nil

		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			return errors.Join(err, fmt.Errorf("close syslog connection: %w", closeErr))
		}

		return err
	}

	return nil
}

// dial opens the connection when the sink holds none.
func (s *Sink) dial(ctx context.Context) error {
	if s.conn != nil {
		return nil
	}

	dialer := &net.Dialer{Timeout: s.cfg.Timeout} //nolint:exhaustruct_v5 // Only the timeout differs from the defaults.

	var (
		conn net.Conn
		err  error
	)

	switch s.cfg.Network {
	case TLS:
		td := &tls.Dialer{NetDialer: dialer, Config: s.tlsConfig()}
		conn, err = td.DialContext(ctx, "tcp", s.cfg.Address)
	case UDP, TCP:
		conn, err = dialer.DialContext(ctx, string(s.cfg.Network), s.cfg.Address)
	default:
		return fmt.Errorf("%w: syslog network %q", logsink.ErrInvalidConfig, s.cfg.Network)
	}

	if err != nil {
		return fmt.Errorf("dial syslog %s %s: %w", s.cfg.Network, s.cfg.Address, err)
	}

	s.conn = conn

	return nil
}

// tlsConfig returns the TLS client configuration: a copy of the configured
// one, with TLS 1.2 at least.
func (s *Sink) tlsConfig() *tls.Config {
	var cfg *tls.Config
	if s.cfg.TLSConfig != nil {
		cfg = s.cfg.TLSConfig.Clone()
	} else {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	if cfg.MinVersion < tls.VersionTLS12 {
		cfg.MinVersion = tls.VersionTLS12
	}

	return cfg
}

// send writes msgs on the open connection within the write deadline.
func (s *Sink) send(ctx context.Context, msgs [][]byte) error {
	deadline := time.Now().Add(s.cfg.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	if err := s.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("set syslog write deadline: %w", err)
	}

	if s.cfg.Network == UDP {
		for _, m := range msgs {
			if _, err := s.conn.Write(m); err != nil {
				return fmt.Errorf("write syslog datagram: %w", err)
			}
		}

		return nil
	}

	var buf bytes.Buffer

	for _, m := range msgs {
		buf.WriteString(strconv.Itoa(len(m)))
		buf.WriteByte(' ')
		buf.Write(m)
	}

	if _, err := s.conn.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("write syslog stream: %w", err)
	}

	return nil
}
