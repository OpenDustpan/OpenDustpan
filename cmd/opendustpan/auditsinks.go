package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/logsink"
	"opendustpan.org/opendustpan/pkg/logsink/file"
	"opendustpan.org/opendustpan/pkg/logsink/objectstore"
	"opendustpan.org/opendustpan/pkg/logsink/otlp"
	"opendustpan.org/opendustpan/pkg/logsink/syslog"
	"opendustpan.org/opendustpan/pkg/logsink/webhook"
)

// Environment variables of the webhook sink secret. No flag takes the
// secret, because flags show in process lists. They hold the names of the
// variables, not a value.
const (
	webhookSecretEnvVar     = "DUSTPAN_AUDIT_WEBHOOK_SECRET"      //nolint:gosec // The name of a variable, not a credential.
	webhookSecretFileEnvVar = "DUSTPAN_AUDIT_WEBHOOK_SECRET_FILE" //nolint:gosec // The name of a variable, not a credential.
)

// Environment variables of the OTLP sink headers. They hold the names of
// the variables, not a value. The headers can hold a credential, so no flag
// takes them.
const (
	otlpHeadersEnvVar        = "DUSTPAN_AUDIT_OTLP_HEADERS"
	otlpHeadersFileEnvVar    = "DUSTPAN_AUDIT_OTLP_HEADERS_FILE"
	logOTLPHeadersEnvVar     = "DUSTPAN_LOG_OTLP_HEADERS"
	logOTLPHeadersFileEnvVar = "DUSTPAN_LOG_OTLP_HEADERS_FILE"
)

// Kinds of an --audit-sink item: the text before the first ":" or "+".
const (
	sinkKindFile    = "file"
	sinkKindSyslog  = "syslog"
	sinkKindWebhook = "webhook"
	sinkKindOTLP    = "otlp"
	sinkKindBlob    = "blob"
)

// blobSinkInterval is the time between two rounds of a blob sink: each
// round writes one object, so a longer interval gives fewer, larger files.
// The other sinks use audit.DefaultShipInterval.
const blobSinkInterval = time.Minute

// errAuditSink reports an --audit-sink item that serve cannot use.
var errAuditSink = errors.New("--audit-sink")

// openedSinks holds the audit sinks that serve opened, and the file sinks
// among them, which SIGHUP opens again.
type openedSinks struct {
	ship  []audit.ShipSink
	files []*file.Sink
}

// sinkConfig holds what openSink needs beyond the spec: the setting that
// names the sink (for errors), the live blob store (nil refuses blob:), the
// default syslog facility, the variables of the webhook secret and the
// variables of the OTLP headers.
type sinkConfig struct {
	setting        error
	blobs          blob.Store
	lookup         func(string) (string, bool)
	secretEnv      string
	secretFileEnv  string
	headersEnv     string
	headersFileEnv string
	facility       int
}

// auditSinkConfig answers the sink configuration of --audit-sink.
func auditSinkConfig(blobs blob.Store, lookup func(string) (string, bool)) sinkConfig {
	return sinkConfig{
		setting:        errAuditSink,
		blobs:          blobs,
		lookup:         lookup,
		secretEnv:      webhookSecretEnvVar,
		secretFileEnv:  webhookSecretFileEnvVar,
		headersEnv:     otlpHeadersEnvVar,
		headersFileEnv: otlpHeadersFileEnvVar,
		facility:       syslog.FacilityLogAudit,
	}
}

// openAuditSinks opens the sink of each --audit-sink item. An item is
// [<name>=]<spec>. The flag repeats, and the variable separates items with
// white space, so a webhook URL can hold a comma. The name keys the cursor of the sink; without
// one, the kind is the name (file, syslog, webhook or blob), so two sinks
// of one kind need names. blobs is the live blob store, which blob: sinks
// write to. On error it closes every sink it opened.
func openAuditSinks(items []string, blobs blob.Store, lookup func(string) (string, bool)) (openedSinks, error) {
	var out openedSinks

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		named := splitSinkName(item)
		name, spec := named.name, named.spec

		sink, interval, err := openSink(spec, auditSinkConfig(blobs, lookup))
		if err != nil {
			return openedSinks{}, errors.Join(err, out.close())
		}

		if name == "" {
			name = sinkKind(spec)
		}

		if f, ok := sink.(*file.Sink); ok {
			out.files = append(out.files, f)
		}

		out.ship = append(out.ship, audit.ShipSink{Sink: sink, Name: name, Kind: sinkKind(spec), Interval: interval})
	}

	return out, nil
}

// sinkItem is one --audit-sink item: the name before "=", when the item
// has one, and the spec.
type sinkItem struct {
	name string
	spec string
}

