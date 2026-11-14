package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/internal/devsignin"
	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// devNamespace is the namespace that dev makes, so the first stack or
// workspace of a trial needs no other command. The name repeats the warning
// in each stack and workspace name.
const devNamespace = "dev"

// Errors of the dev command.
var (
	errDevExclusive = errors.New("--ephemeral and --data-dir exclude each other: set one of them")
	errDevArgs      = errors.New("dev takes no argument, only flags")
	errDevListen    = errors.New(
		"the listen address of dev needs a host and a port: set --listen, for example 127.0.0.1:8080",
	)
	errDevPlacementStore = errors.New("the store keeps no collections or placement rules")
)

// devFlags holds the settings of the dev command. The values come from the
// flags only.
type devFlags struct {
	dataDir        string
	listen         string
	adminTokenFile string
	consoleDir     string
	logLevel       string
	logFormat      string
	hostnames      []string
	maxCheckpoint  int64
	maxSecretBatch int64
	maxState       int64
	maxBodyFlight  int64
	blobCache      int64
	demo           bool
	ephemeral      bool
	tls            bool
	noJournaling   bool
}

// register registers the flags of dev. The flags read no variable.
func (f *devFlags) register(fs *flag.FlagSet) {
	cli.DevByFlag("data-dir").String(fs, &f.dataDir)
	cli.DevByFlag("demo").Bool(fs, &f.demo)
	cli.DevByFlag("ephemeral").Bool(fs, &f.ephemeral)
	cli.DevByFlag("listen").String(fs, &f.listen)
	cli.DevByFlag("tls").Bool(fs, &f.tls)
	cli.DevByFlag("hostname").List(fs, &f.hostnames)
	cli.DevByFlag("admin-token-file").String(fs, &f.adminTokenFile)
	cli.DevByFlag("console-dir").String(fs, &f.consoleDir)
	cli.DevByFlag("log-level").String(fs, &f.logLevel)
	cli.DevByFlag("log-format").String(fs, &f.logFormat)
	cli.DevByFlag("disable-pulumi-journaling").Bool(fs, &f.noJournaling)
	cli.DevByFlag("max-checkpoint-bytes").Int64(fs, &f.maxCheckpoint)
	cli.DevByFlag("max-secret-batch-bytes").Int64(fs, &f.maxSecretBatch)
	cli.DevByFlag("max-state-bytes").Int64(fs, &f.maxState)
	cli.DevByFlag("max-body-bytes-in-flight").Int64(fs, &f.maxBodyFlight)
	cli.DevByFlag("blob-cache-bytes").Int64(fs, &f.blobCache)
}

// check refuses settings that are wrong together or out of range.
func (f *devFlags) check() error {
	if f.ephemeral && f.dataDir != "" {
		return errDevExclusive
	}

	if err := checkLogFlags(f.logLevel, f.logFormat); err != nil {
		return err
	}

	limits := []int64{f.maxCheckpoint, f.maxSecretBatch, f.maxState, f.maxBodyFlight, f.blobCache}
	if err := checkSizeFlags(limits); err != nil {
		return err
	}

	if _, _, err := net.SplitHostPort(f.listen); err != nil {
		return fmt.Errorf("%w: %q", errDevListen, f.listen)
	}

	return nil
}

// devProcess holds what dev takes from its process. A test replaces each
// field, so a test never changes the environment of the process.
type devProcess struct {
	lookup   func(string) (string, bool)
	unsetenv func(string) error
	hostname func() (string, error)
	now      func() time.Time
	dirs     func(flagDir, envParent string) devDirInput
	// started, when it is not nil, runs when the server is about to serve,
	// with the URL of the server.
	started func(url string)
	// roots reads the roots that the certificate bundle starts with.
	roots func(lookup func(string) (string, bool)) []byte
	// processStart answers the start time of the process pid, errNoProcess
	// for an ID that no process has, or errStartUnknown.
	processStart func(pid int) (time.Time, error)
	goos         string
	terminal     bool
}

