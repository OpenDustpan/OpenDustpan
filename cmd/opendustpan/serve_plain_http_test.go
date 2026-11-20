package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestServeCheckPlainHTTPKinds proves the start check of serve for each kind
// of listener, from the flags.
func TestServeCheckPlainHTTPKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		refused bool
	}{
		{"loopback", []string{"--listen", "127.0.0.1:8080"}, false},
		{"non-loopback with TLS", []string{"--listen", ":8443", "--tls-cert", "c.pem", "--tls-key", "k.pem"}, false},
		{
			"non-loopback plain with an https public URL",
			[]string{"--listen", ":8080", "--public-url", "https://dustpan.example"},
			false,
		},
		{
			"non-loopback plain with an http public URL",
			[]string{"--listen", ":8080", "--public-url", "http://dustpan.example"},
			true,
		},
		{"non-loopback plain with the opt-in", []string{"--listen", ":8080", "--allow-plain-http"}, false},
		{"non-loopback plain with neither", []string{"--listen", ":8080"}, true},
		{"the default listen address", nil, true},
		{"the opt-in set to false", []string{"--listen", "0.0.0.0:8080", "--allow-plain-http=false"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			if err := newTestFlagSet(&f).Parse(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			err := f.checkPlainHTTP()
			if tt.refused != errors.Is(err, cli.ErrPlainHTTP) {
				t.Fatalf("checkPlainHTTP = %v, want refused %v", err, tt.refused)
			}
		})
	}
}

// TestServeRefusesHostileOptInFlag proves that the flag takes only a bool:
// another value fails the parse, so serve does not start.
func TestServeRefusesHostileOptInFlag(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"yes", "on", "2", "tru", "true;", ""} {
		var f serveFlags

		fs := newTestFlagSet(&f)
		fs.SetOutput(io.Discard)

		if err := fs.Parse([]string{"--listen", ":8080", "--allow-plain-http=" + value}); err == nil {
			t.Errorf("parse --allow-plain-http=%q = nil, want an error", value)
		}
	}
}

// TestServeOptInVariable proves the variable: only true or 1, in any case and
// with space around it, sets the opt-in. Each other value leaves it off, so
// serve refuses plain HTTP.
func TestServeOptInVariable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value   string
		refused bool
	}{
		{"true", false},
		{"TRUE", false},
		{" 1 ", false},
		{"yes", true},
		{"on", true},
		{"y", true},
		{"2", true},
		{"-1", true},
		{"truee", true},
		{"true\x00", true},
		{"false", true},
		{"0", true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			fs := newTestFlagSet(&f)
			if err := fs.Parse([]string{"--listen", ":8080"}); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			lookup := func(key string) (string, bool) {
				if key == "DUSTPAN_ALLOW_PLAIN_HTTP" {
					return tt.value, true
				}

				return "", false
			}

			if err := f.resolveLayers(fs, lookup); err != nil {
				t.Fatalf("resolve layers: %v", err)
			}

			err := f.checkPlainHTTP()
			if tt.refused != errors.Is(err, cli.ErrPlainHTTP) {
				t.Fatalf("DUSTPAN_ALLOW_PLAIN_HTTP=%q: checkPlainHTTP = %v, want refused %v", tt.value, err, tt.refused)
			}
		})
	}
}

// TestServeOptInFileKey proves that the configuration file key takes only
// the plain scalars true and false: a quoted value or a YAML 1.1 word fails.
func TestServeOptInFileKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line    string
		wantErr bool
		refused bool
	}{
		{"allow-plain-http: true\n", false, false},
		{"allow-plain-http: false\n", false, true},
		{"allow-plain-http: \"true\"\n", true, true},
		{"allow-plain-http: yes\n", true, true},
		{"allow-plain-http: 1\n", true, true},
		{"allow-plain-http: [true]\n", true, true},
	}

	for _, tt := range tests {
		t.Run(strings.TrimSpace(tt.line), func(t *testing.T) {
			t.Parallel()

			config := filepath.Join(t.TempDir(), "serve.yaml")
			if err := os.WriteFile(config, []byte(tt.line), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			f, err := resolveWithConfig(t, config)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolve layers = %v, want an error %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			if checkErr := f.checkPlainHTTP(); tt.refused != errors.Is(checkErr, cli.ErrPlainHTTP) {
				t.Fatalf("checkPlainHTTP = %v, want refused %v", checkErr, tt.refused)
			}
		})
	}
}

// resolveWithConfig parses --config path and --listen :8080, and resolves
// the layers with no variable. It answers the flags and the error of the
// resolve.
func resolveWithConfig(t *testing.T, path string) (*serveFlags, error) {
	t.Helper()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse([]string{"--config", path, "--listen", ":8080"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	return &f, f.resolveLayers(fs, func(string) (string, bool) { return "", false })
}

// TestServeRefusesPlainHTTPBeforeTheDatabase proves that serve stops with
// the one message that names the three fixes, and that it does not open
// or make the database.
func TestServeRefusesPlainHTTPBeforeTheDatabase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	database := filepath.Join(dir, "dustpan.db")

	err := serveWith(t.Context(), []string{
		"--database", "sqlite:" + database,
		"--key-encryption-key-file", filepath.Join(dir, "missing.key"),
		"--listen", ":0",
	}, io.Discard, false, time.Millisecond)
	if !errors.Is(err, cli.ErrPlainHTTP) {
		t.Fatalf("serve = %v, want ErrPlainHTTP", err)
	}

	for _, want := range []string{"--tls-cert", "--public-url", "--allow-plain-http"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("serve error %q does not hold %q", err, want)
		}
	}

	if _, statErr := os.Stat(database); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat database = %v, want no database file", statErr)
	}
}

