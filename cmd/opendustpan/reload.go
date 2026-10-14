package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
)

// reloadPoll is the interval of the content poll of the configuration file
// (ADR 63 decision 5). The poll sees the symbolic link swap of a Kubernetes
// volume, because it reads through the link.
const reloadPoll = 10 * time.Second

// reloadTrigger names what started a reload.
type reloadTrigger int

const (
	// triggerPoll is the content poll: it reloads only new content.
	triggerPoll reloadTrigger = iota
	// triggerSignal is SIGHUP: it always reloads.
	triggerSignal
)

// reloadMetrics records the reload results in the metrics of the server.
// serve sets observe once the server exists; a nil observe records nothing.
type reloadMetrics struct {
	observe func(result string)
}

// record passes result to observe.
func (m *reloadMetrics) record(result string) {
	if m.observe != nil {
		m.observe(result)
	}
}

// startedReloader holds the stop function of a reloader that the start
// started, or nil when none started.
type startedReloader struct {
	stop func()
}

// Stop stops the reloader and waits for it, when one started.
func (s *startedReloader) Stop() {
	if s.stop != nil {
		s.stop()
	}
}

// errReloadAudit wraps a failed audit entry of a reload: the server keeps the
// old configuration (ADR 63 decision 11), and the next poll tries again.
var errReloadAudit = errors.New("write the config-file audit entry")

// reloader reloads the configuration file on SIGHUP and when the poll sees
// new content (ADR 63 decision 5). A reload reads the whole file, checks it
// with the rules of the start, and keeps the old configuration when the file
// or the audit entry fails. No setting is live yet (RFC 68 keeps that open):
// a changed setting takes effect at the next start, and the reload records
// the change in the log and the audit log.
type reloader struct {
	flags   *serveFlags
	entries audit.ServerStore
	observe func(result string)
	logger  *slog.Logger
	now     func() time.Time
	current []cli.Resolved
	lastSum [sha256.Size]byte
	// readFailed is true while the file cannot be read, so a missing file
	// counts one failure, not one failure for each poll.
	readFailed bool
}

// catchHangup catches SIGHUP in f.hup when --config names a file, and
// answers the function that stops it. serve calls it right after the parse
// of the flags: a signal during the start waits in the buffered channel for
// the reloader and never stops the process, which is the default action of
// SIGHUP. Without --config, SIGHUP keeps its default action.
func (f *serveFlags) catchHangup() func() {
	if strings.TrimSpace(f.config) == "" {
		return func() {}
	}

	f.hup = make(chan os.Signal, 1)
	signal.Notify(f.hup, syscall.SIGHUP)

	hup := f.hup

	return func() { signal.Stop(hup) }
}

// startReloader starts the reloader of the configuration file when --config
// names one, and answers the function that stops it and waits for it.
// Without --config, nothing reloads.
func (f *serveFlags) startReloader(
	ctx context.Context, entries audit.ServerStore, observe func(result string), logger *slog.Logger,
) func() {
	if strings.TrimSpace(f.config) == "" {
		return func() {}
	}

	r := &reloader{
		flags:      f,
		entries:    entries,
		observe:    observe,
		logger:     logger,
		now:        time.Now,
		current:    f.resolved,
		lastSum:    f.fileSum,
		readFailed: false,
	}

	// serve catches SIGHUP from the parse of the flags on (f.hup). A caller
	// without that channel gets one here.
	hup := f.hup
	stopSignal := func() {}

	if hup == nil {
		hup = make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)

		stopSignal = func() { signal.Stop(hup) }
	}

	ticker := time.NewTicker(reloadPoll)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)

		r.run(runCtx, hup, ticker.C)
	}()

	return func() {
		cancel()
		<-done
		ticker.Stop()
		stopSignal()
	}
}

// run reloads on each signal and checks the content on each tick until ctx
// is done.
func (r *reloader) run(ctx context.Context, hup <-chan os.Signal, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-hup:
			r.reload(ctx, triggerSignal)
		case <-tick:
			r.reload(ctx, triggerPoll)
		}
	}
}

