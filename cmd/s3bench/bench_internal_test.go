package main

// The tests stay white-box (package main): a main package cannot be
// imported.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestLinkSplit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        link
		wantUp    latencyAttrs
		wantDown  latencyAttrs
		wantTotal int
	}{
		{
			"zero",
			link{RTTMs: 0, JitterMs: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			0,
		},
		{
			"one",
			link{RTTMs: 1, JitterMs: 0},
			latencyAttrs{Latency: 0, Jitter: 0},
			latencyAttrs{Latency: 1, Jitter: 0},
			1,
		},
		{
			"odd",
			link{RTTMs: 65, JitterMs: 4},
			latencyAttrs{Latency: 32, Jitter: 4},
			latencyAttrs{Latency: 33, Jitter: 4},
			65,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pair := tt.in.split()
			up, down := pair.up, pair.down

			if up != tt.wantUp || down != tt.wantDown || up.Latency+down.Latency != tt.wantTotal {
				t.Fatalf("split(%+v) = %+v, %+v, want %+v, %+v", tt.in, up, down, tt.wantUp, tt.wantDown)
			}
		})
	}
}

func TestQuantile(t *testing.T) {
	t.Parallel()

	samples := []float64{5, 1, 4, 2, 3}

	if got := quantile(samples, 0.5); got != 3 {
		t.Fatalf("median = %v, want 3", got)
	}

	if got := quantile(samples, 0.99); got != 5 {
		t.Fatalf("p99 = %v, want 5", got)
	}

	if got := quantile(nil, 0.5); got != 0 {
		t.Fatalf("median of nothing = %v, want 0", got)
	}

	if samples[0] != 5 {
		t.Fatalf("quantile sorted its input: %v", samples)
	}
}

func TestParseSteps(t *testing.T) {
	t.Parallel()

	got, err := parseSteps("0, 50,1000")
	if err != nil || len(got) != 3 || got[2] != 1000 {
		t.Fatalf("parseSteps = %v, %v, want [0 50 1000]", got, err)
	}

	for _, bad := range []string{"", "-1", "ten", "1,,2"} {
		if _, err := parseSteps(bad); !errors.Is(err, errBadFlag) {
			t.Fatalf("parseSteps(%q) error = %v, want errBadFlag", bad, err)
		}
	}
}

func TestSelectProfiles(t *testing.T) {
	t.Parallel()

	all := standardProfiles()

	got, err := selectProfiles(all, "eu-metro,same-az")
	if err != nil || len(got) != 2 {
		t.Fatalf("selectProfiles = %v, %v, want two profiles", got, err)
	}

	if _, err := selectProfiles(all, "moon"); !errors.Is(err, errBadFlag) {
		t.Fatalf("selectProfiles(moon) error = %v, want errBadFlag", err)
	}
}

func TestFirstField(t *testing.T) {
	t.Parallel()

	text := "processor\t: 0\nmodel name\t: Test CPU @ 1.00GHz\nmodel name\t: other\n"

	if got := firstField(text, "model name"); got != "Test CPU @ 1.00GHz" {
		t.Fatalf("firstField = %q", got)
	}

	if got := firstField(text, "MemTotal"); got != "" {
		t.Fatalf("firstField of a missing name = %q, want empty", got)
	}
}

func TestRedact(t *testing.T) {
	t.Parallel()

	creds := secrets{byName: map[string]string{"pg-password": "s3cr3t-value"}}

	if got := creds.redact("connect: password s3cr3t-value refused"); got != "connect: password [redacted] refused" {
		t.Fatalf("redact = %q", got)
	}
}

