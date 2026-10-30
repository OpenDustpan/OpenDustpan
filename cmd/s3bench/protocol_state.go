package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"
)

// Files of the protocol run in the output directory.
const (
	protocolFileName   = "protocol.json"
	protocolReportName = "protocol.md"
)

// Defaults of the protocol run: the protocol asks for at least 10,000
// operations of each family and at least 5 runs of each path.
const (
	defaultProtocolOps   = 10000
	defaultProtocolRuns  = 5
	defaultProtocolConc  = 16
	protocolMeasurePct   = 5.0
	protocolPercentScale = 100.0
)

// errPlanMismatch reports a resume with a plan that differs from the plan
// of the results that protocol.json holds.
var errPlanMismatch = errors.New("the plan differs from the plan of the stored results; " +
	"use the same -ops, -runs and -concurrency, or another BENCH_S3_OUT")

// checkResumePlan refuses a requested plan that differs from the stored plan
// when the file holds results.
func checkResumePlan(stored, requested protocolPlan, results int) error {
	if results > 0 && stored != requested {
		return fmt.Errorf("%w: stored ops %d, runs %d, concurrency %d; requested ops %d, runs %d, concurrency %d",
			errPlanMismatch, stored.Ops, stored.Runs, stored.Concurrency,
			requested.Ops, requested.Runs, requested.Concurrency)
	}

	return nil
}

// errProtocolPlan reports a protocol plan with no operation.
var errProtocolPlan = errors.New("-ops, -runs and -concurrency must be 1 or more")

// familyInfo describes one family in the protocol file.
type familyInfo struct {
	Name    string `json:"name"`
	Bytes   int    `json:"bytes"`
	Read    bool   `json:"read"`
	OldPath string `json:"oldPath"`
	NewPath string `json:"newPath"`
}

// protocolFile is the content of protocol.json: the plan, the families and
// every result so far. A resumed run reads it and skips the results it
// holds.
type protocolFile struct {
	Plan     protocolPlan `json:"plan"`
	Host     hostInfo     `json:"host"`
	Families []familyInfo `json:"families"`
	// Enforced says for each store whether it refused a second write with
	// If-None-Match. A store that does not enforce it gives no verdict: its
	// old path does not run as intended.
	Enforced map[string]bool `json:"enforced"`
	Results  []protoResult   `json:"results"`
}

// readProtocol reads protocol.json of dir, or answers an empty file.
func readProtocol(dir string) (*protocolFile, error) {
	data, err := readIn(dir, protocolFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return &protocolFile{
			Plan:     protocolPlan{Ops: 0, Runs: 0, Concurrency: 0},
			Host:     readHost(),
			Families: nil,
			Enforced: map[string]bool{},
			Results:  nil,
		}, nil
	}

	if err != nil {
		return nil, err
	}

	var pf protocolFile

	if err := json.Unmarshal(data, &pf); err != nil {
		return nil, fmt.Errorf("decode %s: %w", protocolFileName, err)
	}

	if pf.Enforced == nil {
		pf.Enforced = map[string]bool{}
	}

	// A file of an earlier version keeps the run count in its plan only.
	for i := range pf.Results {
		if pf.Results[i].Runs == 0 {
			pf.Results[i].Runs = pf.Plan.Runs
		}
	}

	return &pf, nil
}

// writeProtocol writes protocol.json and protocol.md of dir.
func writeProtocol(dir string, pf *protocolFile) error {
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", protocolFileName, err)
	}

	if err := writeIn(dir, protocolFileName, append(data, '\n')); err != nil {
		return err
	}

	return writeIn(dir, protocolReportName, []byte(renderProtocol(pf)))
}

// runProtocol runs the protocol families of -profile on each store and
// saves each result as it ends.
func runProtocol(ctx context.Context, logger *slog.Logger, o options) error {
	if o.protoOps < 1 || o.protoRuns < 1 || o.protoConc < 1 {
		return errProtocolPlan
	}

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

	pf, err := readProtocol(o.out)
	if err != nil {
		return err
	}

	requested := protocolPlan{Ops: o.protoOps, Runs: o.protoRuns, Concurrency: o.protoConc}
	if planErr := checkResumePlan(pf.Plan, requested, len(pf.Results)); planErr != nil {
		return planErr
	}

	pf.Plan = requested
	pf.Families = familyInfos()

	for store, check := range res.Conditional {
		pf.Enforced[store] = check.Enforced
	}

	db, err := openPGModel(ctx, o.pgURL(creds))
	if err != nil {
		return err
	}

	runErr := runProtocolStores(
		ctx,
		logger,
		o,
		protocolStores{creds: creds, db: db, runID: res.RunID, profile: p.Name},
		pf,
	)

	return errors.Join(runErr, db.close(context.WithoutCancel(ctx)))
}