// reload reads the file and applies it. A poll does nothing when the
// SHA-256 of the content equals the last one. A signal always reloads.
func (r *reloader) reload(ctx context.Context, trigger reloadTrigger) {
	force := trigger == triggerSignal

	data, err := cli.ReadConfigBytes(strings.TrimSpace(r.flags.config))
	if err != nil {
		if !r.readFailed || force {
			r.fail(ctx, err)
		}

		r.readFailed = true

		return
	}

	sum := sha256.Sum256(data)
	if !force && !r.readFailed && sum == r.lastSum {
		return
	}

	r.readFailed = false

	if err := r.apply(ctx, data); err != nil {
		// A file that fails the checks stays refused until its content
		// changes. A failed audit entry tries again at the next poll.
		if !errors.Is(err, errReloadAudit) {
			r.lastSum = sum
		}

		r.fail(ctx, err)

		return
	}

	r.lastSum = sum
	r.observe(metrics.ReloadSuccess)
}

// apply resolves data with the command line and the environment of the
// start, writes the audit entry of the changed settings, and only then
// takes the new configuration and writes the log lines (ADR 63 decisions 8,
// 9 and 11).
func (r *reloader) apply(ctx context.Context, data []byte) error {
	resolved, err := r.flags.resolveReload(data)
	if err != nil {
		return err
	}

	changes := cli.Changes(r.current, resolved)

	if len(changes) > 0 {
		entry := cli.SettingsEntry(audit.ChannelConfigFile, r.flags.instanceID, r.now(), changes)

		if _, err := r.entries.AppendServer(ctx, entry); err != nil {
			return fmt.Errorf("%w: %w", errReloadAudit, err)
		}
	}

	r.current = resolved

	cli.LogChanges(ctx, r.logger, changes)
	cli.LogOverrides(ctx, r.logger, resolved)

	r.logger.LogAttrs(ctx, slog.LevelInfo, "config file reloaded",
		slog.Int("changed", len(changes)),
		slog.String("takes_effect", cli.TakesEffectNextStart),
	)

	return nil
}

// fail records a failed reload: one ERROR line that names the key and the
// rule, never the value, and the failure metrics. The old configuration
// stays.
func (r *reloader) fail(ctx context.Context, err error) {
	r.logger.LogAttrs(ctx, slog.LevelError, "config file reload failed; the old configuration stays",
		slog.String("error", err.Error()),
	)
	r.observe(metrics.ReloadFailure)
}

// databaseSource answers the layer of the database setting in resolved.
func databaseSource(resolved []cli.Resolved) cli.Source {
	for _, r := range resolved {
		if r.Setting.Name() == "database" {
			return r.Source
		}
	}

	return cli.SourceDefault
}

// resolveReload decodes data and resolves every serve setting against the
// command line and the environment snapshot of the start, with the checks
// of the start. It never changes f: it resolves into a new flag set.
func (f *serveFlags) resolveReload(data []byte) ([]cli.Resolved, error) {
	file, err := cli.ParseConfig(data)
	if err != nil {
		return nil, err
	}

	var scratch serveFlags

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scratch.register(fs)

	if parseErr := fs.Parse(f.args); parseErr != nil {
		return nil, fmt.Errorf("parse flags: %w", parseErr)
	}

	resolved, resolveErr := cli.Resolve(fs, cli.ServeSettings(), f.lookup, file)
	if resolveErr != nil {
		return nil, fmt.Errorf("resolve settings: %w", resolveErr)
	}

	if checkErr := scratch.check(); checkErr != nil {
		return nil, checkErr
	}

	// A database URL from a variable reads as a fingerprint token in the
	// snapshot. It did not change since the start, which checked it, so
	// only a value from the flag or the file gets the check again.
	if databaseSource(resolved) != cli.SourceEnv {
		if dbErr := checkDatabaseFlag(scratch.database); dbErr != nil {
			return nil, dbErr
		}
	}

	return resolved, nil
}