// TestServeKeyErrorComesFirst proves that the key error wins over the
// plain HTTP refusal, so a person with no settings gets the hint for dev.
func TestServeKeyErrorComesFirst(t *testing.T) {
	t.Parallel()

	err := serveWith(t.Context(), []string{"--listen", ":0"}, io.Discard, false, time.Millisecond)
	if !errors.Is(err, errNoKey) {
		t.Fatalf("serve = %v, want errNoKey", err)
	}
}

// TestCheckBoundAddrRefusesHostileAddresses sends bound addresses straight
// to the check at the listener, as if the start check did not run: each
// address that is not loopback is refused without TLS, an https public URL
// or the opt-in, and an address that is not TCP is refused always.
func TestCheckBoundAddrRefusesHostileAddresses(t *testing.T) {
	t.Parallel()

	refused := []net.Addr{
		&net.TCPAddr{IP: net.IPv4zero, Port: 8080, Zone: ""},
		&net.TCPAddr{IP: net.IPv6unspecified, Port: 8080, Zone: ""},
		&net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 8080, Zone: ""},
		&net.TCPAddr{IP: net.ParseIP("192.0.2.10"), Port: 8080, Zone: ""},
		&net.TCPAddr{IP: net.ParseIP("fe80::1"), Port: 8080, Zone: "en0"},
		&net.TCPAddr{IP: nil, Port: 8080, Zone: ""},
		(*net.TCPAddr)(nil),
		&net.UnixAddr{Name: "/tmp/dustpan.sock", Net: "unix"},
		nil,
	}

	logger := slog.New(slog.DiscardHandler)

	for _, addr := range refused {
		// The settings name localhost: a name that resolves to another
		// address must not pass.
		settings := cli.ListenSettings{
			Address:        "localhost:8080",
			TLSCert:        "",
			TLSKey:         "",
			PublicURL:      "",
			AllowPlainHTTP: false,
		}

		if err := checkBoundAddr(t.Context(), logger, addr, settings); err == nil {
			t.Errorf("checkBoundAddr(%v) = nil, want a refusal", addr)
		}
	}

	for _, addr := range []net.Addr{
		&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080, Zone: ""},
		&net.TCPAddr{IP: net.IPv6loopback, Port: 8080, Zone: ""},
	} {
		wildcard := cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false}

		err := checkBoundAddr(t.Context(), logger, addr, wildcard)
		if err != nil {
			t.Errorf("checkBoundAddr(%v) = %v, want nil for loopback", addr, err)
		}
	}
}

// TestCheckBoundAddrOptInWarns proves that the opt-in lets serve listen off
// loopback, and that the start logs a warning that names the flag.
func TestCheckBoundAddrOptInWarns(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	logger := slog.New(slog.NewTextHandler(&logs, nil))
	addr := &net.TCPAddr{IP: net.IPv4zero, Port: 8080, Zone: ""}

	err := checkBoundAddr(
		t.Context(),
		logger,
		addr,
		cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: true},
	)
	if err != nil {
		t.Fatalf("checkBoundAddr with the opt-in = %v, want nil", err)
	}

	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "--allow-plain-http") {
		t.Fatalf("logs lack the warning that names the flag:\n%s", logs.String())
	}

	logs.Reset()

	proxy := cli.ListenSettings{
		Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "https://dustpan.example", AllowPlainHTTP: false,
	}

	err = checkBoundAddr(t.Context(), logger, addr, proxy)
	if err != nil {
		t.Fatalf("checkBoundAddr with an https public URL = %v, want nil", err)
	}

	if logs.Len() != 0 {
		t.Fatalf("logs with an https public URL = %q, want no warning", logs.String())
	}
}

// TestServeListenCheckedRefusesAWildcard proves that serve closes the
// listener and stops when the bound address is not loopback and no rule
// lets it listen there.
func TestServeListenCheckedRefusesAWildcard(t *testing.T) {
	t.Parallel()

	var f serveFlags

	if err := newTestFlagSet(&f).Parse([]string{"--listen", "127.0.0.1:0"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	ln, err := f.listenChecked(t.Context(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("listenChecked on loopback = %v, want nil", err)
	}

	if closeErr := ln.Close(); closeErr != nil {
		t.Fatalf("close listener: %v", closeErr)
	}

	// An address with no host binds every interface.
	f.listen = ":0"

	if _, err = f.listenChecked(t.Context(), slog.New(slog.DiscardHandler)); !errors.Is(err, cli.ErrPlainHTTP) {
		t.Fatalf("listenChecked on :0 = %v, want ErrPlainHTTP", err)
	}
}