// osDevProcess answers the process of the binary.
func osDevProcess(stderr io.Writer) devProcess {
	terminal := false
	if file, ok := stderr.(*os.File); ok {
		terminal = isCharDevice(file)
	}

	return devProcess{
		lookup: os.LookupEnv, unsetenv: os.Unsetenv, hostname: os.Hostname, now: time.Now, dirs: osDevDirInput,
		started: nil, processStart: processStartTime, goos: runtime.GOOS, terminal: terminal,
		roots: func(lookup func(string) (string, bool)) []byte {
			return devRoots(devRootsInput{lookup: lookup, files: rootFileCandidates, dirs: rootCertDirs})
		},
	}
}

// dev runs the dev command: a server for a trial or for CI that takes every
// decision for the user and says so.
func dev(ctx context.Context, args []string, stderr io.Writer) error {
	return devWithProcess(ctx, args, stderr, osDevProcess(stderr))
}

// devWithProcess runs dev with an explicit process, so a test drives it in
// process.
func devWithProcess(ctx context.Context, args []string, stderr io.Writer, proc devProcess) error {
	if len(args) > 0 && args[0] == devResetCommand {
		return devReset(ctx, args[1:], stderr, proc)
	}

	var f devFlags

	fs := flag.NewFlagSet("dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	f.register(fs)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if fs.NArg() > 0 {
		return errDevArgs
	}

	if err := f.check(); err != nil {
		return err
	}

	run := &devRun{
		logger: nil, logGate: nil, stderr: stderr, proc: proc, startedAt: proc.now(), flags: f, dir: "",
		fresh: false, token: devToken{value: "", file: "", source: tokenMade},
	}

	return schemaRefusal(kindRefusal(run.run(ctx), commandDev), commandDev, f.dataDir)
}

// gateWriter holds back what is written to it until open is called, then
// passes every write through. dev logs into it until the banner is printed,
// so the banner is the first text on stderr, also with --log-format json.
type gateWriter struct {
	w    io.Writer
	buf  bytes.Buffer
	mu   sync.Mutex
	pass bool
}

// Write implements io.Writer.
func (g *gateWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.pass {
		return g.buf.Write(p) //nolint:wrapcheck // A transparent wrapper: callers see the original error.
	}

	return g.w.Write(p) //nolint:wrapcheck // A transparent wrapper: callers see the original error.
}

// open writes what was held back and lets later writes through.
func (g *gateWriter) open() error {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.pass = true

	if _, err := g.w.Write(g.buf.Bytes()); err != nil {
		return fmt.Errorf("write the log: %w", err)
	}

	g.buf.Reset()

	return nil
}

// devRun is one run of dev.
type devRun struct {
	// logger is the application logger, set by run. Its lines wait in logGate
	// until the banner is printed.
	logger    *slog.Logger
	logGate   *gateWriter
	stderr    io.Writer
	proc      devProcess
	startedAt time.Time
	flags     devFlags
	// dir is the data directory. fresh says that this run made the key file,
	// so it is the first start of the directory.
	dir   string
	fresh bool
	// token is the administrator token of dev.
	token devToken
}

// run claims the data directory, makes what the directory lacks, prints the
// banner and serves until ctx is done.
func (r *devRun) run(ctx context.Context) error {
	envToken, hasEnvToken := r.proc.lookup(cli.DevAdminTokenEnv)
	envParent, _ := r.proc.lookup(cli.DevDataDirEnv)

	// The variables of the secret settings of serve never reach dev. The
	// token of dev is already read: dev clears its variable too.
	for _, name := range cli.DevSecretEnvs() {
		if err := r.proc.unsetenv(name); err != nil {
			return fmt.Errorf("%s: clear variable: %w", name, err)
		}
	}

	level := logLevelOf(r.flags.logLevel)

	r.logGate = &gateWriter{w: r.stderr, buf: bytes.Buffer{}, mu: sync.Mutex{}, pass: false}

	app, err := newAppLog( //nolint:contextcheck // The log writers outlive this call; close bounds them.
		nil, level, r.flags.logFormat, r.logGate, sinkEnv{lookup: r.proc.lookup, ranges: nil},
	)
	if err != nil {
		return err
	}

	r.logger = app.logger

	defer closeAppLog(ctx, &app, r.stderr)
	defer r.openLog() // A run that fails before the banner still shows its log.

	if delErr := r.deleteStaleAddr(envParent); delErr != nil {
		return delErr
	}

	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", r.flags.listen) //nolint:exhaustruct_v5 // Defaults only.
	if err != nil {
		return fmt.Errorf("listen on %s: %w", r.flags.listen, err)
	}

	handedOver := false

	defer func() {
		if !handedOver {
			_ = listener.Close() //nolint:errcheck // The first error is the one that matters.
		}
	}()

	cleanup, err := r.prepareDir(envParent)

	defer cleanup()

	if err != nil {
		return err
	}

	if tokenErr := r.loadToken(envToken, hasEnvToken); tokenErr != nil {
		return tokenErr
	}

	handedOver = true

	return r.serve(ctx, listener)
}

