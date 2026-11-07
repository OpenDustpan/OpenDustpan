package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"opendustpan.org/opendustpan/pkg/blob/s3"
)

// s3Region signs every S3 request of the run. Both stores accept it: the
// Garage configuration of scripts/bench-s3.sh sets it.
const s3Region = "us-east-1"

// errBadFlag reports a flag value the bench cannot use.
var errBadFlag = errors.New("bad flag")

// errUsage reports a missing or unknown subcommand.
var errUsage = errors.New("usage: s3bench setup|profiles|measure|protocol|render|reset [flags]")

// Store names of the -stores flag.
const (
	storeMinIO  = "minio"
	storeGarage = "garage"
)

// Default proxy ports. scripts/bench-s3.sh passes its own.
const (
	defaultMinIOPort  = 18475
	defaultGaragePort = 18476
	defaultPGPort     = 18477
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	err := run(ctx, logger, os.Args[1:], os.Stdout)

	stop()

	if err != nil {
		logger.ErrorContext(ctx, "s3 bench", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

// run dispatches the subcommand of args.
func run(ctx context.Context, logger *slog.Logger, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errUsage
	}

	opts, err := parseOptions(args[0], args[1:])
	if err != nil {
		return err
	}

	switch args[0] {
	case "setup":
		return setUp(ctx, opts)
	case "profiles":
		return listProfiles(opts, stdout)
	case "measure":
		return measure(ctx, logger, opts)
	case "protocol":
		return runProtocol(ctx, logger, opts)
	case "render":
		return render(opts)
	case "reset":
		return resetToxics(ctx, opts)
	default:
		return errUsage
	}
}

// options holds the parsed flags of every subcommand.
type options struct {
	out          string
	secretsDir   string
	toxiproxyAPI string
	listenHost   string
	connectHost  string
	ports        map[string]int
	upstreams    map[string]string
	bucket       string
	stores       []string
	profiles     string
	profile      string
	quick        bool
	limits       bool
	limitS3      string
	limitDB      string
	limitStore   string
	only         string
	protoOps     int
	protoRuns    int
	protoConc    int
	families     string
}

// parseOptions parses the flags of subcommand cmd.
func parseOptions(cmd string, args []string) (options, error) {
	fs := flag.NewFlagSet("s3bench "+cmd, flag.ContinueOnError)

	o := options{
		out: "", secretsDir: "", toxiproxyAPI: "", listenHost: "", connectHost: "",
		ports: map[string]int{}, upstreams: map[string]string{}, bucket: "", stores: nil,
		profiles: "", profile: "", quick: false, limits: false, limitS3: "", limitDB: "", limitStore: "", only: "",
		protoOps: 0, protoRuns: 0, protoConc: 0, families: "",
	}

	fs.StringVar(&o.out, "out", "bench-s3-out", "results `dir`: results.json, results.md and load/")
	fs.StringVar(&o.secretsDir, "secrets", "", "read the throwaway credentials from `dir`")
	fs.StringVar(&o.toxiproxyAPI, "toxiproxy", "http://127.0.0.1:18474", "Toxiproxy API `url`")
	fs.StringVar(&o.listenHost, "listen-host", "0.0.0.0", "`host` the proxies listen on inside Toxiproxy")
	fs.StringVar(&o.connectHost, "connect-host", "127.0.0.1", "`host` the bench dials to reach the proxies")
	minioPort := fs.Int("minio-port", defaultMinIOPort, "proxy `port` of MinIO")
	garagePort := fs.Int("garage-port", defaultGaragePort, "proxy `port` of Garage")
	pgPort := fs.Int("pg-port", defaultPGPort, "proxy `port` of PostgreSQL")
	minioUp := fs.String("minio-upstream", "minio:9000", "MinIO `address` as Toxiproxy reaches it")
	garageUp := fs.String("garage-upstream", "garage:3900", "Garage `address` as Toxiproxy reaches it")
	pgUp := fs.String("pg-upstream", "postgres:5432", "PostgreSQL `address` as Toxiproxy reaches it")
	fs.StringVar(&o.bucket, "bucket", "bench", "bucket `name` on each store")
	stores := fs.String("stores", storeMinIO+","+storeGarage, "comma-separated `stores`")
	fs.StringVar(&o.profiles, "profiles", "all", "profiles: comma-separated `names`, or all")
	fs.StringVar(&o.profile, "profile", "", "measure: the profile or limit step `name`")
	fs.BoolVar(&o.quick, "quick", false, "setup: fewer repetitions, for a CI smoke run")
	fs.BoolVar(&o.limits, "limits", false, "profiles: list the limit sweep steps after the profiles")
	fs.StringVar(&o.limitS3, "limit-s3", "0,50,100,250,500,1000,2000,5000", "S3 round trips of the limit sweep in `ms`")
	fs.StringVar(&o.limitDB, "limit-db", "0,5,10,25,50,100,250,500,1000",
		"PostgreSQL round trips of the limit sweep in `ms`")
	fs.StringVar(&o.limitStore, "limit-store", storeMinIO, "`store` of the limit sweep")
	fs.IntVar(&o.protoOps, "ops", defaultProtocolOps, "protocol: operations of each run")
	fs.IntVar(&o.protoRuns, "runs", defaultProtocolRuns, "protocol: runs of each path")
	fs.IntVar(&o.protoConc, "concurrency", defaultProtocolConc, "protocol: client `workers` of each run")
	fs.StringVar(&o.families, "families", "",
		"protocol: comma-separated family `slugs`, for example pulumi-checkpoint (default all)")
	fs.StringVar(&o.only, "only", "", "measure: comma-separated `parts`: puts, sequences, sweep (default all)")

	if err := fs.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}

	o.ports[storeMinIO], o.ports[storeGarage], o.ports["pg"] = *minioPort, *garagePort, *pgPort
	o.upstreams[storeMinIO], o.upstreams[storeGarage], o.upstreams["pg"] = *minioUp, *garageUp, *pgUp

	for s := range strings.SplitSeq(*stores, ",") {
		s = strings.TrimSpace(s)
		if s != storeMinIO && s != storeGarage {
			return options{}, fmt.Errorf("%w: unknown store %q", errBadFlag, s)
		}

		o.stores = append(o.stores, s)
	}

	return o, nil
}

// runs reports whether the -only flag selects part.
func (o options) runs(part string) bool {
	if o.only == "" {
		return true
	}

	for p := range strings.SplitSeq(o.only, ",") {
		if strings.TrimSpace(p) == part {
			return true
		}
	}

	return false
}

// addr answers the address the bench dials for the proxy of name.
func (o options) addr(name string) string {
	return net.JoinHostPort(o.connectHost, strconv.Itoa(o.ports[name]))
}

// pgURL answers the URL of the bench database through its proxy.
func (o options) pgURL(creds secrets) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword("bench", creds.byName["pg-password"]),
		Host:     o.addr("pg"),
		Path:     "/bench",
		RawQuery: "sslmode=disable",
	}

	return u.String()
}

