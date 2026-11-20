package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestServeCheckNamesTheFlag proves that check refuses a bad --listen and a
// bad --installation, before the database opens, with the flag, the
// environment name and the allowed form.
func TestServeCheckNamesTheFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"listen has no port", []string{"--listen", "notanaddr"}, []string{"--listen", "DUSTPAN_LISTEN", "host:port"}},
		{"listen is empty host and port", []string{"--listen", ":"}, []string{"--listen", "host:port"}},
		{
			"installation has a space and a bang",
			[]string{"--installation", "bad name!"},
			[]string{"--installation", "DUSTPAN_INSTALLATION", "letters, digits"},
		},
		{"installation starts with a dot", []string{"--installation", ".x"}, []string{"--installation"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			fs := newTestFlagSet(&f)
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}

			err := f.check()
			if err == nil {
				t.Fatal("check = nil, want an error")
			}

			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("check error %q does not hold %q", err, w)
				}
			}
		})
	}
}

// TestServeCheckAcceptsValidNetFlags proves that the new checks keep the
// values that the server accepts.
func TestServeCheckAcceptsValidNetFlags(t *testing.T) {
	t.Parallel()

	for _, listen := range []string{":8080", "127.0.0.1:8080", "[::1]:8080", "localhost:0"} {
		var f serveFlags

		fs := newTestFlagSet(&f)
		if err := fs.Parse([]string{"--listen", listen, "--installation", "eu-west.1_a"}); err != nil {
			t.Fatalf("parse flags: %v", err)
		}

		if err := f.check(); err != nil {
			t.Fatalf("check with --listen %q: %v, want no error", listen, err)
		}
	}
}

// TestServeRefusesAStrayArgument proves that serve names an argument that is
// not a flag, so a typo does not start the server with a default.
func TestServeRefusesAStrayArgument(t *testing.T) {
	t.Parallel()

	err := serveWith(t.Context(), []string{"extra-arg"}, io.Discard, false, time.Millisecond)
	if err == nil || !errors.Is(err, errServeArgument) || !strings.Contains(err.Error(), `"extra-arg"`) {
		t.Fatalf("serve extra-arg = %v, want errServeArgument that names the argument", err)
	}
}
