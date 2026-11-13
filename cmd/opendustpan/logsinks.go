package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/slogsink"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
)

// Environment variables of the secret of a webhook log sink. No flag takes
// the secret, because flags show in process lists. They hold the names of
// the variables, not a value.
const (
	logWebhookSecretEnvVar     = "DUSTPAN_LOG_WEBHOOK_SECRET"      //nolint:gosec // The name of a variable, not a credential.
	logWebhookSecretFileEnvVar = "DUSTPAN_LOG_WEBHOOK_SECRET_FILE" //nolint:gosec // The name of a variable, not a credential.
)

// The standard streams that --log-sink can name.
const (
	logSinkStderr = "stderr"
	logSinkStdout = "stdout"
)

// logFlushTimeout bounds the flush of the log sinks when serve stops.
const logFlushTimeout = 5 * time.Second

// errLogSink reports a --log-sink item that serve cannot use.
var errLogSink = errors.New("--log-sink")

// appLog is the application logger of serve and the sinks behind it.
type appLog struct {
	logger   *slog.Logger
	drops    *dropCounter
	handlers []*slogsink.Handler
	sinks    []logsink.Sink
	files    []*file.Sink
}

// sinkEnv holds what a log sink reads beyond its item: the variables of its
// secrets and headers, and the egress ranges that the operator allows.
type sinkEnv struct {
	lookup func(string) (string, bool)
	ranges []netip.Prefix
}

// newAppLog builds the application logger from the --log-sink items. No
// item writes to stderr, as serve always did.
// An item is [<name>=]<sink>: stderr, stdout, file:<path>,
// syslog+udp|tcp|tls://<host>:<port>, webhook:<https URL> or
// otlp:<https URL>. The name
// labels the drop metric; without one, the kind is the name. Each sink
// other than a stream is best effort: a slow or failed sink drops records
// and never holds a request. On error it closes every sink it opened.
func newAppLog(items []string, level slog.Level, format string, stderr io.Writer, env sinkEnv) (appLog, error) {
	out := appLog{logger: nil, drops: newDropCounter(), handlers: nil, sinks: nil, files: nil}

	var handlers []slog.Handler

	opts := &slog.HandlerOptions{Level: level}

	// seen holds the names of the sinks so far: a name labels the drop
	// metric, so two sinks with one name would share a count.
	seen := map[string]bool{}

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		named := splitSinkName(item)

		switch named.spec {
		case logSinkStderr:
			handlers = append(handlers, streamHandler(stderr, format, opts))

			continue
		case logSinkStdout:
			handlers = append(handlers, streamHandler(os.Stdout, format, opts))

			continue
		default:
		}

		h, err := out.openSinkHandler(named, level, env, seen)
		if err != nil {
			return appLog{}, errors.Join(err, out.close(context.Background()))
		}

		handlers = append(handlers, h)
	}

	switch len(handlers) {
	case 0:
		out.logger = slog.New(streamHandler(stderr, format, opts))
	case 1:
		out.logger = slog.New(handlers[0])
	default:
		out.logger = slog.New(slog.NewMultiHandler(handlers...))
	}

	return out, nil
}

// openSinkHandler opens the sink of one item and wraps it in a queued
// handler that counts its drops under the name of the item.
func (a *appLog) openSinkHandler(
	named sinkItem, level slog.Level, env sinkEnv, seen map[string]bool,
) (*slogsink.Handler, error) {
	name := named.name
	if name == "" {
		name = sinkKind(named.spec)
	}

	if seen[name] {
		return nil, fmt.Errorf("%w: two log sinks are named %q: give each a name, for example app=file:<path>",
			errLogSink, name)
	}

	seen[name] = true

	sink, _, err := openSink(named.spec, sinkConfig{
		setting:        errLogSink,
		blobs:          nil,
		lookup:         env.lookup,
		secretEnv:      logWebhookSecretEnvVar,
		secretFileEnv:  logWebhookSecretFileEnvVar,
		headersEnv:     logOTLPHeadersEnvVar,
		headersFileEnv: logOTLPHeadersFileEnvVar,
		facility:       syslog.FacilityLocal0,
		rules:          sinkRules(env.ranges),
		options:        egresshttp.Options{RootCAs: nil, Resolver: nil},
	})
	if err != nil {
		return nil, err
	}

	h := slogsink.New(sink, slogsink.Options{
		Level:         level,
		OnDrop:        a.drops.counter(name),
		QueueSize:     0,
		BatchSize:     0,
		FlushInterval: 0,
		WriteTimeout:  0,
	})

	a.sinks = append(a.sinks, sink)
	a.handlers = append(a.handlers, h)

	if f, ok := sink.(*file.Sink); ok {
		a.files = append(a.files, f)
	}

	return h, nil
}

// streamHandler answers the JSON handler on w, or the text handler when
// format is text.
func streamHandler(w io.Writer, format string, opts *slog.HandlerOptions) slog.Handler {
	if normalizeLogLevel(format) == "text" {
		return slog.NewTextHandler(w, opts)
	}

	return slog.NewJSONHandler(w, opts)
}

// close flushes each queued handler within ctx and logFlushTimeout, then
// closes the sinks. It answers the joined errors.
func (a *appLog) close(ctx context.Context) error {
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), logFlushTimeout)
	defer cancel()

	var errs []error

	for _, h := range a.handlers {
		if err := h.Close(flushCtx); err != nil {
			errs = append(errs, err)
		}
	}

	for _, s := range a.sinks {
		if err := s.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close log sink: %w", err))
		}
	}

	return errors.Join(errs...)
}

// dropCounter counts the records each log sink dropped. The logger starts
// before the server and its metrics, so the counter keeps the counts until
// attach hands them to the metrics, and reports later drops at once.
type dropCounter struct {
	report  func(sink string, records int)
	pending map[string]int
	mu      sync.Mutex
}

func newDropCounter() *dropCounter {
	return &dropCounter{report: nil, pending: map[string]int{}, mu: sync.Mutex{}}
}

// counter answers the drop hook of the sink named name.
func (d *dropCounter) counter(name string) func(int) {
	return func(n int) {
		d.mu.Lock()
		defer d.mu.Unlock()

		if d.report != nil {
			d.report(name, n)

			return
		}

		d.pending[name] += n
	}
}

// attach sends the counts so far to report, and every later drop.
func (d *dropCounter) attach(report func(sink string, records int)) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for name, n := range d.pending {
		report(name, n)
	}

	d.pending = map[string]int{}
	d.report = report
}