// allSteps answers the standard profiles of -profiles and, with -limits,
// the limit sweep steps after them.
func (o options) allSteps() ([]profile, error) {
	steps, err := selectProfiles(standardProfiles(), o.profiles)
	if err != nil {
		return nil, err
	}

	if !o.limits {
		return steps, nil
	}

	s3Steps, err := parseSteps(o.limitS3)
	if err != nil {
		return nil, err
	}

	dbSteps, err := parseSteps(o.limitDB)
	if err != nil {
		return nil, err
	}

	return append(steps, limitSteps(s3Steps, dbSteps)...), nil
}

// findStep answers the profile or limit step called name. A limit step is
// found by the -limit-s3 and -limit-db values, so measure takes the same
// values as profiles.
func (o options) findStep(name string) (profile, error) {
	every := o
	every.profiles, every.limits = "all", true

	steps, err := every.allSteps()
	if err != nil {
		return profile{}, err
	}

	for _, p := range steps {
		if p.Name == name {
			return p, nil
		}
	}

	return profile{}, fmt.Errorf("%w: unknown profile %q", errBadFlag, name)
}

// listProfiles writes one line for each step, with fields split by spaces:
// the step name, its kind (profile or limit), the comma-separated stores of
// the load harness runs, and the -reps, -dataset-units and -max-duration
// values of those runs. scripts/bench-s3.sh reads the lines to drive the run.
func listProfiles(o options, stdout io.Writer) error {
	steps, err := o.allSteps()
	if err != nil {
		return err
	}

	res, err := readResults(o.out)
	if err != nil {
		return err
	}

	for _, p := range steps {
		line := fmt.Sprintf("%s profile %s %d %d %dm", p.Name, strings.Join(o.stores, ","),
			res.Plan.LoadReps, res.Plan.LoadDataset, res.Plan.LoadMinutes)
		if p.Limit {
			line = fmt.Sprintf("%s limit %s %d %d %dm", p.Name, o.limitStore,
				res.Plan.LimitReps, res.Plan.LimitDataset, res.Plan.LimitMinutes)
		}

		if _, writeErr := fmt.Fprintln(stdout, line); writeErr != nil {
			return fmt.Errorf("write the profile list: %w", writeErr)
		}
	}

	return nil
}