// splitSinkName splits "<name>=<spec>" into its parts. An item whose text
// before the first "=" holds ":" or "/" has no name: the "=" belongs to the
// spec.
func splitSinkName(item string) sinkItem {
	before, after, found := strings.Cut(item, "=")
	if !found || strings.ContainsAny(before, ":/") {
		return sinkItem{name: "", spec: item}
	}

	return sinkItem{name: before, spec: after}
}

// sinkKind answers the kind of spec: the text before the first ":" or "+".
func sinkKind(spec string) string {
	end := strings.IndexAny(spec, ":+")
	if end < 0 {
		return spec
	}

	return spec[:end]
}

// openSink opens the sink of one spec and answers its ship interval.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openSink(spec string, cfg sinkConfig) (logsink.Sink, time.Duration, error) {
	switch kind := sinkKind(spec); kind {
	case sinkKindFile:
		s, err := file.Open(strings.TrimPrefix(spec, sinkKindFile+":"))
		if err != nil {
			return nil, 0, fmt.Errorf("%w %s: %w", cfg.setting, spec, err)
		}

		return s, 0, nil
	case sinkKindSyslog:
		s, err := openSyslogSink(spec, cfg)

		return s, 0, err
	case sinkKindWebhook:
		s, err := openWebhookSink(strings.TrimPrefix(spec, sinkKindWebhook+":"), cfg)

		return s, 0, err
	case sinkKindOTLP:
		s, err := openOTLPSink(strings.TrimPrefix(spec, sinkKindOTLP+":"), cfg)

		return s, 0, err
	case sinkKindBlob:
		return openBlobSink(spec, cfg)
	default:
		return nil, 0, fmt.Errorf("%w %q: unknown sink kind %q: want file, syslog, webhook, otlp or blob",
			cfg.setting, spec, kind)
	}
}

// openBlobSink opens a blob:<prefix> sink in the live blob store.
//
//nolint:ireturn // The adapter is chosen at runtime, so the result must be an interface.
func openBlobSink(spec string, cfg sinkConfig) (logsink.Sink, time.Duration, error) {
	if cfg.blobs == nil {
		return nil, 0, fmt.Errorf("%w %s: %w", cfg.setting, spec, errNoBlobSink)
	}

	prefix := strings.TrimPrefix(spec, sinkKindBlob+":")

	// The state sweepers delete unknown objects under namespaces and pulumi,
	// and the checkpoint check reads every object under audit-checkpoints, so
	// an object there can fail the check.
	first, _, _ := strings.Cut(prefix, "/")
	if first == "namespaces" || first == "pulumi" || first == audit.CheckpointPrefix {
		return nil, 0, fmt.Errorf("%w %s: %w", cfg.setting, spec, errReservedPrefix)
	}

	s, err := objectstore.New(cfg.blobs, prefix, objectstore.WithPartition(audit.RecordScope))
	if err != nil {
		return nil, 0, fmt.Errorf("%w %s: %w", cfg.setting, spec, err)
	}

	return s, blobSinkInterval, nil
}

// openSyslogSink opens a syslog+udp://, syslog+tcp:// or syslog+tls://
// sink. The query takes facility (0 to 23, default from cfg), max-size
// (the message cap in bytes, 480 to 65507 over UDP and 480 to 65536 over
// TCP and TLS, default 8192 over UDP and 65536 over TCP and TLS) and, for
// TLS, ca-file: a PEM bundle to trust instead of the system store.
func openSyslogSink(spec string, cfg sinkConfig) (*syslog.Sink, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", cfg.setting, spec, err)
	}

	network := syslog.Network(strings.TrimPrefix(u.Scheme, sinkKindSyslog+"+"))

	facility := cfg.facility
	if raw := u.Query().Get("facility"); raw != "" {
		facility, err = strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%w %s: facility: %w", cfg.setting, spec, err)
		}
	}

	maxBytes := 0
	if raw := u.Query().Get("max-size"); raw != "" {
		maxBytes, err = strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%w %s: max-size: %w", cfg.setting, spec, err)
		}
	}

	conf := syslog.Config{
		TLSConfig: nil,
		Network:   network,
		Address:   u.Host,
		Hostname:  "",
		AppName:   "",
		Facility:  facility,
		Timeout:   0,

		MaxMessageBytes: maxBytes,
	}

	if network == syslog.TLS {
		conf.TLSConfig, err = syslogTLSConfig(u)
		if err != nil {
			return nil, fmt.Errorf("%w %s: %w", cfg.setting, spec, err)
		}
	}

	s, err := syslog.New(conf)
	if err != nil {
		return nil, fmt.Errorf("%w %s: %w", cfg.setting, spec, err)
	}

	return s, nil
}