// deleteStaleAddr deletes dev.addr of the data directory at start, before dev
// listens: a file that an earlier run left (a crash or a kill) must not tell
// `health` that a server runs. It first runs the checks of prepareDir that
// read only: the directory must be a data directory of dev (it holds the key
// file), private, and owned by the current user. A foreign directory, and a
// directory with an error, keep their files. The error shows later, when
// prepareDir reads the same path. It then deletes the file only when the
// process ID and the start time in it name no running process, so a second
// `dev` on the directory of a live server leaves the live file in place. A
// new --ephemeral directory has no such file, and a directory that does not
// exist yet has none.
func (r *devRun) deleteStaleAddr(envParent string) error {
	if r.flags.ephemeral {
		return nil
	}

	dir, err := devDirPath(r.proc.dirs(r.flags.dataDir, envParent))
	if err != nil {
		return nil //nolint:nilerr // prepareDir reports the same error.
	}

	if claimed, inspectErr := inspectDevDir(dir); inspectErr != nil || !claimed {
		return nil //nolint:nilerr // prepareDir reports the same error, and a foreign directory keeps its file.
	}

	// Defense in depth: the file is deleted only when it names no running
	// process. A refusal above does not decide it, and the later listen
	// does not either, so the three checks each stand alone.
	if addr, ok := readDevAddr(dir); ok && devAddrLive(addr, r.proc.processStart) {
		return nil
	}

	return removeDevAddr(dir)
}

// openLog lets the log through when the run ends before the banner, so the
// lines that wait are not lost.
func (r *devRun) openLog() {
	if err := r.logGate.open(); err != nil {
		_, _ = fmt.Fprintln(r.stderr, "opendustpan:", err) //nolint:errcheck // Nothing is left to report to.
	}
}

// prepareDir chooses and claims the data directory, and makes the key file of
// a directory that has none. It answers the function that runs when dev
// stops: it deletes the directory of --ephemeral.
func (r *devRun) prepareDir(envParent string) (func(), error) {
	noCleanup := func() {}

	if r.flags.ephemeral {
		dir, err := os.MkdirTemp("", "opendustpan-dev-")
		if err != nil {
			return noCleanup, fmt.Errorf("make the temporary data directory: %w", err)
		}

		r.dir, r.fresh = dir, true

		if envParent != "" {
			r.logger.Info("--ephemeral ignores the variable of the data directory",
				slog.String("variable", cli.DevDataDirEnv))
		}

		r.logger.Warn("dev keeps its data in a temporary directory and deletes it at stop, "+
			"and with it the state that tracks the cloud resources of the trial", slog.String("path", dir))

		return func() { r.removeEphemeral() }, r.makeKey()
	}

	dir, err := devDirPath(r.proc.dirs(r.flags.dataDir, envParent))
	if err != nil {
		return noCleanup, err
	}

	fresh, err := claimDevDir(dir)
	if err != nil {
		return noCleanup, err
	}

	r.dir, r.fresh = dir, fresh

	if fresh {
		return noCleanup, r.makeKey()
	}

	return noCleanup, nil
}

