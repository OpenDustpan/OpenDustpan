package syslog_test

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/logsinktest"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
)

// deliveryWait bounds how long a test waits for messages to arrive.
const deliveryWait = 5 * time.Second

func TestConformanceUDP(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		r := listenUDP(t)

		return testSink(t, config(syslog.UDP, r.addr), r)
	})
}

func TestConformanceTCP(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		r := listenStream(t, nil)

		return testSink(t, config(syslog.TCP, r.addr), r)
	})
}

func TestConformanceTLS(t *testing.T) {
	t.Parallel()

	logsinktest.Run(t, func(t *testing.T) logsinktest.TestSink {
		t.Helper()

		serverCfg, roots := selfSigned(t)
		r := listenStream(t, serverCfg)

		cfg := config(syslog.TLS, r.addr)
		cfg.TLSConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}

		return testSink(t, cfg, r)
	})
}

func TestHeaderFormat(t *testing.T) {
	t.Parallel()

	cfg := config(syslog.UDP, "127.0.0.1:514")
	cfg.Hostname = "host one"
	cfg.AppName = "dustpan"
	cfg.Facility = syslog.FacilityLogAudit

	s, err := syslog.New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 5000000, time.UTC),
		Level:   logsink.LevelWarn,
		Message: "m",
		Attrs:   nil,
	}

	got, err := s.Format(rec)
	if err != nil {
		t.Fatalf("format: %v", err)
	}

	// Facility 13, severity 4 (warning): 13*8+4 = 108. The space in the
	// host name is not printable US-ASCII, so the field drops it.
	want := "<108>1 2026-10-13T12:00:00.005Z hostone dustpan " + strconv.Itoa(os.Getpid()) +
		` - - {"time":"2026-10-13T12:00:00.005Z","level":"warn","msg":"m"}`
	if string(got) != want {
		t.Fatalf("format:\n got %s\nwant %s", got, want)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  syslog.Config
	}{
		{name: "unknown network", cfg: config("unix", "127.0.0.1:514")},
		{name: "address without port", cfg: config(syslog.UDP, "127.0.0.1")},
		{name: "facility too high", cfg: withFacility(config(syslog.UDP, "127.0.0.1:514"), 24)},
		{name: "negative facility", cfg: withFacility(config(syslog.UDP, "127.0.0.1:514"), -1)},
		{name: "message cap too low", cfg: withMaxBytes(config(syslog.UDP, "127.0.0.1:514"), 100)},
		{name: "message cap too high", cfg: withMaxBytes(config(syslog.UDP, "127.0.0.1:514"), 70000)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := syslog.New(tc.cfg); err == nil {
				t.Fatal("new: got nil error")
			}
		})
	}
}

func TestLargeRecordIsTrimmedAndDelivered(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.MinMaxMessageBytes))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{
		Time:    time.Date(2026, time.October, 13, 12, 0, 0, 0, time.UTC),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 7),
			logsink.String("action", "unit:read-secrets"),
			logsink.String("object", "pulumi:acme/web/prod"),
			logsink.String("kind", "reveal"),
			logsink.String("outcome", "success"),
			logsink.String("prevHash", "abc"),
			logsink.String("hash", "def"),
			logsink.String("detail", strings.Repeat("d", 600)),
			logsink.String("note", strings.Repeat("n", 600)),
			logsink.String("extra", strings.Repeat("e", 600)),
		},
	}

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		skip, ok := errors.AsType[*logsink.SkipError](err)
		if ok {
			t.Fatalf("write: skipped %d records, want none", skip.Skipped)
		}

		t.Fatalf("write: %v", err)
	}

	msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1", len(msgs))
	}

	if len(msgs[0]) > syslog.MinMaxMessageBytes {
		t.Fatalf("message holds %d bytes, want at most %d", len(msgs[0]), syslog.MinMaxMessageBytes)
	}

	fields := decodeBody(t, body(t, msgs[0]))

	marked, ok := fields["truncated"].(bool)
	if !ok || !marked {
		t.Fatalf("message lacks truncated true: %s", body(t, msgs[0]))
	}

	checkProtectedFields(t, fields)
	checkTrimmedFields(t, fields)
}

// checkProtectedFields fails when a protected audit field changed.
func checkProtectedFields(t *testing.T, fields map[string]any) {
	t.Helper()

	for key, want := range map[string]any{
		"scope":    "acme",
		"sequence": float64(7),
		"action":   "unit:read-secrets",
		"object":   "pulumi:acme/web/prod",
		"kind":     "reveal",
		"outcome":  "success",
		"prevHash": "abc",
		"hash":     "def",
	} {
		if fields[key] != want {
			t.Fatalf("protected field %q: got %v, want %v", key, fields[key], want)
		}
	}
}

// checkTrimmedFields fails when no long field got shorter.
func checkTrimmedFields(t *testing.T, fields map[string]any) {
	t.Helper()

	for _, key := range []string{"detail", "note", "extra"} {
		text, ok := fields[key].(string)
		if !ok {
			t.Fatalf("field %q is not a string: %v", key, fields[key])
		}

		if len(text) < 600 {
			return
		}
	}

	t.Fatal("no long field got shorter")
}

