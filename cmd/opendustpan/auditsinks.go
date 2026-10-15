package main

import (
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

// Kinds of an --audit-sink item: the text before the first ":" or "+".
const (
	sinkKindFile    = "file"
	sinkKindSyslog  = "syslog"
	sinkKindWebhook = "webhook"
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
// default syslog facility and the variables of the webhook secret.
type sinkConfig struct {
	setting       error
	blobs         blob.Store
	lookup        func(string) (string, bool)
	secretEnv     string
	secretFileEnv string
	facility      int
}

// auditSinkConfig answers the sink configuration of --audit-sink.
func auditSinkConfig(blobs blob.Store, lookup func(string) (string, bool)) sinkConfig {
	return sinkConfig{
		setting:       errAuditSink,
		blobs:         blobs,
		lookup:        lookup,
		secretEnv:     webhookSecretEnvVar,
		secretFileEnv: webhookSecretFileEnvVar,
		facility:      syslog.FacilityLogAudit,
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
	case sinkKindBlob:
		return openBlobSink(spec, cfg)
	default:
		return nil, 0, fmt.Errorf("%w %q: unknown sink kind %q: want file, syslog, webhook or blob",
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

	// The state sweepers delete unknown objects under these prefixes.
	if first, _, _ := strings.Cut(prefix, "/"); first == "tofu" || first == "pulumi" {
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
// (the message cap in bytes, 480 to 65507, default 8192) and, for TLS,
// ca-file: a PEM bundle to trust instead of the system store.
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
	errNoBlobSink = errors.New("this setting has no blob store: pick file, syslog or webhook")
	// errReservedPrefix reports a blob sink prefix that state blobs own.
	errReservedPrefix = errors.New("the prefixes tofu and pulumi hold state: pick another prefix")
	// errSecretFilePermissions reports a secret file that group or others can read.
	errSecretFilePermissions = errors.New("secret file allows group or other access")
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
	if raw, ok := lookup(env); ok && strings.TrimSpace(raw) != "" {
		return []byte(strings.TrimSpace(raw)), nil
	}

	path, ok := lookup(fileEnv)
	if !ok || strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%w: set %s or %s", errNoWebhookSecret, env, fileEnv)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", fileEnv, err)
	}

	if perm := info.Mode().Perm(); perm&keyFilePrivateBits != 0 {
		return nil, fmt.Errorf("%s: %w %q (mode %04o): run chmod 600 on the file",
			fileEnv, errSecretFilePermissions, path, perm)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the secret file through the environment.
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", fileEnv, err)
	}

	secret := strings.TrimSpace(string(raw))
	if secret == "" {
		return nil, fmt.Errorf("%w: %s is empty", errNoWebhookSecret, fileEnv)
	}

	return []byte(secret), nil
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