// targetFor opens the target of store with the run credentials.
func (o options) targetFor(store string, creds secrets) (*s3Target, error) {
	c := s3.Credentials{AccessKeyID: creds.access(store), SecretAccessKey: creds.secret(store), SessionToken: ""}

	return newS3Target(store, "http://"+o.addr(store), o.bucket, c)
}

// setUp makes the proxies, creates the buckets, checks conditional writes on
// each store and writes a fresh results.json.
func setUp(ctx context.Context, o options) error {
	creds, err := readSecrets(o.secretsDir)
	if err != nil {
		return err
	}

	toxi, err := newToxiproxy(o.toxiproxyAPI)
	if err != nil {
		return err
	}

	for _, name := range append([]string{"pg"}, o.stores...) {
		listen := net.JoinHostPort(o.listenHost, strconv.Itoa(o.ports[name]))

		if proxyErr := toxi.ensureProxy(ctx, name, listen, o.upstreams[name]); proxyErr != nil {
			return proxyErr
		}
	}

	runID, err := newKey("run", 0)
	if err != nil {
		return err
	}

	chosen := fullPlan()
	if o.quick {
		chosen = quickPlan()
	}

	res := newResults(chosen, strings.ReplaceAll(runID, "/", "-"))

	for _, name := range o.stores {
		check, checkErr := checkStore(ctx, o, creds, name, "bench/"+res.RunID+"/conditional-check")
		if checkErr != nil {
			return checkErr
		}

		res.Conditional[name] = check
	}

	return writeResults(o.out, res)
}

// checkStore creates the bucket of store and checks whether the store
// enforces If-None-Match with a write to key.
func checkStore(ctx context.Context, o options, creds secrets, store, key string) (conditionalCheck, error) {
	t, err := o.targetFor(store, creds)
	if err != nil {
		return conditionalCheck{}, err
	}

	if bucketErr := t.createBucket(ctx); bucketErr != nil {
		return conditionalCheck{}, bucketErr
	}

	check, err := t.checkConditional(ctx, key)
	if err != nil {
		return conditionalCheck{}, fmt.Errorf("check conditional writes on %s: %w", store, err)
	}

	return check, nil
}

// setProfile sets the delay of p on the proxy of each store and of
// PostgreSQL.
func setProfile(ctx context.Context, o options, p profile) error {
	toxi, err := newToxiproxy(o.toxiproxyAPI)
	if err != nil {
		return err
	}

	if err := toxi.setLatency(ctx, "pg", p.DB); err != nil {
		return err
	}

	for _, name := range o.stores {
		if err := toxi.setLatency(ctx, name, p.S3); err != nil {
			return err
		}
	}

	return nil
}