// makeKey makes the key file of a data directory that has none. The key file
// comes first: the database is opened only in a directory with a key file.
func (r *devRun) makeKey() error {
	_, err := makeDevKey(filepath.Join(r.dir, devKeyName))

	return err
}

// removeEphemeral deletes the data directory of --ephemeral and logs the
// warning again.
func (r *devRun) removeEphemeral() {
	r.logger.Warn("dev deletes its temporary data directory, and with it the state that tracks "+
		"the cloud resources of the trial", slog.String("path", r.dir))

	if err := os.RemoveAll(r.dir); err != nil {
		r.logger.Error("delete the temporary data directory", slog.String("path", r.dir),
			slog.String("error", err.Error()))
	}
}

// loadToken chooses the administrator token.
func (r *devRun) loadToken(envToken string, hasEnvToken bool) error {
	token, err := loadDevToken(devTokenInput{
		dir: r.dir, flagFile: r.flags.adminTokenFile, envValue: envToken, hasEnvVal: hasEnvToken,
	})
	if err != nil {
		return err
	}

	r.token = token

	return nil
}

// serve opens the store and the blob store, writes the files of the
// credentials, prints the banner and runs the server.
func (r *devRun) serve(ctx context.Context, listener net.Listener) error {
	key, err := readDevKey(filepath.Join(r.dir, devKeyName))
	if err != nil {
		return err
	}

	wrapper, err := builtinWrapper(key)
	if err != nil {
		return err
	}

	authn, err := identity.NewStaticTokens(r.token.value)
	if err != nil {
		return fmt.Errorf("administrator token of dev: %w", err)
	}

	cert, err := r.certificate()
	if err != nil {
		return err
	}

	st, err := openStore(ctx, "sqlite:"+filepath.Join(r.dir, devDBName), r.logger, openMigrate, installation.KindDev)
	if err != nil {
		return err
	}

	defer st.close()

	serverURL := r.serverURL(listener, cert)

	// The code and its route exist only when the banner can show the link:
	// the first start of the directory, a terminal and a console.
	var code *devcode.Code

	codeText := ""

	if r.showsLink(listener) {
		if code, codeText, err = devcode.Make(r.proc.now); err != nil {
			return err
		}
	}

	if filesErr := r.writeCredentials(serverURL, cert); filesErr != nil {
		return filesErr
	}

	if bannerErr := r.banner(listener, serverURL, cert, codeText); bannerErr != nil {
		return bannerErr
	}

	if logErr := r.logGate.open(); logErr != nil {
		return logErr
	}

	return r.runServer(ctx, runInputs{
		st: st, listener: listener, wrapper: wrapper, authn: authn, cert: cert, serverURL: serverURL, code: code,
	})
}

// runInputs holds what runServer needs.
type runInputs struct {
	st        store
	listener  net.Listener
	wrapper   secret.Wrapper
	authn     *identity.StaticTokens
	cert      devCert
	serverURL string
	code      *devcode.Code
}

// certificate makes the certificate of --tls, or answers the zero value.
func (r *devRun) certificate() (devCert, error) {
	if !r.flags.tls {
		return noCert, nil
	}

	host, _, err := net.SplitHostPort(r.flags.listen)
	if err != nil {
		return devCert{}, fmt.Errorf("%w: %q", errDevListen, r.flags.listen)
	}

	hostName, err := r.proc.hostname()
	if err != nil {
		hostName = ""
	}

	in := devCertInput{
		now: r.proc.now, dir: r.dir, names: devCertNames(host, hostName, r.flags.hostnames), roots: nil,
		writeBundle: r.proc.goos == "linux",
	}

	if in.writeBundle {
		in.roots = r.proc.roots(r.proc.lookup)
	}

	return ensureDevCert(in)
}

// noCert is the certificate of a run with no --tls: no file and no end.
//
//nolint:gochecknoglobals // A frozen zero value.
var noCert = devCert{certFile: "", keyFile: "", bundleFile: "", notAfter: time.Time{}}

