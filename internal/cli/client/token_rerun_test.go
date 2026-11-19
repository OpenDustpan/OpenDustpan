package client_test

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// TestMissingTokenNamesTheFailedCommand checks that the no-token error of each
// client command names that command and not the backup command.
func TestMissingTokenNamesTheFailedCommand(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "")
	t.Setenv(client.AdminTokenFileEnvVar, "")

	const server = "http://127.0.0.1:9"

	cases := []struct {
		name string
		run  func(ctx context.Context, stdout, stderr io.Writer) error
		want string
	}{
		{
			name: "store check",
			run: func(ctx context.Context, stdout, stderr io.Writer) error {
				return client.StoreCheck(ctx, []string{"--server", server, "--target", "blobs"}, stdout, stderr)
			},
			want: "run opendustpan store check --target blobs again",
		},
		{
			name: "audit-chain status",
			run: func(ctx context.Context, stdout, stderr io.Writer) error {
				return client.AuditChainStatus(ctx, []string{"--server", server}, stdout, stderr)
			},
			want: "run opendustpan audit-chain status again",
		},
		{
			name: "backup check",
			run: func(ctx context.Context, stdout, stderr io.Writer) error {
				return client.BackupCheck(ctx, []string{"--server", server, "--backup", "bkp-aaa"}, stdout, stderr)
			},
			want: "run opendustpan backup check again",
		},
		{
			name: "upgrade check",
			run: func(ctx context.Context, stdout, stderr io.Writer) error {
				return client.UpgradeCheck(ctx, []string{"--server", server}, stdout, stderr, "v0.0.0")
			},
			want: "run opendustpan upgrade check again",
		},
		{
			name: "doctor",
			run: func(ctx context.Context, stdout, stderr io.Writer) error {
				return client.Doctor(ctx, []string{"--server", server}, stdout, stderr, "v0.0.0")
			},
			want: "run opendustpan doctor again",
		},
	}

	for _, tc := range cases { //nolint:paralleltest // The test sets an environment variable with t.Setenv.
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			err := tc.run(t.Context(), &stdout, &stderr)

			text := stdout.String() + stderr.String()
			if err != nil {
				text += err.Error()
			}

			if !strings.Contains(text, tc.want) {
				t.Fatalf("output = %q, want it to contain %q", text, tc.want)
			}

			if tc.name != "backup check" && strings.Contains(text, "run opendustpan backup again") {
				t.Fatalf("output = %q, want no mention of the backup command", text)
			}
		})
	}
}

// TestStoreCheckHelpNeedsNoToken checks that --help prints usage when no
// admin token is set.
func TestStoreCheckHelpNeedsNoToken(t *testing.T) {
	t.Setenv(client.AdminTokenEnvVar, "")
	t.Setenv(client.AdminTokenFileEnvVar, "")

	var stdout, stderr bytes.Buffer

	err := client.StoreCheck(t.Context(), []string{"--help"}, &stdout, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("store check --help error = %v, want flag.ErrHelp", err)
	}

	if !strings.Contains(stderr.String(), "-target") {
		t.Fatalf("store check --help printed %q, want the usage with -target", stderr.String())
	}
}
