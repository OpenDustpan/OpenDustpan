package compare_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/perf/compare"
)

func m(name string, p50, p95, peak float64) compare.Measurement {
	return compare.Measurement{Name: name, P50: p50, P95: p95, Max: peak, Unit: "ms", Scope: "load"}
}

func evaluate(t *testing.T, budgets compare.Budgets, ms []compare.Measurement) compare.Result {
	t.Helper()
	res, err := compare.Evaluate(budgets, ms)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return res
}

func b(p50, p95, peak float64) compare.Budget {
	return compare.Budget{P50: p50, P95: p95, Max: peak, Unit: "ms", Scope: "load"}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	budgets := compare.Budgets{
		"a.within": b(10, 20, 30),
		"a.breach": b(10, 20, 30),
		"a.equal":  b(10, 20, 30),
		"a.stale":  b(1, 1, 1),
	}
	res, err := compare.Evaluate(budgets, []compare.Measurement{
		m("a.within", 5, 6, 7),
		m("a.breach", 11, 20, 31),
		m("a.equal", 10, 20, 30),
		m("a.new", 1, 2, 3),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if got := len(res.Breaches["a.breach"]); got != 2 {
		t.Fatalf("breach fields = %d, want 2 (p50 and max)", got)
	}
	if _, ok := res.Breaches["a.equal"]; ok {
		t.Fatal("a value equal to its budget must pass (zero tolerance means above only)")
	}
	if _, ok := res.Breaches["a.within"]; ok {
		t.Fatal("within budget must not breach")
	}
	if len(res.RecordOnly) != 1 || res.RecordOnly[0] != "a.new" {
		t.Fatalf("RecordOnly = %v, want [a.new]", res.RecordOnly)
	}
	if len(res.Stale) != 1 || res.Stale[0] != "a.stale" {
		t.Fatalf("Stale = %v, want [a.stale]", res.Stale)
	}
	if got := res.Checked; len(got) != 3 {
		t.Fatalf("Checked = %v, want 3 measurements with a budget", got)
	}
}

func TestEvaluateNoMeasurement(t *testing.T) {
	t.Parallel()
	_, err := compare.Evaluate(compare.Budgets{"a": b(1, 1, 1)}, nil)
	if !errors.Is(err, compare.ErrNoMeasurement) {
		t.Fatalf("err = %v, want ErrNoMeasurement", err)
	}
}

func TestEvaluateRejectsBadValues(t *testing.T) {
	t.Parallel()
	bad := []compare.Measurement{
		m("x", -1, 0, 0),
		m("x", 0, nan(), 0),
		m("x", 0, 0, inf()),
		m("bad name; rm", 1, 1, 1),
		m("", 1, 1, 1),
		m("x\ny", 1, 1, 1),
		m(strings.Repeat("a", 200), 1, 1, 1),
	}
	for _, ms := range bad {
		if _, err := compare.Evaluate(compare.Budgets{}, []compare.Measurement{ms}); err == nil {
			t.Errorf("Evaluate accepted %+v", ms)
		}
	}
	_, err := compare.Evaluate(compare.Budgets{}, []compare.Measurement{m("x", 1, 1, 1), m("x", 1, 1, 1)})
	if err == nil {
		t.Error("Evaluate accepted a duplicate name")
	}
	_, err = compare.Evaluate(compare.Budgets{"bad name": b(1, 1, 1)}, []compare.Measurement{m("x", 1, 1, 1)})
	if err == nil {
		t.Error("Evaluate accepted a bad budget name")
	}
}

func TestParseBudgetsStrict(t *testing.T) {
	t.Parallel()
	good := `{"a.b":{"p50":1,"p95":2,"max":3,"unit":"ms","scope":"load"}}`
	if _, err := compare.ParseBudgets(strings.NewReader(good)); err != nil {
		t.Fatalf("good budgets: %v", err)
	}
	for _, in := range []string{
		`{"a":{"p50":1,"p95":2,"max":3,"unit":"ms","scope":"load","extra":1}}`,
		`{"a":{"p50":-1,"p95":2,"max":3,"unit":"ms","scope":"load"}}`,
		`{"a":{"p50":1,"p95":2,"max":3,"unit":"ms","scope":"load"}} {}`,
		`[]`,
		``,
	} {
		if _, err := compare.ParseBudgets(strings.NewReader(in)); err == nil {
			t.Errorf("ParseBudgets accepted %q", in)
		}
	}
}

var errGh = errors.New("gh failed")

type fakeIssues struct {
	open    map[string]int
	calls   []string
	failOn  string
	nextNum int
}

func (f *fakeIssues) Find(_ context.Context, title string) (int, bool, error) {
	if err := f.step("find " + title); err != nil {
		return 0, false, err
	}
	n, ok := f.open[title]
	return n, ok, nil
}

func (f *fakeIssues) Open(_ context.Context, title, _ string) error {
	if err := f.step("open " + title); err != nil {
		return err
	}
	f.nextNum++
	f.open[title] = f.nextNum
	return nil
}

func (f *fakeIssues) Comment(_ context.Context, _ int, body string) error {
	return f.step("comment " + firstLine(body))
}

func (f *fakeIssues) Close(_ context.Context, n int, _ string) error {
	if err := f.step("close"); err != nil {
		return err
	}
	for k, v := range f.open {
		if v == n {
			delete(f.open, k)
		}
	}
	return nil
}

func (f *fakeIssues) step(call string) error {
	f.calls = append(f.calls, call)
	if f.failOn == call {
		return errGh
	}
	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

func TestSyncLifecycle(t *testing.T) {
	t.Parallel()
	budgets := compare.Budgets{"a.x": b(10, 10, 10)}
	issues := &fakeIssues{open: map[string]int{}, calls: nil, failOn: "", nextNum: 0}
	ctx := t.Context()

	breach := evaluate(t, budgets, []compare.Measurement{m("a.x", 99, 99, 99)})
	if err := compare.Sync(ctx, issues, breach, "run-1"); err != nil {
		t.Fatalf("first breach: %v", err)
	}
	if _, ok := issues.open["[nightly-perf] a.x"]; !ok {
		t.Fatalf("no issue with the exact title; calls = %v", issues.calls)
	}

	if err := compare.Sync(ctx, issues, breach, "run-2"); err != nil {
		t.Fatalf("repeat breach: %v", err)
	}
	opens := 0
	for _, c := range issues.calls {
		if strings.HasPrefix(c, "open ") {
			opens++
		}
	}
	if opens != 1 {
		t.Fatalf("opens = %d, want 1 (repeat breach comments)", opens)
	}
	if last := issues.calls[len(issues.calls)-1]; !strings.HasPrefix(last, "comment ") {
		t.Fatalf("last call = %q, want a comment", last)
	}

	ok := evaluate(t, budgets, []compare.Measurement{m("a.x", 1, 1, 1)})
	if err := compare.Sync(ctx, issues, ok, "run-3"); err != nil {
		t.Fatalf("recovery: %v", err)
	}
	if len(issues.open) != 0 {
		t.Fatalf("issue still open after recovery: %v", issues.open)
	}
}

func TestSyncRecordOnlyAndStaleTouchNothing(t *testing.T) {
	t.Parallel()
	issues := &fakeIssues{open: map[string]int{"[nightly-perf] a.new": 7}, calls: nil, failOn: "", nextNum: 0}
	res := evaluate(t, compare.Budgets{"a.stale": b(1, 1, 1)}, []compare.Measurement{m("a.new", 9e9, 9e9, 9e9)})
	if err := compare.Sync(t.Context(), issues, res, "r"); err != nil {
		t.Fatal(err)
	}
	if len(issues.calls) != 0 {
		t.Fatalf("calls = %v, want none", issues.calls)
	}
}

func TestSyncIssueCallFailureFailsClosed(t *testing.T) {
	t.Parallel()
	budgets := compare.Budgets{"a.x": b(10, 10, 10), "a.y": b(10, 10, 10)}
	res := evaluate(t, budgets, []compare.Measurement{m("a.x", 99, 99, 99), m("a.y", 99, 99, 99)})
	issues := &fakeIssues{open: map[string]int{}, calls: nil, failOn: "find [nightly-perf] a.x", nextNum: 0}
	if err := compare.Sync(t.Context(), issues, res, "r"); err == nil {
		t.Fatal("Sync hid a failed gh call")
	}
	for _, c := range issues.calls {
		if strings.HasPrefix(c, "open ") || c == "close" {
			t.Fatalf("Sync made %q after a failed call", c)
		}
	}
}

func TestReportTableNamesRunAndCommand(t *testing.T) {
	t.Parallel()
	res := evaluate(t, compare.Budgets{"a.x": b(10, 10, 10)}, []compare.Measurement{m("a.x", 99, 5, 5)})
	out := compare.Table(res, "4242")
	for _, want := range []string{"a.x", "99", "10", "4242", "gh run view 4242"} {
		if !strings.Contains(out, want) {
			t.Errorf("table lacks %q:\n%s", want, out)
		}
	}
}

func TestExtractLoad(t *testing.T) {
	t.Parallel()
	in := `{"checkpoints":[{"sizeBytes":1048999,"fullMs":12.5,"deltaMs":3}],
"imports":[{"sizeBytes":1048972,"importMs":40}],
"workspaceLocks":{"p50Ms":2},"updateConflicts":{"p50Ms":3},"namespaceUpdates":{"p50Ms":4},
"lists":{"workspaceListP50Ms":1,"workspaceListFullMs":9,"inventoryMs":8},
"journal":{"runs":[{"entriesPerBatch":20,"appendP50Ms":1,"appendMaxMs":5,"completeMs":7}]}}`
	ms, err := compare.ExtractLoad(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]compare.Measurement{}
	for _, x := range ms {
		got[x.Name] = x
	}
	for _, name := range []string{
		"load.checkpoint.full.1024k", "load.checkpoint.delta.1024k", "load.import.1024k",
		"load.workspace-lock.attempt", "load.update-conflict.start", "load.namespace-update.start",
		"load.list.workspace-page", "load.list.workspace-walk", "load.list.inventory",
		"load.journal.append.20", "load.journal.complete.20",
	} {
		if _, ok := got[name]; !ok {
			t.Errorf("missing %s; got %v", name, got)
		}
	}
	if got["load.journal.append.20"].Max != 5 || got["load.journal.append.20"].P50 != 1 {
		t.Errorf("append stats = %+v", got["load.journal.append.20"])
	}
	_, err = compare.ExtractLoad(
		strings.NewReader(`{"checkpoints":[{"sizeBytes":1,"fullMs":1,"deltaMs":1}],"bogus":1}`),
	)
	if err != nil {
		t.Errorf("unknown field must not fail: %v", err)
	}
	if _, err := compare.ExtractLoad(strings.NewReader(`{}`)); !errors.Is(err, compare.ErrNoMeasurement) {
		t.Errorf("empty report err = %v, want ErrNoMeasurement", err)
	}
}

func TestExtractSoak(t *testing.T) {
	t.Parallel()
	csv := "elapsed_s,goroutines,heap_bytes,fds,db_conns,runs,failures,error_rate\n" +
		"10,100,1000,10,,5,0,0\n20,110,2000,12,,6,1,0.1\n30,120,3000,14,,7,0,0\n"
	ms, err := compare.ExtractSoak(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]compare.Measurement{}
	for _, x := range ms {
		got[x.Name] = x
	}
	g := got["soak.goroutines"]
	if g.P50 != 110 || g.Max != 120 {
		t.Errorf("goroutines = %+v", g)
	}
	if got["soak.error-rate"].Max != 0.1 {
		t.Errorf("error rate = %+v", got["soak.error-rate"])
	}
	for _, in := range []string{"", "elapsed_s,goroutines\n", "elapsed_s,goroutines,heap_bytes,fds,db_conns,runs,failures,error_rate\n"} {
		_, err := compare.ExtractSoak(strings.NewReader(in))
		if err == nil {
			t.Errorf("ExtractSoak accepted %q", in)
		}
	}
}

func TestExtractFaults(t *testing.T) {
	t.Parallel()
	in := `{"Action":"pass","Test":"TestA","Elapsed":1.5}
{"Action":"pass","Test":"TestA/sub","Elapsed":0.5}
{"Action":"fail","Test":"TestB","Elapsed":2}
{"Action":"output","Test":"TestA","Output":"x"}
{"Action":"pass","Package":"p","Elapsed":9}
`
	ms, err := compare.ExtractFaults(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 || ms[0].Name != "faults.test.TestA" || ms[0].Max != 1.5 {
		t.Fatalf("ms = %+v, want only the passing top-level test", ms)
	}
}

func nan() float64 { var z float64; return z / z }
func inf() float64 { var z float64; return 1 / z }
