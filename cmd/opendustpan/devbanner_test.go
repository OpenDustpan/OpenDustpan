package main

import (
	"strings"
	"testing"
	"time"
)

func baseBanner() devBanner {
	return devBanner{
		tlsExpiry: time.Time{},
		dir:       "/home/alex/.local/share/opendustpan/dev",
		serverURL: "http://127.0.0.1:8080",
		signIn:    "http://127.0.0.1:8080/sign-in#code=CODE   (first start on a terminal only, works one time, for 10 minutes)",
		envFile:   "/home/alex/.local/share/opendustpan/dev/dev.env",
		network:   "",
		tokenNote: "",
		ephemeral: false,
		trustStep: false,
	}
}

func TestWriteDevBanner(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, time.November, 18, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mutate func(b *devBanner)
		want   []string
		refuse []string
	}{
		{
			name:   "the default banner follows the decided text",
			mutate: func(*devBanner) {},
			want: []string{
				"opendustpan dev: this server is for a trial or for CI. Do not use it in production.\n",
				"Data:     /home/alex/.local/share/opendustpan/dev\n",
				"Server:   http://127.0.0.1:8080\n",
				"Sign in:  http://127.0.0.1:8080/sign-in#code=CODE   (first start on a terminal only, works one time, for 10 minutes)\n",
				"Settings: . /home/alex/.local/share/opendustpan/dev/dev.env\n",
				"The key-encryption key is in the data directory. A backup of this directory holds the key.\n",
				"For production, run: opendustpan serve --help\n",
			},
			refuse: []string{"Network:", "TLS:", "Token:"},
		},
		{
			name:   "ephemeral says that a stop deletes the data",
			mutate: func(b *devBanner) { b.ephemeral = true },
			want: []string{
				"dev deletes this directory at stop, and with it the state that tracks the cloud resources of the trial",
			},
			refuse: nil,
		},
		{
			name:   "an address that is not loopback adds a line",
			mutate: func(b *devBanner) { b.network = "0.0.0.0:8080 is not a loopback address" },
			want:   []string{"Network:  0.0.0.0:8080 is not a loopback address\n"},
			refuse: nil,
		},
		{
			name:   "tls prints the expiry date",
			mutate: func(b *devBanner) { b.tlsExpiry = expiry },
			want:   []string{"TLS:      self-signed certificate, valid until 2026-11-18\n"},
			refuse: []string{"To trust it"},
		},
		{
			name:   "tls on a platform with no bundle names the guide",
			mutate: func(b *devBanner) { b.tlsExpiry = expiry; b.trustStep = true },
			want:   []string{"To trust it, see docs/guides/dev-server.md\n"},
			refuse: nil,
		},
		{
			name:   "a token note adds a line",
			mutate: func(b *devBanner) { b.tokenNote = "in the file /x/dev.token" },
			want:   []string{"Token:    in the file /x/dev.token\n"},
			refuse: nil,
		},
		{
			name:   "a path with a space is quoted in the settings line",
			mutate: func(b *devBanner) { b.envFile = "/home/a b/dev.env" },
			want:   []string{"Settings: . '/home/a b/dev.env'\n"},
			refuse: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			b := baseBanner()
			tt.mutate(&b)

			var out strings.Builder
			if err := writeDevBanner(&out, b); err != nil {
				t.Fatalf("writeDevBanner: %v", err)
			}

			checkText(t, out.String(), tt.want, tt.refuse)
		})
	}
}

// checkText fails for each text in want that the output lacks, and for each
// text in refuse that it holds.
func checkText(t *testing.T, output string, want, refuse []string) {
	t.Helper()

	for _, text := range want {
		if !strings.Contains(output, text) {
			t.Errorf("output does not hold %q:\n%s", text, output)
		}
	}

	for _, text := range refuse {
		if strings.Contains(output, text) {
			t.Errorf("output holds %q:\n%s", text, output)
		}
	}
}

func TestShellWord(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{in: "/a/b/dev.env", want: "/a/b/dev.env"},
		{in: "/a b/dev.env", want: "'/a b/dev.env'"},
		{in: "/a/$b", want: "'/a/$b'"},
		{in: "", want: ""},
	}

	for _, tt := range tests {
		if got := shellWord(tt.in); got != tt.want {
			t.Errorf("shellWord(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
