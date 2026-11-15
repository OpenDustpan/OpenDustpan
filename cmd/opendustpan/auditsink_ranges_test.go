package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
)

// TestServeRefusesHostileAuditSinkRanges sends a range that opens the
// loopback interface, the metadata address or the whole network to the check
// of the flags, as the flag and as the key of the configuration file. Each
// source must fail the start with the check of this layer.
func TestServeRefusesHostileAuditSinkRanges(t *testing.T) {
	t.Parallel()

	ranges := []string{
		"0.0.0.0/0", "::/0", "10.0.0.0/7", "127.0.0.0/8", "169.254.0.0/16", "fd00:ec2::254/128", "intranet",
	}

	sources := []struct {
		name string
		args func(t *testing.T, list string) []string
	}{
		{"flag", func(t *testing.T, list string) []string {
			t.Helper()

			return append(serveBase(t), "--audit-sink-allowed-ranges", list)
		}},
		{"configuration file", func(t *testing.T, list string) []string {
			t.Helper()

			config := writeConfig(t, "audit-sink-allowed-ranges: \""+list+"\"\n")

			return append(serveBase(t), "--config", config)
		}},
	}

	for _, src := range sources {
		for _, list := range ranges {
			t.Run(src.name+" "+list, func(t *testing.T) {
				t.Parallel()

				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()

				err := serve(ctx, src.args(t, list), io.Discard)
				if !errors.Is(err, egress.ErrRangeTooWide) && !errors.Is(err, egress.ErrRange) {
					t.Fatalf("serve with %q from the %s: got %v, want a range refusal", list, src.name, err)
				}

				if !strings.Contains(err.Error(), "--audit-sink-allowed-ranges") {
					t.Fatalf("serve error %q does not name the flag", err)
				}
			})
		}
	}
}

// TestServeParsesTheAuditSinkRanges proves that the check parses the ranges
// once and keeps them for the sinks and the settings API.
func TestServeParsesTheAuditSinkRanges(t *testing.T) {
	t.Parallel()

	var f serveFlags

	fs := newTestFlagSet(&f)

	if err := fs.Parse([]string{"--audit-sink-allowed-ranges", "10.20.0.0/16, 192.168.0.0/16"}); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	if err := f.check(); err != nil {
		t.Fatalf("check: %v", err)
	}

	want := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16"), netip.MustParsePrefix("192.168.0.0/16")}
	if !slices.Equal(f.auditSinkRanges, want) {
		t.Fatalf("ranges = %v, want %v", f.auditSinkRanges, want)
	}
}

// TestAuditSinkRangesOpenTheWebhook proves that an allowed private range
// opens a webhook sink on that range, and that the same URL without the range
// is refused.
func TestAuditSinkRangesOpenTheWebhook(t *testing.T) {
	t.Parallel()

	lookup := envOf(map[string]string{webhookSecretEnvVar: "s3cret"})
	ranges := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	const item = "webhook:https://10.20.0.5/hook"

	sinks, err := openAuditSinks([]string{item}, nil, lookup, ranges)
	if err != nil {
		t.Fatalf("open a webhook sink on an allowed range: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := sinks.close(); closeErr != nil {
			t.Errorf("close sinks: %v", closeErr)
		}
	})

	_, err = openAuditSinks([]string{item}, nil, lookup, nil)
	if !errors.Is(err, errAuditSink) || !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("open a webhook sink without a range: got %v, want a refusal", err)
	}
}

// TestAuditSinkRangesOpenTheLogSink proves that the same setting opens a
// webhook sink of the application log on a private range, and that the same
// URL without the range is refused.
func TestAuditSinkRangesOpenTheLogSink(t *testing.T) {
	t.Parallel()

	lookup := envOf(map[string]string{logWebhookSecretEnvVar: "s3cret"})
	ranges := []netip.Prefix{netip.MustParsePrefix("10.20.0.0/16")}
	const item = "webhook:https://10.20.0.5/hook"

	app, err := newAppLog([]string{item}, slog.LevelInfo, "json", io.Discard, sinkEnv{lookup: lookup, ranges: ranges})
	if err != nil {
		t.Fatalf("open a webhook log sink on an allowed range: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := app.close(t.Context()); closeErr != nil {
			t.Errorf("close log sinks: %v", closeErr)
		}
	})

	_, err = newAppLog([]string{item}, slog.LevelInfo, "json", io.Discard, sinkEnv{lookup: lookup, ranges: nil})
	if !errors.Is(err, errLogSink) || !errors.Is(err, egress.ErrRefused) {
		t.Fatalf("open a webhook log sink without a range: got %v, want a refusal", err)
	}
}