// protocolStores holds what the stores of one protocol profile share.
type protocolStores struct {
	creds   secrets
	db      *pgModel
	runID   string
	profile string
}

// runProtocolStores runs every family on each store of o.
func runProtocolStores(ctx context.Context, logger *slog.Logger, o options, ps protocolStores, pf *protocolFile) error {
	done := map[string]bool{}
	for i := range pf.Results {
		done[pf.Results[i].id()] = true
	}

	save := func(r protoResult) error {
		pf.Results = append(pf.Results, r)

		return writeProtocol(o.out, pf)
	}

	for _, store := range o.stores {
		t, err := o.targetFor(store, ps.creds)
		if err != nil {
			return err
		}

		runner := protocolRunner{
			plan: pf.Plan, profile: ps.profile, target: t, db: ps.db, runID: ps.runID, logger: logger,
		}

		for _, f := range protocolFamilies() {
			if err := runner.runFamily(ctx, f, done, save); err != nil {
				return err
			}
		}
	}

	return writeProtocol(o.out, pf)
}

// familyInfos answers the description of each protocol family.
func familyInfos() []familyInfo {
	fams := protocolFamilies()
	out := make([]familyInfo, 0, len(fams))

	for _, f := range fams {
		out = append(
			out,
			familyInfo{Name: f.Name, Bytes: f.Bytes, Read: f.Read, OldPath: f.OldPath, NewPath: f.NewPath},
		)
	}

	return out
}

// pathSummary is the medians of the runs of one path, with their spread.
type pathSummary struct {
	Runs      int
	P50       spread
	P99       spread
	OpsPerSec spread
	MBPerSec  spread
	Errors    int
}

// spread is the median of the runs with their lowest and highest value.
type spread struct {
	Median float64
	Min    float64
	Max    float64
}

// spreadOf answers the spread of values.
func spreadOf(values []float64) spread {
	const median = 0.5

	if len(values) == 0 {
		return spread{Median: 0, Min: 0, Max: 0}
	}

	return spread{Median: quantile(values, median), Min: slices.Min(values), Max: slices.Max(values)}
}

// summarize answers the summary of results.
func summarize(results []protoResult) pathSummary {
	p50 := make([]float64, 0, len(results))
	p99 := make([]float64, 0, len(results))
	ops := make([]float64, 0, len(results))
	mbs := make([]float64, 0, len(results))
	errs := 0

	for i := range results {
		const mib = 1 << 20

		r := &results[i]

		p50 = append(p50, r.P50Ms)
		p99 = append(p99, r.P99Ms)
		ops = append(ops, r.OpsPerSec)
		mbs = append(mbs, r.BytesPerSec/mib)
		errs += r.Errors
	}

	return pathSummary{
		Runs: len(results), P50: spreadOf(p50), P99: spreadOf(p99),
		OpsPerSec: spreadOf(ops), MBPerSec: spreadOf(mbs), Errors: errs,
	}
}

// deltaPct answers how much higher new is than old, in percent.
func deltaPct(oldValue, newValue float64) float64 {
	if oldValue == 0 {
		return math.NaN()
	}

	return (newValue - oldValue) / oldValue * protocolPercentScale
}

// verdict answers whether the new path meets the measure: its median p50
// and median p99 are at most 5 percent higher, and its median throughput is
// at most 5 percent lower.
func verdict(oldPath, newPath pathSummary) string {
	if oldPath.Runs == 0 || newPath.Runs == 0 {
		return "incomplete"
	}

	if deltaPct(oldPath.P50.Median, newPath.P50.Median) <= protocolMeasurePct &&
		deltaPct(oldPath.P99.Median, newPath.P99.Median) <= protocolMeasurePct &&
		deltaPct(oldPath.OpsPerSec.Median, newPath.OpsPerSec.Median) >= -protocolMeasurePct {
		return "meets"
	}

	return "does not meet"
}

