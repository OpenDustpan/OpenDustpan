package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// newTestAppLog builds the application log for raw and closes it when the
// test ends.
func newTestAppLog(t *testing.T, raw string, stderr *bytes.Buffer, lookup func(string) (string, bool)) (appLog, error) {
	t.Helper()

	app, err := newAppLog(strings.Fields(raw), slog.LevelInfo, "json", stderr, sinkEnv{lookup: lookup, ranges: nil})
	if err == nil {
		t.Cleanup(func() {
			// A cleanup runs after the test context ends.
			if closeErr := app.close(context.Background()); closeErr != nil {
				t.Errorf("close app log: %v", closeErr)
			}
		})
	}

	return app, err
}

func TestAppLogWithoutSinksWritesToStderr(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	app, err := newTestAppLog(t, "", &stderr, noEnv)
	if err != nil {
		t.Fatalf("new app log: %v", err)
	}

	app.logger.Info("hello")

	if !strings.Contains(stderr.String(), `"msg":"hello"`) {
		t.Fatalf("stderr: got %q, want the record", stderr.String())
	}
}

func TestAppLogWritesToStderrAndAFile(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	path := filepath.Join(t.TempDir(), "app.jsonl")

	app, err := newAppLog(
		[]string{"stderr", "file:" + path},
		slog.LevelInfo,
		"json",
		&stderr,
		sinkEnv{lookup: noEnv, ranges: nil},
	)
	if err != nil {
		t.Fatalf("new app log: %v", err)
	}

	app.logger.Info("both", slog.String("adapter", "pulumi"))
	app.logger.Debug("hidden")

	// close flushes the queue of the file sink.
	if closeErr := app.close(t.Context()); closeErr != nil {
		t.Fatalf("close app log: %v", closeErr)
	}

	if !strings.Contains(stderr.String(), `"msg":"both"`) {
		t.Fatalf("stderr: got %q, want the record", stderr.String())
	}

	data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if err != nil {
		t.Fatalf("read file sink: %v", err)
	}

	if !strings.Contains(string(data), `"msg":"both","adapter":"pulumi"`) || strings.Contains(string(data), "hidden") {
		t.Fatalf("file sink: got %q, want the info record only", data)
	}
}

func TestAppLogRefusesBadItems(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		raw  string
	}{
		{name: "blob has no store for the application log", raw: "blob:logs"},
		{name: "unknown kind", raw: "journald"},
		{name: "two file sinks without names", raw: "file:/tmp/a.jsonl file:/tmp/b.jsonl"},
		{name: "two sinks with one name", raw: "app=file:/tmp/a.jsonl app=syslog+udp://127.0.0.1:514"},
		{name: "webhook without the log secret", raw: "webhook:https://logs.example/hook"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer

			// The audit webhook secret does not serve a log webhook.
			lookup := envOf(map[string]string{webhookSecretEnvVar: "audit-only"})

			if _, err := newTestAppLog(t, tc.raw, &stderr, lookup); !errors.Is(err, errLogSink) {
				t.Fatalf("new app log %q: got %v, want a --log-sink error", tc.raw, err)
			}
		})
	}
}

func TestAppLogWebhookUsesTheLogSecret(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer

	lookup := envOf(map[string]string{logWebhookSecretEnvVar: "log-secret"})

	app, err := newTestAppLog(t, "siem=webhook:https://logs.example/hook", &stderr, lookup)
	if err != nil {
		t.Fatalf("new app log: %v", err)
	}

	if len(app.handlers) != 1 || len(app.sinks) != 1 {
		t.Fatalf("handlers %d, sinks %d: want 1 each", len(app.handlers), len(app.sinks))
	}
}

func TestDropCounterKeepsCountsUntilAttach(t *testing.T) {
	t.Parallel()

	d := newDropCounter()
	d.counter("syslog")(3)
	d.counter("syslog")(2)

	var (
		mu  sync.Mutex
		got = map[string]int{}
	)

	d.attach(func(sink string, n int) {
		mu.Lock()
		defer mu.Unlock()

		got[sink] += n
	})

	d.counter("file")(1)

	if got["syslog"] != 5 || got["file"] != 1 {
		t.Fatalf("reported drops: got %v, want syslog 5 and file 1", got)
	}
}

//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeWritesTheApplicationLogToAFileSink(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	dir := t.TempDir()
	path := filepath.Join(dir, "app.jsonl")

	args := []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--listen", "127.0.0.1:0",
		"--log-sink", "stderr",
		"--log-sink", "file:" + path,
	}

	// startServer waits for the listening line on stderr, then stops serve;
	// the stop flushes the file sink.
	if err := startServer(t, args...); err != nil {
		t.Fatalf("serve with a file log sink: %v", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // The path is a test temp file.
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}

	if !strings.Contains(string(data), `"msg":"listening"`) {
		t.Fatalf("log file lacks the listening line: %q", data)
	}
}
