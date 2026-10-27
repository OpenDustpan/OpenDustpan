package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// reloadCanary is a sensitive value that no log line and no audit entry of
// a reload may hold.
const reloadCanary = "sqlite:/tmp/canary-5b1e.db"

// errDiskFull is the error of a failing fake audit store.
var errDiskFull = errors.New("disk full")

// fakeServerStore records the entries of AppendServer, or fails each append
// while fail is set.
type fakeServerStore struct {
	mu      sync.Mutex
	entries []audit.Entry
	fail    bool
}

func (s *fakeServerStore) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fail {
		return audit.Entry{}, errDiskFull
	}

	if err := e.Validate(); err != nil {
		return audit.Entry{}, err
	}

	s.entries = append(s.entries, e)

	return e, nil
}

// appended answers a copy of the appended entries.
func (s *fakeServerStore) appended() []audit.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]audit.Entry(nil), s.entries...)
}

// setFail makes each later append fail or succeed.
func (s *fakeServerStore) setFail(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fail = fail
}

// observations counts the results that a reloader reports: true for a
// success, false for a failure.
type observations struct {
	mu      sync.Mutex
	results []bool
}

func (o *observations) observe(result string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.results = append(o.results, result == metrics.ReloadSuccess)
}

func (o *observations) list() []bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]bool(nil), o.results...)
}

// reloadFixture is a reloader on a configuration file in a new directory,
// with a fake audit store, a log buffer and the observed results.
type reloadFixture struct {
	path    string
	r       *reloader
	store   *fakeServerStore
	results *observations
	logs    *lockedBuffer
}

// newReloadFixture resolves the serve settings as serve does at start, with
// the configuration file text, the arguments args and the variables env,
// and answers a reloader on them.
func newReloadFixture(t *testing.T, text string, env map[string]string, args ...string) *reloadFixture {
	t.Helper()

	path := writeConfig(t, text)

	var f serveFlags

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	f.register(fs)

	args = append([]string{"--config", path}, args...)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Serve has no default database. A test that sets none gets one from the
	// variables.
	if !strings.Contains(text, `"database"`) && !slices.Contains(args, "--database") {
		env = maps.Clone(env)
		if env == nil {
			env = map[string]string{}
		}

		env["DUSTPAN_DATABASE"] = "sqlite:/tmp/reload-test.db"
	}

	// As serve does: the start resolves against the variables, each
	// reload against the snapshot.
	vars := func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}

	f.args = args
	f.lookup = cli.SnapshotEnv(vars, cli.DeclaredEnvs())
	f.instanceID = "ins-aaaaaaaaaaaaaaaaaaaa"

	if err := f.resolveLayers(fs, vars); err != nil {
		t.Fatalf("resolve: %v", err)
	}

	fx := &reloadFixture{
		path:    path,
		store:   &fakeServerStore{mu: sync.Mutex{}, entries: nil, fail: false},
		results: &observations{mu: sync.Mutex{}, results: nil},
		logs:    &lockedBuffer{mu: sync.Mutex{}, buf: bytes.Buffer{}},
		r:       nil,
	}

	fx.r = &reloader{
		flags:      &f,
		entries:    fx.store,
		observe:    fx.results.observe,
		logger:     slog.New(slog.NewTextHandler(fx.logs, nil)),
		now:        func() time.Time { return time.Date(2026, time.October, 14, 12, 0, 0, 0, time.UTC) },
		current:    f.resolved,
		lastSum:    f.fileSum,
		readFailed: false,
	}

	return fx
}

// write replaces the content of the configuration file.
func (fx *reloadFixture) write(t *testing.T, text string) {
	t.Helper()

	if err := os.WriteFile(fx.path, []byte(text), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// installation answers the effective installation in the current configuration.
func (fx *reloadFixture) installation() string {
	for _, r := range fx.r.current {
		if r.Setting.Name() == "installation" {
			return r.Value
		}
	}

	return ""
}

func TestReloadAppliesAChangeWithAnAuditEntry(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west", "log-level": "info"}`, nil)

	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 0 {
		t.Fatalf("a poll of the same content reported %v, want nothing", got)
	}

	fx.write(t, `{"installation": "us-east", "log-level": "info"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 1 || !got[0] {
		t.Fatalf("results = %v, want one success", got)
	}

	if got := fx.installation(); got != "us-east" {
		t.Fatalf("installation = %q, want us-east", got)
	}

	checkInstallationEntry(t, fx.store.appended())

	logs := fx.logs.String()
	for _, want := range []string{
		`msg="setting changed" key=installation old=eu-west value=us-east layer=file takes_effect="next start"`,
		`msg="config file reloaded" changed=1 takes_effect="next start"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not hold %q:\n%s", want, logs)
		}
	}
}

// checkInstallationEntry proves entries hold one config-file entry of the
// instance with one change: the installation from eu-west to us-east.
func checkInstallationEntry(t *testing.T, entries []audit.Entry) {
	t.Helper()

	if len(entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(entries))
	}

	e := entries[0]
	if e.Channel != audit.ChannelConfigFile || e.Subject != "ins-aaaaaaaaaaaaaaaaaaaa" ||
		e.Action != string(identity.ActionServerManageSettings) || e.Namespace != audit.ServerScope {
		t.Fatalf("entry = %+v", e)
	}

	want := "installation:eu-west>us-east@file next start"
	if len(e.Changes) != 1 || e.Changes[0].Old == nil {
		t.Fatalf("settings = %+v, want %s", e.Changes, want)
	}

	c := e.Changes[0]
	if got := c.Key + ":" + *c.Old + ">" + c.New + "@" + c.Layer + " " + c.TakesEffect; got != want {
		t.Fatalf("change = %s, want %s", got, want)
	}
}