// resetToxics removes the delay from every proxy.
func resetToxics(ctx context.Context, o options) error {
	zero := link{RTTMs: 0, JitterMs: 0}

	return setProfile(ctx, o, profile{Name: "reset", S3: zero, DB: zero, Measured: "", Limit: false})
}

// measure sets the delay of -profile and, for a standard profile, measures
// the puts, the sequences and the sweep on each store. A limit step only
// sets the delay and checks the database round trip: the script then runs
// the load harness.
func measure(ctx context.Context, logger *slog.Logger, o options) error {
	p, err := o.findStep(o.profile)
	if err != nil {
		return err
	}

	if setErr := setProfile(ctx, o, p); setErr != nil {
		return setErr
	}

	creds, err := readSecrets(o.secretsDir)
	if err != nil {
		return err
	}

	res, err := readResults(o.out)
	if err != nil {
		return err
	}

	db, err := openPGModel(ctx, o.pgURL(creds))
	if err != nil {
		return err
	}

	m := measurement{opts: o, creds: creds, db: db, plan: res.Plan, runID: res.RunID, logger: logger}

	if p.Limit {
		res.Limits = append(res.Limits, limitResult{Profile: p, Store: o.limitStore, DBPing: m.ping(ctx), Load: nil})
	} else {
		res.Profiles = append(res.Profiles, m.profile(ctx, p))
	}

	return errors.Join(db.close(context.WithoutCancel(ctx)), writeResults(o.out, res))
}

// measurement holds what the parts of one profile share.
type measurement struct {
	opts   options
	creds  secrets
	db     *pgModel
	plan   plan
	runID  string
	logger *slog.Logger
}

// dbPingReps is the count of timed database round trips of each step.
const dbPingReps = 50

// ping times dbPingReps database round trips.
func (m measurement) ping(ctx context.Context) stats {
	var ping tally
	for range dbPingReps {
		ping.time(func() error { return m.db.ping(ctx) })
	}

	return ping.stats()
}

// profile measures p on each store.
func (m measurement) profile(ctx context.Context, p profile) profileResult {
	out := profileResult{Profile: p, Stores: nil}

	for _, name := range m.opts.stores {
		m.logger.InfoContext(ctx, "measure", slog.String("profile", p.Name), slog.String("store", name))
		out.Stores = append(out.Stores, m.store(ctx, p, name))
	}

	return out
}

// store runs every selected part of profile p on one store.
func (m measurement) store(ctx context.Context, p profile, name string) storeResult {
	out := storeResult{Store: name, DBPing: m.ping(ctx), Puts: nil, Sequences: nil, Sweep: nil, Load: nil, Errors: nil}

	t, err := m.opts.targetFor(name, m.creds)
	if err != nil {
		out.addError("open", err)

		return out
	}

	prefix := "bench/" + m.runID + "/" + p.Name + "/" + name

	if m.opts.runs("puts") {
		out.Puts, err = t.measurePuts(ctx, prefix, m.plan.Sizes)
		out.addError("puts", err)
	}

	if m.opts.runs("sequences") {
		run := sequenceRun{target: t, db: m.db, prefix: prefix + "/seq", reps: m.plan.SequenceReps}
		out.Sequences, err = run.measure(ctx, m.plan.SequenceBody)
		out.addError("sequences", err)
	}

	if m.opts.runs("sweep") {
		sweep, sweepErr := measureSweep(ctx, t, m.db, prefix, m.plan.SweepObjects)
		if sweepErr == nil {
			out.Sweep = &sweep
		}

		out.addError("sweep", sweepErr)
	}

	return out
}

// parseSteps parses a comma-separated list of whole milliseconds.
func parseSteps(raw string) ([]int, error) {
	var out []int

	for part := range strings.SplitSeq(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 {
			return nil, fmt.Errorf("%w: step %q", errBadFlag, part)
		}

		out = append(out, n)
	}

	return out, nil
}