// syslogTLSConfig answers the TLS client configuration of a syslog+tls://
// sink: the host of u as the server name, and the CA bundle of ca-file when
// set, else the system store.
func syslogTLSConfig(u *url.URL) (*tls.Config, error) {
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, fmt.Errorf("address: %w", err)
	}

	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host}

	caFile := u.Query().Get("ca-file")
	if caFile == "" {
		return cfg, nil
	}

	pem, err := os.ReadFile(caFile) //nolint:gosec // The operator names the CA file in the server settings.
	if err != nil {
		return nil, fmt.Errorf("ca-file: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("ca-file %s: %w", caFile, errNoCertificates)
	}

	cfg.RootCAs = pool

	return cfg, nil
}

// Errors of the sink settings.
var (
	// errNoCertificates reports a CA file with no PEM certificate.
	errNoCertificates = errors.New("holds no PEM certificate")
	// errNoBlobSink reports a blob: sink for a setting that has no blob store.
	errNoBlobSink = errors.New("this setting has no blob store: pick file, syslog, webhook or otlp")
	// errReservedPrefix reports a blob sink prefix that the server owns.
	errReservedPrefix = errors.New(
		"the prefixes namespaces, pulumi and audit-checkpoints belong to the server: pick another prefix",
	)
	// errSecretFilePermissions reports a secret file that group or others can read.
	errSecretFilePermissions = errors.New("secret file allows group or other access")
	// errSecretControlChar reports a secret with a control character.
	errSecretControlChar = errors.New("the secret holds a control character")
)

// openWebhookSink opens a webhook sink on rawURL with the secret of the
// variable cfg.secretEnv or of the file that cfg.secretFileEnv names.
func openWebhookSink(rawURL string, cfg sinkConfig) (*webhook.Sink, error) {
	secret, err := webhookSecretFrom(cfg.lookup, cfg.secretEnv, cfg.secretFileEnv)
	if err != nil {
		return nil, fmt.Errorf("%w webhook: %w", cfg.setting, err)
	}

	s, err := webhook.New(webhook.Config{Client: nil, URL: rawURL, Secret: secret, Timeout: 0})
	if err != nil {
		return nil, fmt.Errorf("%w webhook: %w", cfg.setting, err)
	}

	return s, nil
}

// openOTLPSink opens an otlp:<https URL> sink. The headers come from the
// variable cfg.headersEnv or from the file that cfg.headersFileEnv names. A
// sink with no headers is valid, because a collector in a private network
// can need none.
func openOTLPSink(rawURL string, cfg sinkConfig) (*otlp.Sink, error) {
	headers, err := otlpHeadersFrom(cfg.lookup, cfg.headersEnv, cfg.headersFileEnv)
	if err != nil {
		return nil, fmt.Errorf("%w otlp: %w", cfg.setting, err)
	}

	s, err := otlp.New(otlp.Config{
		Client: nil, Headers: headers, URL: rawURL, ServiceName: "", Timeout: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("%w otlp: %w", cfg.setting, err)
	}

	return s, nil
}

// Errors of the OTLP header settings. None of them holds a header value.
var (
	// errNoOTLPHeaders reports that neither header variable holds a value.
	errNoOTLPHeaders = errors.New("no OTLP headers are set")
	// errOTLPHeaderItem reports a header item that is not Name=Value.
	errOTLPHeaderItem = errors.New("each OTLP header is Name=Value, with the value percent-encoded")
	// errOTLPHeaderTwice reports a header name that the settings give twice.
	errOTLPHeaderTwice = errors.New("an OTLP header name is set twice")
)

// otlpHeadersFrom reads the OTLP headers from the variable env, else from
// the file that the variable fileEnv names. The file must allow no group or
// other access. It answers no headers when neither variable holds a value.
// The text is a list of Name=Value items, separated by commas or line
// breaks, as OTEL_EXPORTER_OTLP_HEADERS writes it. A value is
// percent-encoded, so it can hold a comma or an equals sign. The errors name
// the position of an item, never its text, because a value can be a secret.
func otlpHeadersFrom(lookup func(string) (string, bool), env, fileEnv string) (map[string]string, error) {
	raw, _, err := secretTextFrom(lookup, env, fileEnv, errNoOTLPHeaders)
	if errors.Is(err, errNoOTLPHeaders) {
		return map[string]string{}, nil
	}

	if err != nil {
		return nil, err
	}

	headers := map[string]string{}

	items := strings.FieldsFunc(string(raw), func(r rune) bool { return r == ',' || r == '\n' || r == '\r' })
	for i, item := range items {
		name, value, found := strings.Cut(item, "=")

		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf("item %d: %w", i+1, errOTLPHeaderItem)
		}

		decoded, decodeErr := url.PathUnescape(strings.TrimSpace(value))
		if decodeErr != nil {
			// The error of PathUnescape repeats a part of the value.
			return nil, fmt.Errorf("item %d (%s): %w", i+1, name, errOTLPHeaderItem)
		}

		if _, dup := headers[strings.ToLower(name)]; dup {
			return nil, fmt.Errorf("item %d (%s): %w", i+1, name, errOTLPHeaderTwice)
		}

		headers[strings.ToLower(name)] = decoded
	}

	return headers, nil
}