// serverURL answers the URL of the server: the scheme, and an address that a
// client can dial.
func (r *devRun) serverURL(listener net.Listener, cert devCert) string {
	scheme := "http"
	if cert.certFile != "" {
		scheme = "https"
	}

	return scheme + "://" + dialAddress(listener.Addr())
}

// writeCredentials writes dev.env and dev.tfrc with mode 0600.
func (r *devRun) writeCredentials(serverURL string, cert devCert) error {
	envFile := filepath.Join(r.dir, devEnvName)
	tfrcFile := filepath.Join(r.dir, devTFRCName)

	tfrc, err := renderDevTFRC(serverURL, r.token.value)
	if err != nil {
		return err
	}

	env := renderDevEnv(devEnvInput{
		serverURL: serverURL, envFile: envFile, tfrcFile: tfrcFile, token: r.token, bundleFile: cert.bundleFile,
	})

	if err := writePrivateFile(envFile, []byte(env)); err != nil {
		return err
	}

	return writePrivateFile(tfrcFile, []byte(tfrc))
}

// banner prints the banner of dev to stderr.
func (r *devRun) banner(listener net.Listener, serverURL string, cert devCert, codeText string) error {
	b := devBanner{
		placement: r.placementLine(serverURL),
		tlsExpiry: cert.notAfter, dir: r.dir, serverURL: serverURL, signIn: r.signInLine(listener, serverURL, codeText),
		envFile: filepath.Join(r.dir, devEnvName), network: r.networkLine(listener), tokenNote: r.tokenNote(),
		ephemeral: r.flags.ephemeral, trustStep: r.flags.tls && cert.bundleFile == "",
	}

	return writeDevBanner(r.stderr, b)
}

// showsLink reports whether the banner shows the sign-in link: the first start
// of the data directory, stderr is a terminal, dev serves a console, and a
// browser can keep the session cookie. The cookie is Secure, so on plain HTTP
// a browser keeps it only from a loopback origin: dev makes no link for an
// address that is not a loopback address, unless --tls is set.
func (r *devRun) showsLink(listener net.Listener) bool {
	return r.fresh && r.proc.terminal && r.flags.consoleDir != "" && (r.flags.tls || isLoopbackAddr(listener.Addr()))
}

// signInLine answers the text of the Sign in line. The link shows only on the
// first start of the directory and only when stderr is a terminal, so a log of
// CI or of a container never holds the code. Without --tls, the link names
// localhost, because a browser keeps the Secure cookie on plain HTTP only for a
// loopback name, and the address of a LAN can be a name that it refuses. Else
// the line names where the token is.
func (r *devRun) signInLine(listener net.Listener, serverURL, codeText string) string {
	switch {
	case r.flags.consoleDir == "":
		return "dev serves no console: set --console-dir to serve one"
	case codeText != "":
		return r.linkBase(listener, serverURL) + "/sign-in#code=" + codeText +
			"   (first start on a terminal only, works one time, for 10 minutes)"
	case r.fresh && r.proc.terminal && !r.flags.tls && !isLoopbackAddr(listener.Addr()):
		return "no sign-in link on plain HTTP from another machine: set --tls to sign in with a link, " +
			"or use the administrator token in the console"
	default:
		return "with the administrator token, in the console"
	}
}

// linkBase answers the base of the sign-in link: the URL of the server with
// TLS, else http://localhost and the port of the server.
func (r *devRun) linkBase(listener net.Listener, serverURL string) string {
	if r.flags.tls {
		return serverURL
	}

	tcp, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return serverURL
	}

	return "http://" + net.JoinHostPort("localhost", strconv.Itoa(tcp.Port))
}

// tokenNote answers the text of the Token line: where the administrator
// token is. The banner never shows the token itself.
func (r *devRun) tokenNote() string {
	switch r.token.source {
	case tokenMade:
		return "in the file " + r.token.file
	case tokenEnv:
		return "in the variable " + cli.DevAdminTokenEnv + ", which dev.env reads in the shell that loads it"
	case tokenFile:
		return "in the file " + r.token.file + ". Set PULUMI_ACCESS_TOKEN and TF_HTTP_PASSWORD to it for the " +
			"Pulumi CLI and OpenTofu, because they read no token file"
	default:
		return ""
	}
}