func TestRecordWithLongProtectedFieldIsSkippedAndTheRestArrive(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"subject", "object", "userAgent", "requestID"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()

			r := listenStream(t, nil)

			s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.MinMaxMessageBytes))
			if err != nil {
				t.Fatalf("new: %v", err)
			}

			closeOnCleanup(t, s)

			large := logsink.Record{
				Time:    time.Unix(0, 0),
				Level:   logsink.LevelInfo,
				Message: "audit entry",
				Attrs: []logsink.Attr{
					logsink.String("scope", "acme"),
					logsink.Int64("sequence", 7),
					logsink.String("action", "unit:read-secrets"),
					logsink.String("kind", "reveal"),
					logsink.String("outcome", "success"),
					logsink.String(key, strings.Repeat("x", 2000)),
				},
			}
			small := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "small", Attrs: nil}

			err = s.Write(t.Context(), []logsink.Record{large, small})

			skip, ok := errors.AsType[*logsink.SkipError](err)
			if !ok || skip.Skipped != 1 || !errors.Is(err, logsink.ErrSkipped) {
				t.Fatalf("write with a long %q: got %v, want a skip of 1 record", key, err)
			}

			msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
			if len(msgs) != 1 || !bytes.Contains(msgs[0], []byte(`"msg":"small"`)) {
				t.Fatalf("received %q, want only the small record", msgs)
			}
		})
	}
}

func TestRecordThatCannotFitIsSkippedAndTheRestArrive(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.MinMaxMessageBytes))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	large := logsink.Record{
		Time:    time.Unix(0, 0),
		Level:   logsink.LevelInfo,
		Message: "audit entry",
		Attrs: []logsink.Attr{
			logsink.String("scope", "acme"),
			logsink.Int64("sequence", 7),
			logsink.String("action", strings.Repeat("a", 2000)),
			logsink.String("kind", "reveal"),
			logsink.String("outcome", "success"),
		},
	}
	small := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "small", Attrs: nil}

	err = s.Write(t.Context(), []logsink.Record{large, small})

	skip, ok := errors.AsType[*logsink.SkipError](err)
	if !ok || skip.Skipped != 1 || !errors.Is(err, logsink.ErrSkipped) {
		t.Fatalf("write: got %v, want a skip of 1 record", err)
	}

	msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
	if len(msgs) != 1 || !bytes.Contains(msgs[0], []byte(`"msg":"small"`)) {
		t.Fatalf("received %q, want only the small record", msgs)
	}
}

func TestSmallRecordHasNoTruncatedMark(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(withMaxBytes(config(syslog.TCP, r.addr), syslog.MinMaxMessageBytes))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "small", Attrs: nil}

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		t.Fatalf("write: %v", err)
	}

	msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) > 0 })
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1", len(msgs))
	}

	if bytes.Contains(body(t, msgs[0]), []byte(`"truncated"`)) {
		t.Fatalf("small message carries a truncated mark: %q", msgs[0])
	}
}

// decodeBody answers the JSON document of a syslog MSG part.
func decodeBody(t *testing.T, msg []byte) map[string]any {
	t.Helper()

	var fields map[string]any
	if err := json.Unmarshal(msg, &fields); err != nil {
		t.Fatalf("decode message %q: %v", msg, err)
	}

	return fields
}

func TestTCPWriteDialsAgainAfterTheReceiverDropsTheConnection(t *testing.T) {
	t.Parallel()

	r := listenStream(t, nil)

	s, err := syslog.New(config(syslog.TCP, r.addr))
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	closeOnCleanup(t, s)

	rec := logsink.Record{Time: time.Unix(0, 0), Level: logsink.LevelInfo, Message: "one", Attrs: nil}

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		t.Fatalf("first write: %v", err)
	}

	r.dropConnections()

	// The first write after the drop can succeed into the kernel buffer of
	// the dead connection. Write until one fails and the sink dials again,
	// then check that a later write arrives.
	deadline := time.Now().Add(deliveryWait)
	for time.Now().Before(deadline) {
		if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
			break
		}

		pause(t)
	}

	rec.Message = "after"

	if err := s.Write(t.Context(), []logsink.Record{rec}); err != nil {
		t.Fatalf("write after the drop: %v", err)
	}

	r.waitFor(t, func(msgs [][]byte) bool {
		return len(msgs) > 0 && bytes.Contains(msgs[len(msgs)-1], []byte(`"msg":"after"`))
	})
}

// config returns a configuration for network and address with the
// defaults of every other field.
func config(network syslog.Network, address string) syslog.Config {
	return syslog.Config{
		TLSConfig: nil,
		Network:   network,
		Address:   address,
		Hostname:  "",
		AppName:   "",
		Facility:  0,
		Timeout:   0,

		MaxMessageBytes: 0,
	}
}

// withMaxBytes returns cfg with the message cap set.
func withMaxBytes(cfg syslog.Config, n int) syslog.Config {
	cfg.MaxMessageBytes = n

	return cfg
}