func TestSlugOf(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"Pulumi checkpoint":             "pulumi-checkpoint",
		"restore write (key absent)":    "restore-write-key-absent",
		"audit checkpoint check (read)": "audit-checkpoint-check-read",
		"  --x":                         "x",
		"../../namespaces":              "namespaces",
	} {
		if got := slugOf(in); got != want {
			t.Fatalf("slugOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVerdict(t *testing.T) {
	t.Parallel()

	path := func(p50, p99, ops float64) pathSummary {
		return pathSummary{
			Runs: 5, P50: spread{Median: p50, Min: p50, Max: p50}, P99: spread{Median: p99, Min: p99, Max: p99},
			OpsPerSec: spread{Median: ops, Min: ops, Max: ops}, MBPerSec: spread{Median: 0, Min: 0, Max: 0}, Errors: 0,
		}
	}

	tests := []struct {
		name     string
		old, new pathSummary
		want     string
	}{
		{"equal", path(10, 20, 100), path(10, 20, 100), "meets"},
		{"p50 at 5 percent", path(10, 20, 100), path(10.5, 20, 100), "meets"},
		{"p50 above 5 percent", path(10, 20, 100), path(10.6, 20, 100), "does not meet"},
		{"p99 above 5 percent", path(10, 20, 100), path(10, 21.2, 100), "does not meet"},
		{"throughput 6 percent lower", path(10, 20, 100), path(10, 20, 94), "does not meet"},
		{"no runs", noRuns(), path(10, 20, 100), "incomplete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := verdict(tt.old, tt.new); got != tt.want {
				t.Fatalf("verdict = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	results := []protoResult{
		resultOf(3, 9, 100, 1<<20, 1),
		resultOf(1, 7, 300, 3<<20, 0),
		resultOf(2, 8, 200, 2<<20, 0),
	}

	s := summarize(results)
	if s.Runs != 3 || s.P50.Median != 2 || s.P50.Min != 1 || s.P50.Max != 3 || s.OpsPerSec.Median != 200 ||
		s.MBPerSec.Median != 2 || s.Errors != 1 {
		t.Fatalf("summarize = %+v", s)
	}
}

func TestAttemptPart(t *testing.T) {
	t.Parallel()

	if got := attemptPart("a/0-0a1b2c"); got != "0a1b2c" {
		t.Fatalf("attemptPart = %q", got)
	}
}

// noRuns answers the summary of a path with no run.
func noRuns() pathSummary {
	zero := spread{Median: 0, Min: 0, Max: 0}

	return pathSummary{Runs: 0, P50: zero, P99: zero, OpsPerSec: zero, MBPerSec: zero, Errors: 0}
}

// resultOf answers a protocol result with the timing fields set.
func resultOf(p50, p99, ops, bytesPerSec float64, errs int) protoResult {
	return protoResult{
		Profile: "p", Store: "s", Family: "f", Mode: modeOld, Run: 1, Ops: 1, Runs: 1, Concurrency: 1,
		Errors: errs, FirstError: "", P50Ms: p50, P99Ms: p99, WallMs: 1, OpsPerSec: ops, BytesPerSec: bytesPerSec,
		Time: "",
	}
}

func TestCheckResumePlan(t *testing.T) {
	t.Parallel()

	full := protocolPlan{Ops: 10000, Runs: 5, Concurrency: 16}
	smoke := protocolPlan{Ops: 200, Runs: 1, Concurrency: 16}

	if err := checkResumePlan(full, full, 3); err != nil {
		t.Fatalf("same plan: %v, want no error", err)
	}

	if err := checkResumePlan(smoke, full, 0); err != nil {
		t.Fatalf("a file with no results: %v, want no error", err)
	}

	for _, requested := range []protocolPlan{
		smoke,
		{Ops: 10000, Runs: 6, Concurrency: 16},
		{Ops: 10000, Runs: 5, Concurrency: 8},
	} {
		if err := checkResumePlan(full, requested, 1); !errors.Is(err, errPlanMismatch) {
			t.Fatalf("checkResumePlan(%+v) = %v, want errPlanMismatch", requested, err)
		}
	}
}

func TestResultIDHoldsThePlan(t *testing.T) {
	t.Parallel()

	a := resultOf(1, 2, 3, 4, 0)
	b := a
	b.Ops = 200

	c := a
	c.Concurrency = 8

	d := a
	d.Runs = 9

	if a.id() == b.id() || a.id() == c.id() || a.id() == d.id() {
		t.Fatalf("ids of different plans are equal: %q", a.id())
	}
}

func TestLargestGap(t *testing.T) {
	t.Parallel()

	at := func(clock string) protoResult {
		r := resultOf(1, 2, 3, 4, 0)
		r.Time = "2026-10-30T" + clock + "Z"

		return r
	}

	results := []protoResult{at("01:00:00"), at("01:02:00"), at("03:02:00"), at("03:04:00")}
	key := groupKey{profile: "p", store: "s", family: "f"}

	if got := largestGap(results, key); got != 2*time.Hour {
		t.Fatalf("largestGap = %v, want 2h", got)
	}

	var b strings.Builder

	writeGapNotes(&b, results)

	if !strings.Contains(b.String(), "a gap of 2h0m0s") {
		t.Fatalf("gap notes = %q", b.String())
	}
}

func TestRenderNoVerdictWithoutEnforcement(t *testing.T) {
	t.Parallel()

	oldRun, newRun := resultOf(10, 20, 100, 1, 0), resultOf(30, 60, 30, 1, 0)
	newRun.Mode = modeNew

	pf := &protocolFile{
		Plan: protocolPlan{Ops: 10000, Runs: 5, Concurrency: 16}, Host: readHost(), Families: nil,
		Enforced: map[string]bool{"s": false}, Results: []protoResult{oldRun, newRun}, Replaced: nil,
	}

	out := renderProtocol(pf)
	if !strings.Contains(out, "old, not enforced") || !strings.Contains(out, "no verdict") ||
		strings.Contains(out, "does not meet") {
		t.Fatalf("render of a store that does not enforce:\n%s", out)
	}

	pf.Enforced["s"] = true
	if out := renderProtocol(pf); !strings.Contains(out, "does not meet") {
		t.Fatalf("render of a store that enforces:\n%s", out)
	}
}

func TestReplaceKeepsTheReplacedRun(t *testing.T) {
	t.Parallel()

	bad := resultOf(1, 2, 3, 4, 7)
	bad.FirstError = "PUT did not answer 200: status 507"

	other := resultOf(1, 2, 3, 4, 0)
	other.Run = 2

	good := resultOf(5, 6, 7, 8, 0)

	pf := &protocolFile{
		Plan: protocolPlan{Ops: 1, Runs: 1, Concurrency: 1}, Host: readHost(), Families: nil,
		Enforced: map[string]bool{"s": true}, Results: []protoResult{bad, other}, Replaced: nil,
	}

	pf.replace(good)

	if len(pf.Results) != 2 || pf.Results[0].P50Ms != 5 || pf.Results[1].Run != 2 {
		t.Fatalf("results after replace = %+v", pf.Results)
	}

	if len(pf.Replaced) != 1 || !strings.Contains(pf.Replaced[0], "7 errors") ||
		!strings.Contains(pf.Replaced[0], "status 507") {
		t.Fatalf("replaced = %q", pf.Replaced)
	}
}

func TestVerdictInvalidWithErrors(t *testing.T) {
	t.Parallel()

	ok := summarize([]protoResult{resultOf(10, 20, 100, 1, 0)})
	bad := summarize([]protoResult{resultOf(10, 20, 100, 1, 3)})

	if got := verdict(ok, bad); got != "invalid: a run has errors" {
		t.Fatalf("verdict with errors = %q", got)
	}
}

func TestSelectsFamily(t *testing.T) {
	t.Parallel()

	all, err := parseOptions("protocol", nil)
	if err != nil || !all.selectsFamily("Pulumi checkpoint") {
		t.Fatalf("an empty -families selects nothing (parse error %v)", err)
	}

	some, err := parseOptions("protocol", []string{"-families", "audit-checkpoint-check-read, job-log-chunk"})
	if err != nil {
		t.Fatalf("parse -families: %v", err)
	}

	if !some.selectsFamily("audit checkpoint check (read)") || !some.selectsFamily("job log chunk") ||
		some.selectsFamily("Pulumi checkpoint") {
		t.Fatal("-families selects the wrong families")
	}
}

func TestPaddedSortsInOrder(t *testing.T) {
	t.Parallel()

	if got := padded(42); got != "000042" {
		t.Fatalf("padded(42) = %q", got)
	}

	if padded(999) >= padded(1000) {
		t.Fatal("padded keys do not sort in the order of their numbers")
	}
}

func TestRenderNoVerdictForCandidates(t *testing.T) {
	t.Parallel()

	oldRun, newRun := resultOf(10, 20, 100, 1, 0), resultOf(10, 20, 100, 1, 0)
	newRun.Mode = modeNew

	pf := &protocolFile{
		Plan: protocolPlan{Ops: 10000, Runs: 5, Concurrency: 16}, Host: readHost(),
		Families: []familyInfo{{Name: "f", Bytes: 1, Read: false, Candidate: true, OldPath: "", NewPath: ""}},
		Enforced: map[string]bool{"s": true}, Results: []protoResult{oldRun, newRun}, Replaced: nil,
	}

	out := renderProtocol(pf)
	if !strings.Contains(out, "no verdict: a candidate, not code") || strings.Contains(out, "| meets |") {
		t.Fatalf("render of a candidate:\n%s", out)
	}
}
