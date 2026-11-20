package cli_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestCheckListenKinds proves the rule for each kind of listener: a
// loopback address, TLS, an https public URL, the opt-in, and the refusal of
// every other case.
func TestCheckListenKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   cli.ListenSettings
		want cli.ListenReason
	}{
		{
			"IPv4 loopback",
			cli.ListenSettings{
				Address:        "127.0.0.1:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
			cli.ListenLoopback,
		},
		{
			"IPv4 loopback range",
			cli.ListenSettings{
				Address:        "127.10.0.1:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
			cli.ListenLoopback,
		},
		{
			"IPv6 loopback",
			cli.ListenSettings{Address: "[::1]:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
			cli.ListenLoopback,
		},
		{
			"localhost",
			cli.ListenSettings{
				Address:        "localhost:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
			cli.ListenLoopback,
		},
		{
			"non-loopback with TLS",
			cli.ListenSettings{
				Address:        ":8443",
				TLSCert:        "c.pem",
				TLSKey:         "k.pem",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
			cli.ListenTLS,
		},
		{
			"non-loopback with an https public URL",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "https://dustpan.example",
				AllowPlainHTTP: false,
			},
			cli.ListenProxy,
		},
		{
			"non-loopback with the opt-in",
			cli.ListenSettings{Address: "0.0.0.0:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: true},
			cli.ListenPlainHTTPAllowed,
		},
		{
			"non-loopback with neither",
			cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
			cli.ListenRefused,
		},
		{
			"non-loopback with an http public URL",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "http://dustpan.example",
				AllowPlainHTTP: false,
			},
			cli.ListenRefused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.CheckListen(tt.in)
			if got != tt.want {
				t.Fatalf("CheckListen(%+v) = %v, want %v", tt.in, got, tt.want)
			}

			if (err != nil) != (tt.want == cli.ListenRefused) {
				t.Fatalf("CheckListen(%+v) error = %v, want an error only for a refusal", tt.in, err)
			}
		})
	}
}

// TestCheckListenRefusalNamesTheFixes proves that the refusal is one message
// that names the address and the three fixes, with their variables.
func TestCheckListenRefusalNamesTheFixes(t *testing.T) {
	t.Parallel()

	_, err := cli.CheckListen(
		cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
	)
	if !errors.Is(err, cli.ErrPlainHTTP) {
		t.Fatalf("CheckListen error = %v, want ErrPlainHTTP", err)
	}

	for _, want := range []string{
		`--listen ":8080"`, "DUSTPAN_LISTEN",
		"--tls-cert", "--tls-key", "DUSTPAN_TLS_CERT",
		"--public-url", "DUSTPAN_PUBLIC_URL", "https",
		"--allow-plain-http", "DUSTPAN_ALLOW_PLAIN_HTTP",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CheckListen error %q does not hold %q", err, want)
		}
	}
}

// TestCheckListenRefusesHostileValues sends hostile addresses, TLS settings
// and public URLs straight to the rule: each one is refused.
func TestCheckListenRefusesHostileValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   cli.ListenSettings
	}{
		{
			"wildcard IPv4",
			cli.ListenSettings{Address: "0.0.0.0:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"wildcard IPv6",
			cli.ListenSettings{Address: "[::]:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{"no host", cli.ListenSettings{Address: ":0", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false}},
		{
			"private address",
			cli.ListenSettings{Address: "10.0.0.1:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"a host name",
			cli.ListenSettings{
				Address:        "dustpan.example:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
		},
		{
			"localhost with a dot",
			cli.ListenSettings{
				Address:        "localhost.:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
		},
		{
			"localhost in a subdomain",
			cli.ListenSettings{
				Address:        "localhost.attacker.example:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
		},
		{
			"short loopback form",
			cli.ListenSettings{Address: "127.1:8080", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"loopback with a zone",
			cli.ListenSettings{
				Address:        "[::1%lo0]:8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "",
				AllowPlainHTTP: false,
			},
		},
		{
			"no port",
			cli.ListenSettings{Address: "127.0.0.1", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{"empty", cli.ListenSettings{Address: "", TLSCert: "", TLSKey: "", PublicURL: "", AllowPlainHTTP: false}},
		{
			"a certificate with no key",
			cli.ListenSettings{Address: ":8080", TLSCert: "c.pem", TLSKey: "", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"a key with no certificate",
			cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "k.pem", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"a blank TLS pair",
			cli.ListenSettings{Address: ":8080", TLSCert: " ", TLSKey: "\t", PublicURL: "", AllowPlainHTTP: false},
		},
		{
			"https with no host",
			cli.ListenSettings{Address: ":8080", TLSCert: "", TLSKey: "", PublicURL: "https://", AllowPlainHTTP: false},
		},
		{
			"https with a port only",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "https://:443",
				AllowPlainHTTP: false,
			},
		},
		{
			"https opaque",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "https:dustpan.example",
				AllowPlainHTTP: false,
			},
		},
		{
			"a space before https",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      " https://dustpan.example",
				AllowPlainHTTP: false,
			},
		},
		{
			"a line end in the URL",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "https://dustpan.example\n",
				AllowPlainHTTP: false,
			},
		},
		{
			"another scheme",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "ftp://dustpan.example",
				AllowPlainHTTP: false,
			},
		},
		{
			"https in the path",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "http://dustpan.example/https://x",
				AllowPlainHTTP: false,
			},
		},
		{
			"a relative URL",
			cli.ListenSettings{
				Address:        ":8080",
				TLSCert:        "",
				TLSKey:         "",
				PublicURL:      "//dustpan.example",
				AllowPlainHTTP: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.CheckListen(tt.in)
			if got != cli.ListenRefused || !errors.Is(err, cli.ErrPlainHTTP) {
				t.Fatalf("CheckListen(%+v) = %v, %v, want a refusal", tt.in, got, err)
			}
		})
	}
}

// TestAllowPlainHTTPSetting proves that the opt-in is a serve setting with a
// flag, a variable and a configuration file key, off by default.
func TestAllowPlainHTTPSetting(t *testing.T) {
	t.Parallel()

	settings := cli.SettingsFor(cli.AllowPlainHTTPFlag)
	if len(settings) != 1 {
		t.Fatalf("SettingsFor(%q) = %d settings, want 1", cli.AllowPlainHTTPFlag, len(settings))
	}

	s := settings[0]
	if s.Env != "DUSTPAN_ALLOW_PLAIN_HTTP" || s.File != cli.AllowPlainHTTPFlag || s.Default != "false" ||
		s.Kind != cli.KindBool {
		t.Fatalf("setting = %+v, want the variable DUSTPAN_ALLOW_PLAIN_HTTP, the file key, default false, a bool", s)
	}

	found := false

	for _, serve := range cli.ServeFlags() {
		if serve.Flag == cli.AllowPlainHTTPFlag {
			found = true
		}
	}

	if !found {
		t.Fatalf("ServeFlags does not hold --%s", cli.AllowPlainHTTPFlag)
	}
}