// renderProtocol answers the summary tables of the protocol run.
func renderProtocol(pf *protocolFile) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Protocol run\n\n%d operations for each run, %d runs of each path, concurrency %d. "+
		"Host: %s, %d CPUs, %s memory.\n\n", pf.Plan.Ops, pf.Plan.Runs, pf.Plan.Concurrency,
		pf.Host.CPU, pf.Host.CPUs, pf.Host.Memory)

	if pf.Plan.Ops < defaultProtocolOps || pf.Plan.Runs < defaultProtocolRuns {
		b.WriteString("This run is below the minimums of the protocol (10,000 operations, 5 runs). " +
			"It is a smoke run, not a record.\n\n")
	}

	b.WriteString("Values are the median of the runs, with the lowest and the highest run in brackets. " +
		"Δ is the new path against the old path.\n\n")
	b.WriteString("| Profile | Store | Family | Path | p50 ms | p99 ms | ops/s | MiB/s | Errors |\n" +
		"|---|---|---|---|---|---|---|---|---|\n")

	for _, key := range groupKeys(pf.Results) {
		oldS := summarize(filterResults(pf.Results, key, modeOld))
		newS := summarize(filterResults(pf.Results, key, modeNew))

		oldLabel, result := modeOld, verdict(oldS, newS)
		if !pf.Enforced[key.store] {
			oldLabel, result = modeOld+", not enforced", "no verdict: the store does not enforce If-None-Match"
		}

		writePathRow(&b, key, oldLabel, oldS)
		writePathRow(&b, key, modeNew, newS)
		fmt.Fprintf(
			&b,
			"| %s | %s | %s | Δ | %+.1f %% | %+.1f %% | %+.1f %% | %+.1f %% | %s |\n",
			key.profile,
			key.store,
			key.family,
			deltaPct(oldS.P50.Median, newS.P50.Median),
			deltaPct(oldS.P99.Median, newS.P99.Median),
			deltaPct(
				oldS.OpsPerSec.Median,
				newS.OpsPerSec.Median,
			),
			deltaPct(oldS.MBPerSec.Median, newS.MBPerSec.Median),
			result,
		)
	}

	writeGapNotes(&b, pf.Results)

	return b.String()
}

// writePathRow writes the row of one path.
func writePathRow(b *strings.Builder, key groupKey, mode string, s pathSummary) {
	fmt.Fprintf(b, "| %s | %s | %s | %s (%d runs) | %s | %s | %s | %s | %d |\n",
		key.profile, key.store, key.family, mode, s.Runs,
		spreadText(s.P50, "%.1f"), spreadText(s.P99, "%.1f"), spreadText(s.OpsPerSec, "%.0f"),
		spreadText(s.MBPerSec, "%.1f"), s.Errors)
}

// spreadText answers "median [min, max]" in format f.
func spreadText(s spread, f string) string {
	return fmt.Sprintf(f+" ["+f+", "+f+"]", s.Median, s.Min, s.Max)
}

// groupKey names the results of one family on one store and profile.
type groupKey struct {
	profile string
	store   string
	family  string
}

// groupKeys answers each group of results once, in the order of results.
func groupKeys(results []protoResult) []groupKey {
	var out []groupKey

	seen := map[groupKey]bool{}

	for i := range results {
		r := &results[i]
		k := groupKey{profile: r.Profile, store: r.Store, family: r.Family}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}

	return out
}

// filterResults answers the results of key in mode.
func filterResults(results []protoResult, key groupKey, mode string) []protoResult {
	var out []protoResult

	for i := range results {
		r := results[i]
		if r.Profile == key.profile && r.Store == key.store && r.Family == key.family && r.Mode == mode {
			out = append(out, r)
		}
	}

	return out
}

// longGap is the gap between two runs of one family above which the report
// names it: a resumed run can split the runs of a family over hours.
const longGap = 30 * time.Minute

// writeGapNotes names each family whose runs have a gap above longGap
// between two runs that follow each other.
func writeGapNotes(b *strings.Builder, results []protoResult) {
	var notes []string

	for _, key := range groupKeys(results) {
		if gap := largestGap(results, key); gap > longGap {
			notes = append(notes, fmt.Sprintf("- %s, %s, %s: a gap of %s between two runs. "+
				"The run stopped and resumed there.", key.profile, key.store, key.family, gap.Round(time.Minute)))
		}
	}

	if len(notes) == 0 {
		return
	}

	b.WriteString("\nResumed families:\n\n" + strings.Join(notes, "\n") + "\n")
}

// largestGap answers the largest gap between the start times of two runs of
// key that follow each other. A time that does not parse counts as no gap.
func largestGap(results []protoResult, key groupKey) time.Duration {
	var times []time.Time

	for i := range results {
		r := &results[i]
		if r.Profile != key.profile || r.Store != key.store || r.Family != key.family {
			continue
		}

		if t, err := time.Parse(time.RFC3339, r.Time); err == nil {
			times = append(times, t)
		}
	}

	slices.SortFunc(times, func(a, c time.Time) int { return a.Compare(c) })

	var largest time.Duration

	for i := 1; i < len(times); i++ {
		largest = max(largest, times[i].Sub(times[i-1]))
	}

	return largest
}