func TestReloadSignalWithoutChangeWritesNoEntry(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, map[string]string{"DUSTPAN_INSTALLATION": "from-env"})

	fx.r.reload(t.Context(), triggerSignal)

	if got := fx.results.list(); len(got) != 1 || !got[0] {
		t.Fatalf("results = %v, want one success", got)
	}

	if got := fx.store.appended(); len(got) != 0 {
		t.Fatalf("a reload without a change wrote %d entries", len(got))
	}

	// The override of decision 8 is logged again after each applied reload.
	if want := `level=WARN msg="setting has a value in more than one layer" key=installation`; !strings.Contains(
		fx.logs.String(), want,
	) {
		t.Errorf("logs do not hold %q:\n%s", want, fx.logs.String())
	}
}

func TestReloadKeepsTheOldConfigurationForABadFile(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west", "database": "sqlite:/tmp/x.db"}`, nil)

	// Each bad file keeps a valid database, so it fails for its own defect.
	const keep = `"database": "sqlite:/tmp/x.db", `

	for _, bad := range []string{
		`{"installation": "us-east", ` + keep + `"disable-pulumi-journaling": no}`,
		`{"installation": "us-east", ` + keep + `"log-level": "verbose"}`,
		// The URL is built in parts, so no commit holds a contiguous URL with
		// a password: make trufflehog scans every branch of the history.
		`{"installation": "us-east", ` + keep + `"public-url": "https://user:` + `pw@dustpan.example"}`,
		`{"installation": "us-east", ` + keep + `"tls-cert": "/a.crt"}`,
		`{"installation": "us-east", "database": "mysql://x"}`,
		`{"installation": "us-east", ` + keep + `"trusted-proxies": "0.0.0.0/0"}`,
		`{"installation": "us-east", ` + keep + `"trusted-proxy-header": "x-real-ip"}`,
	} {
		fx.write(t, bad)
		fx.r.reload(t.Context(), triggerPoll)
	}

	if got := fx.results.list(); len(got) != 7 || slices.Contains(got, true) {
		t.Fatalf("results = %v, want seven failures", got)
	}

	if got := fx.installation(); got != "eu-west" {
		t.Fatalf("installation = %q after bad files, want eu-west", got)
	}

	if got := fx.store.appended(); len(got) != 0 {
		t.Fatalf("bad files wrote %d audit entries", len(got))
	}

	if strings.Contains(fx.logs.String(), "pw@") {
		t.Errorf("logs hold the refused value:\n%s", fx.logs.String())
	}

	// The same bad content is refused once, not at each poll.
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 7 {
		t.Fatalf("a poll of the same bad content reported %v", got)
	}

	fx.write(t, `{"installation": "us-east", `+keep+`"log-level": "info"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.installation(); got != "us-east" {
		t.Fatalf("installation = %q after a good file, want us-east", got)
	}
}

func TestReloadFailsClosedWhenTheAuditEntryFails(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, nil)
	fx.store.setFail(true)

	fx.write(t, `{"installation": "us-east"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 1 || got[0] {
		t.Fatalf("results = %v, want one failure", got)
	}

	if got := fx.installation(); got != "eu-west" {
		t.Fatalf("installation = %q after a failed audit entry, want eu-west", got)
	}

	// The next poll tries again with the same content.
	fx.store.setFail(false)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.installation(); got != "us-east" {
		t.Fatalf("installation = %q after the retry, want us-east", got)
	}

	if got := fx.store.appended(); len(got) != 1 {
		t.Fatalf("got %d audit entries after the retry, want 1", len(got))
	}
}

func TestReloadOfASensitiveSettingShowsOnlySet(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"database": "sqlite:/tmp/a.db"}`, nil)

	fx.write(t, `{"database": "`+reloadCanary+`"}`)
	fx.r.reload(t.Context(), triggerPoll)

	entries := fx.store.appended()
	if len(entries) != 1 || len(entries[0].Changes) != 1 {
		t.Fatalf("entries = %+v, want one entry with the database", entries)
	}

	change := entries[0].Changes[0]
	if change.Key != "database" || change.New != "set" || change.Old == nil || *change.Old != "set" {
		t.Fatalf("change = %+v, want database set to set", change)
	}

	for name, text := range map[string]string{
		"logs": fx.logs.String(), "entry": audit.EncodeChanges(entries[0].Changes),
	} {
		for _, value := range []string{reloadCanary, "sqlite:/tmp/a.db"} {
			if form := leak(text, value); form != "" {
				t.Errorf("%s hold %s of a sensitive value:\n%s", name, form, text)
			}
		}
	}
}

func TestReloadOfAMissingFileFailsOnce(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, nil)

	if err := os.Remove(fx.path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	fx.r.reload(t.Context(), triggerPoll)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 1 || got[0] {
		t.Fatalf("results = %v, want one failure", got)
	}

	fx.r.reload(t.Context(), triggerSignal)

	if got := fx.results.list(); len(got) != 2 {
		t.Fatalf("a signal on a missing file reported %v, want a second failure", got)
	}

	// The file comes back with the same content: the gauge must go back
	// to 1, so the poll applies it.
	fx.write(t, `{"installation": "eu-west"}`)
	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.results.list(); len(got) != 3 || !got[2] {
		t.Fatalf("results = %v, want a success after the file came back", got)
	}
}

func TestReloaderRunsOnSignalAndTickAndStops(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, nil)

	hup := make(chan os.Signal, 1)
	tick := make(chan time.Time, 1)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		fx.r.run(ctx, hup, tick)
	}()

	fx.write(t, `{"installation": "us-east"}`)
	tick <- time.Now()

	waitFor(t, func() bool { return len(fx.results.list()) == 1 })

	hup <- syscall.SIGHUP

	waitFor(t, func() bool { return len(fx.results.list()) == 2 })

	cancel()
	<-done

	if got := len(fx.store.appended()); got != 1 {
		t.Fatalf("got %d audit entries, want 1", got)
	}
}

// waitFor polls cond until it is true or one second passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()

	for !cond() {
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("condition not met in one second")
		}
	}
}

// TestResolveReloadKeepsTheStartFlags proves a reload keeps the command line
// and the environment of the start: a flag still wins over the file.
func TestResolveReloadKeepsTheStartFlags(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"listen": ":9000"}`, map[string]string{"DUSTPAN_LOG_LEVEL": "debug"},
		"--listen", ":7000")

	resolved, err := fx.r.flags.resolveReload([]byte(`{"listen": ":9001", "log-level": "warn"}`))
	if err != nil {
		t.Fatalf("resolveReload: %v", err)
	}

	got := map[string]string{}
	for _, r := range resolved {
		got[r.Setting.Name()] = r.Value + "@" + string(r.Source)
	}

	if got["listen"] != ":7000@flag" || got["log-level"] != "debug@env" {
		t.Fatalf("listen = %s, log-level = %s; want the flag and the variable", got["listen"], got["log-level"])
	}

	if changes := cli.Changes(fx.r.current, resolved); len(changes) != 0 {
		t.Fatalf("changes = %+v, want none", changes)
	}
}