// networkLine answers the text of the Network line for a listen address that
// is not a loopback address, and the empty text for a loopback address. A log
// line says the same.
func (r *devRun) networkLine(listener net.Listener) string {
	if isLoopbackAddr(listener.Addr()) {
		return ""
	}

	text := listener.Addr().String() + " is not a loopback address: other hosts can reach this server"
	if !r.flags.tls {
		text += ", and the administrator token crosses the network in plain text"
	}

	r.logger.Warn("dev listens on an address that is not a loopback address",
		slog.String("addr", listener.Addr().String()), slog.Bool("tls", r.flags.tls))

	return text
}

// isLoopbackAddr reports whether addr is a loopback address.
func isLoopbackAddr(addr net.Addr) bool {
	tcp, ok := addr.(*net.TCPAddr)

	return ok && tcp.IP != nil && tcp.IP.IsLoopback()
}

// runServer builds the server with the server code of serve and runs it.
func (r *devRun) runServer(ctx context.Context, in runInputs) error {
	instanceID := instance.NewID()

	sf, err := r.serveFlags(in.serverURL, in.cert, instanceID)
	if err != nil {
		return err
	}

	r.logger.LogAttrs(ctx, slog.LevelInfo, "instance", slog.String("instance_id", instanceID))

	blobs, err := sf.openLiveBlobs(ctx, r.logger)
	if err != nil {
		return err
	}

	defer closeLiveStore(ctx, r.logger, blobs)

	opts, err := sf.serverOptions(in.st, blobs, in.authn, in.wrapper, r.logger)
	if err != nil {
		return err
	}

	opts = append(opts, dustpan.WithListener(in.listener), dustpan.WithBeforeServe(r.beforeServe(in, instanceID)))

	if in.code != nil {
		signIn, signInErr := r.devSignInOption(ctx, in)
		if signInErr != nil {
			return signInErr
		}

		opts = append(opts, signIn)
	}

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		return fmt.Errorf("configure server: %w", err)
	}

	defer r.stopped()

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}

// devSignInOption answers the server option of the dev sign-in. The session
// of the link gets the role from the bootstrap administrator of the token
// file, so the grant names the same principal as the token.
func (r *devRun) devSignInOption(ctx context.Context, in runInputs) (dustpan.Option, error) {
	admin, err := in.authn.AuthenticateToken(ctx, r.token.value)
	if err != nil {
		return nil, fmt.Errorf("administrator of dev: %w", err)
	}

	raw, err := devsignin.Option(devsignin.Config{Code: in.code, Admin: admin})
	if err != nil {
		return nil, fmt.Errorf("dev sign-in: %w", err)
	}

	opt, ok := raw.(dustpan.Option)
	if !ok {
		return nil, fmt.Errorf("dev sign-in: %w", devsignin.ErrNotRegistered)
	}

	return opt, nil
}

// stopped deletes dev.addr at stop.
func (r *devRun) stopped() {
	if err := removeDevAddr(r.dir); err != nil {
		r.logger.Warn("delete dev.addr", slog.String("error", err.Error()))
	}
}

// beforeServe answers the function that runs after the server claimed the
// installation kind and listens, and before it serves: it makes the
// namespace dev, writes the start entry and dev.addr.
func (r *devRun) beforeServe(in runInputs, instanceID string) func(context.Context) error {
	return func(ctx context.Context) error {
		created, err := r.ensureNamespace(ctx, in.st)
		if err != nil {
			return err
		}

		// Dev makes the rule on the first start only, so a later start never
		// makes a rule that the developer deleted.
		if created {
			if seedErr := r.seedPlacement(ctx, in, devNamespace); seedErr != nil {
				return seedErr
			}
		}

		if err := r.seedDemo(ctx, in); err != nil {
			return err
		}

		entry := cli.SettingsEntry(audit.ChannelStartup, instanceID, r.proc.now(), r.startChanges())
		if _, err := in.st.AppendServer(ctx, entry); err != nil {
			return fmt.Errorf("write the start audit entry: %w", err)
		}

		addr := devAddr{
			Started: r.startedAt.UTC(), Addr: dialAddress(in.listener.Addr()), Cert: in.cert.certFile, PID: os.Getpid(),
		}
		if err := writeDevAddr(r.dir, addr); err != nil {
			return err
		}

		if r.proc.started != nil {
			r.proc.started(in.serverURL)
		}

		return nil
	}
}

