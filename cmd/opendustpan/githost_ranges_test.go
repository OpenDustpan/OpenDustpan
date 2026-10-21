package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
)

// TestServeRefusesHostileGitHostRanges sends a range that opens the loopback
// interface, the link-local interface, the metadata address or the whole
// network to the check of the flags, as the flag, as the environment variable
// and as the key of the configuration file. Each source must fail the start
// with the check of this layer, whatever the layers behind it do.
//
//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestServeRefusesHostileGitHostRanges(t *testing.T) {
	ranges := []string{
		"0.0.0.0/0", "::/0", "::ffff:0:0/96", "127.0.0.0/8", "127.0.0.1/32", "169.254.0.0/16", "169.254.169.254/32",
		"fe80::/32", "fd00:ec2::254/128", "10.0.0.0/7", "10.0.0.0/8,0.0.0.0/0",
	}

	sources := []struct {
		name string
		run  func(ctx context.Context, t *testing.T, list string) error
	}{
		{"flag", func(ctx context.Context, t *testing.T, list string) error {
			t.Helper()

			return serve(ctx, append(serveBase(t), "--git-host-allowed-ranges", list), io.Discard)
		}},
		{"environment variable", func(ctx context.Context, t *testing.T, list string) error {
			t.Helper()
			t.Setenv("DUSTPAN_GIT_HOST_ALLOWED_RANGES", list)

			return serve(ctx, serveBase(t), io.Discard)
		}},
		{"configuration file", func(ctx context.Context, t *testing.T, list string) error {
			t.Helper()

			config := writeConfig(t, "git-host-allowed-ranges: \""+list+"\"\n")

			return serve(ctx, append(serveBase(t), "--config", config), io.Discard)
		}},
	}

	// A range that the check lets through starts the server. The deadline turns
	// that into a failed test and not a test that never ends.
	const deadline = 30 * time.Second

	for _, src := range sources {
		for _, list := range ranges {
			t.Run(src.name+" "+list, func(t *testing.T) { //nolint:paralleltest // The subtest sets process environment.
				setServeEnv(t, testKeyBase64())

				ctx, cancel := context.WithTimeout(t.Context(), deadline)
				defer cancel()

				err := src.run(ctx, t, list)
				if !errors.Is(err, egress.ErrRangeTooWide) {
					t.Fatalf("serve with %q from the %s: got %v, want ErrRangeTooWide", list, src.name, err)
				}

				if !strings.Contains(err.Error(), "--git-host-allowed-ranges") {
					t.Fatalf("serve error %q does not name the flag", err)
				}
			})
		}
	}
}

func serveBase(t *testing.T) []string {
	t.Helper()

	return []string{"--database", "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")}
}