// withFacility returns cfg with facility set.
func withFacility(cfg syslog.Config, facility int) syslog.Config {
	cfg.Facility = facility

	return cfg
}

// pause waits a short time, or fails the test when it ends first.
func pause(t *testing.T) {
	t.Helper()

	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal("test ended while it waited for syslog messages")
	}
}

// testSink builds the sink for cfg and reads back from r.
func testSink(t *testing.T, cfg syslog.Config, r *receiver) logsinktest.TestSink {
	t.Helper()

	s, err := syslog.New(cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	return logsinktest.TestSink{
		Sink: s,
		Delivered: func(t *testing.T, want int) [][]byte {
			t.Helper()

			msgs := r.waitFor(t, func(msgs [][]byte) bool { return len(msgs) >= want })

			out := make([][]byte, 0, len(msgs))

			for _, m := range msgs {
				out = append(out, body(t, m))
			}

			return out
		},
	}
}

// body returns the MSG part of an RFC 5424 message with a nil MSGID and nil
// structured data: everything after the seventh space.
func body(t *testing.T, msg []byte) []byte {
	t.Helper()

	parts := bytes.SplitN(msg, []byte(" "), 8)
	if len(parts) != 8 || string(parts[5]) != "-" || string(parts[6]) != "-" {
		t.Fatalf("not an RFC 5424 message with nil MSGID and SD: %q", msg)
	}

	return parts[7]
}

// receiver collects the messages a test listener receives.
type receiver struct {
	closeConns func()
	addr       string
	msgs       [][]byte
	mu         sync.Mutex
}

func (r *receiver) add(m []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.msgs = append(r.msgs, m)
}

func (r *receiver) dropConnections() {
	r.closeConns()
}

// waitFor waits until done holds for the received messages, then answers
// a copy of them. It fails the test after deliveryWait.
func (r *receiver) waitFor(t *testing.T, done func([][]byte) bool) [][]byte {
	t.Helper()

	deadline := time.Now().Add(deliveryWait)

	for {
		r.mu.Lock()
		msgs := append([][]byte(nil), r.msgs...)
		r.mu.Unlock()

		if done(msgs) || time.Now().After(deadline) {
			return msgs
		}

		pause(t)
	}
}

// listenUDP starts a UDP receiver that stops with the test.
func listenUDP(t *testing.T) *receiver {
	t.Helper()

	var lc net.ListenConfig

	pc, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	r := &receiver{addr: pc.LocalAddr().String(), closeConns: func() {}, msgs: nil, mu: sync.Mutex{}}

	var wg sync.WaitGroup

	wg.Go(func() {
		buf := make([]byte, 64*1024)

		for {
			n, _, readErr := pc.ReadFrom(buf)
			if readErr != nil {
				return
			}

			r.add(append([]byte(nil), buf[:n]...))
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

// listenStream starts a TCP receiver, with TLS when cfg is set, that reads
// octet-counted frames and stops with the test.
func listenStream(t *testing.T, cfg *tls.Config) *receiver {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}

	if cfg != nil {
		ln = tls.NewListener(ln, cfg)
	}

	var (
		wg      sync.WaitGroup
		connsMu sync.Mutex
		conns   []net.Conn
	)

	r := &receiver{addr: ln.Addr().String(), closeConns: nil, msgs: nil, mu: sync.Mutex{}}
	r.closeConns = func() {
		connsMu.Lock()
		defer connsMu.Unlock()

		for _, c := range conns {
			// The sink can close its end first, so a failed close is no
			// test failure.
			_ = c.Close() //nolint:errcheck // See the comment above.
		}

		conns = nil
	}

	wg.Go(func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}

			connsMu.Lock()
			conns = append(conns, conn)
			connsMu.Unlock()

			wg.Go(func() { readFrames(conn, r) })
		}
	})

	t.Cleanup(func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close tcp listener: %v", err)
		}

		r.closeConns()
		wg.Wait()
	})

	return r
}

// readFrames reads octet-counted frames from conn until it closes.
func readFrames(conn net.Conn, r *receiver) {
	br := bufio.NewReader(conn)

	for {
		lenText, err := br.ReadString(' ')
		if err != nil {
			return
		}

		n, err := strconv.Atoi(lenText[:len(lenText)-1])
		if err != nil || n <= 0 {
			return
		}

		msg := make([]byte, n)
		if _, err := io.ReadFull(br, msg); err != nil {
			return
		}

		r.add(msg)
	}
}

// selfSigned returns a server TLS configuration with a self-signed
// certificate for 127.0.0.1 and a pool that trusts it.
func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	//nolint:exhaustruct_v5 // A test certificate needs only these fields.
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "syslog test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	roots := x509.NewCertPool()
	roots.AddCert(cert)

	return &tls.Config{
		//nolint:exhaustruct_v5 // A test certificate needs no OCSP staple or SCTs.
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key, Leaf: cert}},
		MinVersion:   tls.VersionTLS12,
	}, roots
}

// closeOnCleanup closes s when the test ends and reports a failed close.
func closeOnCleanup(t *testing.T, s logsink.Sink) {
	t.Helper()

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close sink: %v", err)
		}
	})
}