// ensureNamespace makes the namespace dev when it does not exist. It answers
// true when it made the namespace.
func (r *devRun) ensureNamespace(ctx context.Context, st store) (bool, error) {
	return r.ensureNamed(ctx, st, devNamespace)
}

// ensureNamed makes the namespace name when it does not exist. It answers true
// when it made the namespace.
func (r *devRun) ensureNamed(ctx context.Context, st store, name namespace.Name) (bool, error) {
	installationName := cli.ByFlag("installation").Default

	svc, err := namespace.NewService(
		namespace.Config{Store: st, Now: nil, Installation: namespace.InstallationName(installationName)},
	)
	if err != nil {
		return false, fmt.Errorf("namespace service: %w", err)
	}

	_, err = svc.Create(ctx, name)

	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, namespace.ErrAlreadyExists):
		return false, nil
	default:
		return false, fmt.Errorf("make the namespace %s: %w", name, err)
	}
}

// devCollection is the collection that the placement rule of dev names.
const devCollection = "everything"

// placementPorts is what dev needs to make a collection and a placement rule.
// The SQLite store of dev keeps both.
type placementPorts interface {
	collection.Store
	collection.PlacementStore
}

// seedPlacement makes the collection devCollection in the namespace ns and
// two placement rules that put each new Pulumi stack and each new OpenTofu
// workspace in it. The server refuses a new stack or workspace that no rule
// places, so the first "pulumi stack init" or "tofu init" of a trial needs the
// rules. The server itself makes no rule: this is a step of the dev
// command, as the administrator of dev, and the audit log holds both entries.
func (r *devRun) seedPlacement(ctx context.Context, in runInputs, ns namespace.Name) error {
	ports, ok := in.st.(placementPorts)
	if !ok {
		return fmt.Errorf("make the placement rule of %s: %w", ns, errDevPlacementStore)
	}

	admin, err := in.authn.AuthenticateToken(ctx, r.token.value)
	if err != nil {
		return fmt.Errorf("administrator of dev: %w", err)
	}

	collections, err := collection.NewService(collection.Config{Store: ports, Namespaces: in.st, Now: r.proc.now})
	if err != nil {
		return fmt.Errorf("collection service: %w", err)
	}

	rules, err := collection.NewPlacementService(
		collection.PlacementConfig{Store: ports, Namespaces: in.st, Now: r.proc.now},
	)
	if err != nil {
		return fmt.Errorf("placement service: %w", err)
	}

	entry := func(action identity.Action, object string) audit.Entry {
		e := cli.SettingsEntry(audit.ChannelStartup, admin.Subject, r.proc.now(), nil)
		e.Namespace = ns
		e.Action = string(action)
		e.Object = object

		return e
	}

	ref := collection.Ref{Namespace: ns, Name: devCollection}
	created := entry(identity.ActionCollectionCreate, "collection:"+ref.String())

	if _, err = collections.Create(ctx, ref, created); err != nil {
		return fmt.Errorf("make the collection %s: %w", ref, err)
	}

	for _, tool := range []string{collection.ToolPulumi, collection.ToolOpenTofu} {
		input := collection.RuleInput{Tool: tool, Project: "*", Stack: "*", Collection: devCollection, Position: 0}
		ruled := entry(identity.ActionNamespaceManagePlacementRules, "namespace:"+ns.String())

		if _, err = rules.Create(ctx, ns, input, ruled); err != nil {
			return fmt.Errorf("make the %s placement rule of %s: %w", tool, ns, err)
		}
	}

	return nil
}