// errNoWebhookSecret reports a webhook sink without a secret.
var errNoWebhookSecret = errors.New("a webhook sink needs its secret variable or its secret file variable")

// webhookSecret reads the secret of a webhook audit sink.
func webhookSecret(lookup func(string) (string, bool)) ([]byte, error) {
	return webhookSecretFrom(lookup, webhookSecretEnvVar, webhookSecretFileEnvVar)
}

// webhookSecretFrom reads a webhook secret from the variable env, or from
// the file that the variable fileEnv names. The variable wins when both
// are set. The file must not be readable by group or others.
func webhookSecretFrom(lookup func(string) (string, bool), env, fileEnv string) ([]byte, error) {
	return secretFrom(lookup, env, fileEnv, errNoWebhookSecret)
}

// secretFrom reads a secret from the variable env, else from the file that
// the variable fileEnv names. The file must allow no group or other
// access. A missing or empty secret answers errMissing. A secret with a
// control character is refused.
func secretFrom(lookup func(string) (string, bool), env, fileEnv string, errMissing error) ([]byte, error) {
	secret, source, err := secretTextFrom(lookup, env, fileEnv, errMissing)
	if err != nil {
		return nil, err
	}

	// Defense in depth: the start reads the real environment; this check
	// stays so that a snapshot value or a broken file cannot become a
	// secret.
	if bytes.IndexFunc(secret, isControlRune) >= 0 {
		return nil, fmt.Errorf("%s: %w", source, errSecretControlChar)
	}

	return secret, nil
}

// secretTextFrom reads the text of a secret from the variable env, else
// from the file that the variable fileEnv names. It answers the text and
// the variable that gave it. The file must allow no group or other access.
// A missing or empty secret answers errMissing.
func secretTextFrom(
	lookup func(string) (string, bool), env, fileEnv string, errMissing error,
) ([]byte, string, error) {
	if raw, ok := lookup(env); ok && strings.TrimSpace(raw) != "" {
		return []byte(strings.TrimSpace(raw)), env, nil
	}

	path, ok := lookup(fileEnv)
	if !ok || strings.TrimSpace(path) == "" {
		return nil, "", fmt.Errorf("%w: set %s or %s", errMissing, env, fileEnv)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("%s: stat file: %w", fileEnv, err)
	}

	if perm := info.Mode().Perm(); perm&keyFilePrivateBits != 0 {
		return nil, "", fmt.Errorf("%s: %w %q (mode %04o): run chmod 600 on the file",
			fileEnv, errSecretFilePermissions, path, perm)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the secret file through the environment.
	if err != nil {
		return nil, "", fmt.Errorf("%s: read file: %w", fileEnv, err)
	}

	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return nil, "", fmt.Errorf("%w: %s is empty", errMissing, fileEnv)
	}

	return []byte(secret), fileEnv, nil
}

// isControlRune reports a control character: a rune below 0x20 or 0x7f.
func isControlRune(r rune) bool {
	return r < 0x20 || r == 0x7f
}

// close closes every opened sink and answers the joined errors.
func (o openedSinks) close() error {
	var errs []error

	for _, s := range o.ship {
		if err := s.Sink.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close audit sink %s: %w", s.Name, err))
		}
	}

	return errors.Join(errs...)
}

// reopenOnHangup opens each file sink again on SIGHUP until ctx is done, so
// an external rotation tool can move the files away. extra holds the file
// sinks of the application log. The returned function stops the watch and
// waits for it. With no file sink it watches nothing.
func (o openedSinks) reopenOnHangup(ctx context.Context, logger *slog.Logger, extra []*file.Sink) func() {
	files := append(append([]*file.Sink(nil), o.files...), extra...)
	if len(files) == 0 {
		return func() {}
	}

	hangup := make(chan os.Signal, 1)
	signal.Notify(hangup, syscall.SIGHUP)

	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			select {
			case <-watchCtx.Done():
				return
			case <-hangup:
				reopenFiles(watchCtx, logger, files)
			}
		}
	}()

	return func() {
		signal.Stop(hangup)
		cancel()
		<-done
	}
}

// reopenFiles opens each file sink again and logs a failure. A sink whose
// open fails keeps its old file.
func reopenFiles(ctx context.Context, logger *slog.Logger, files []*file.Sink) {
	for _, f := range files {
		if err := f.Reopen(); err != nil {
			logger.WarnContext(ctx, "reopen audit sink file", slog.String("error", err.Error()))
		}
	}
}