// TestReloadOfAnUnchangedSecretVariableIsNoChange proves a sensitive
// variable that did not change gives no change at a reload: the snapshot
// keeps only its fingerprint, and the fingerprint matches the start.
func TestReloadOfAnUnchangedSecretVariableIsNoChange(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"DUSTPAN_DATABASE":    "sqlite:/var/lib/dustpan.db",
		"DUSTPAN_ADMIN_TOKEN": "admin-" + reloadCanary,
	}

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, env)

	fx.write(t, `{"installation": "us-east"}`)
	fx.r.reload(t.Context(), triggerPoll)

	checkInstallationEntry(t, fx.store.appended())
}

// TestReloadSeesASymlinkSwap proves the poll sees the update of a Kubernetes
// ConfigMap volume: the kubelet points the ..data link at a new directory,
// and the file path reads through the links.
func TestReloadSeesASymlinkSwap(t *testing.T) {
	t.Parallel()

	fx := newReloadFixture(t, `{"installation": "eu-west"}`, nil)

	volume := t.TempDir()
	first := filepath.Join(volume, "..2026_10_14_12_00_00.1")
	second := filepath.Join(volume, "..2026_10_14_12_05_00.2")

	for dir, text := range map[string]string{first: `{"installation": "eu-west"}`, second: `{"installation": "us-east"}`} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(text), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	data := filepath.Join(volume, "..data")
	if err := os.Symlink(filepath.Base(first), data); err != nil {
		t.Fatalf("link ..data: %v", err)
	}

	path := filepath.Join(volume, "config.yaml")
	if err := os.Symlink(filepath.Join("..data", "config.yaml"), path); err != nil {
		t.Fatalf("link config.yaml: %v", err)
	}

	fx.r.flags.config = path

	fx.r.reload(t.Context(), triggerPoll)

	if got := fx.store.appended(); len(got) != 0 {
		t.Fatalf("the same content wrote %d entries", len(got))
	}

	// The atomic swap of the kubelet: a new link, renamed over ..data.
	tmp := filepath.Join(volume, "..data_tmp")
	if err := os.Symlink(filepath.Base(second), tmp); err != nil {
		t.Fatalf("link ..data_tmp: %v", err)
	}

	if err := os.Rename(tmp, data); err != nil {
		t.Fatalf("swap ..data: %v", err)
	}

	fx.r.reload(t.Context(), triggerPoll)

	checkInstallationEntry(t, fx.store.appended())
}