// placementLine answers the text of the Placement line of the banner on the
// first start: the rule that dev made and the command that removes it. It
// answers the empty string on a later start, when dev makes no rule.
func (r *devRun) placementLine(serverURL string) string {
	if !r.fresh {
		return ""
	}

	where := "in the namespace " + devNamespace
	if r.flags.demo {
		where = "in each of the namespaces " + strings.Join(demoNamespaceNames(), ", ")
	}

	path := serverURL + "/api/dustpan/v1/namespaces/<namespace>/placement-rules"

	return where + `, a rule for each tool puts each new Pulumi stack and each new OpenTofu workspace in the collection ` +
		devCollection + `. List it with: curl -H "Authorization: Bearer $TOKEN" ` + path +
		`; delete it with: curl -X DELETE -H "Authorization: Bearer $TOKEN" ` + path + `/<id>`
}

// startChanges answers the changes of the start audit entry: the command,
// the installation kind and the settings that tell how the server runs.
func (r *devRun) startChanges() []audit.Change {
	change := func(key, value string) audit.Change {
		return audit.Change{Key: key, Old: nil, New: value, Layer: "flag", TakesEffect: ""}
	}

	return []audit.Change{
		kindChange(installation.KindDev),
		change("command", commandDev),
		change("listen", r.flags.listen),
		change("tls", strconv.FormatBool(r.flags.tls)),
		change("ephemeral", strconv.FormatBool(r.flags.ephemeral)),
		change("demo", strconv.FormatBool(r.flags.demo)),
	}
}

// serveFlags answers the settings of the server code of serve for dev: the
// defaults of serve for each setting that dev does not take, and the values of
// the flags of dev for the others. Dev reads no variable of serve, so the
// defaults come from the declarations and never from the environment.
func (r *devRun) serveFlags(serverURL string, cert devCert, instanceID string) (serveFlags, error) {
	retention, err := parseRetention(cli.ByFlag("soft-delete-retention").Default)
	if err != nil {
		return serveFlags{}, err
	}

	cutoff, err := strconv.ParseInt(cli.ByFlag("checkpoint-delta-cutoff").Default, 10, 64)
	if err != nil {
		return serveFlags{}, fmt.Errorf("checkpoint delta cutoff default: %w", err)
	}

	var f serveFlags

	f.installationKind = installation.KindDev
	f.instanceID = instanceID
	f.installation = cli.ByFlag("installation").Default
	f.database = "sqlite:" + filepath.Join(r.dir, devDBName)
	f.listen = r.flags.listen
	f.consoleDir = r.flags.consoleDir
	f.origins = strings.Join(devOrigins(serverURL), ",")
	f.tlsCert = cert.certFile
	f.tlsKey = cert.keyFile
	f.publicURL = serverURL
	f.trustedProxyHeader = cli.ByFlag("trusted-proxy-header").Default
	f.logLevel = r.flags.logLevel
	f.logFormat = r.flags.logFormat
	f.maxCheckpointBytes = r.flags.maxCheckpoint
	f.maxSecretBatchBytes = r.flags.maxSecretBatch
	f.maxStateBytes = r.flags.maxState
	f.blobCacheBytes = r.flags.blobCache
	f.maxBodyBytesInFlight = r.flags.maxBodyFlight
	f.checkpointDeltaCutoff = cutoff
	f.disablePulumiJournaling = r.flags.noJournaling
	f.auditChain = cli.ByFlag("audit-chain").Default
	f.softDeleteRetention = cli.ByFlag("soft-delete-retention").Default
	f.retention = retention
	f.planRetention = cli.ByFlag(history.PlanRetentionFlag).Default
	f.jobLogRetention = cli.ByFlag(history.JobLogRetentionFlag).Default

	if err := f.checkJobRetention(); err != nil {
		return serveFlags{}, err
	}

	return f, nil
}

// devOrigins answers the origins that may send a request with the session
// cookie: the origin of the server, and the same port on localhost, because a
// browser can reach a loopback server through either name.
func devOrigins(serverURL string) []string {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil
	}

	port := parsed.Port()
	if port == "" {
		return nil
	}

	other := parsed.Scheme + "://localhost:" + port
	if other == serverURL {
		return nil
	}

	return []string{other}
}
