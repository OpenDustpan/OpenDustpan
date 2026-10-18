package cost_test

import (
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/cost"
)

func eur(units, cents int64) cost.Money {
	return cost.Money{Currency: "EUR", Micros: units*cost.MicrosPerUnit + cents*cost.MicrosPerUnit/100}
}

func TestMoneyFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m            cost.Money
		plain, delta string
	}{
		{eur(31, 20), "EUR 31.20", "+EUR 31.20"},
		{cost.Money{Currency: "EUR", Micros: -4_049_999}, "EUR -4.05", "-EUR 4.05"},
		{cost.Money{Currency: "USD", Micros: 4_999}, "USD 0.00", "USD 0.00"},
		{cost.Money{Currency: "USD", Micros: 5_000}, "USD 0.01", "+USD 0.01"},
		{eur(1250, 0), "EUR 1250.00", "+EUR 1250.00"},
		{cost.Money{Currency: "USD", Micros: -4_999}, "USD 0.00", "USD 0.00"},
		{cost.Money{Currency: "JPY", Micros: 3100 * cost.MicrosPerUnit}, "JPY 3100", "+JPY 3100"},
		{cost.Money{Currency: "JPY", Micros: 499_999}, "JPY 0", "JPY 0"},
		{cost.Money{Currency: "JPY", Micros: -1_500_000}, "JPY -2", "-JPY 2"},
		{cost.Money{Currency: "BHD", Micros: 1_250_000}, "BHD 1.250", "+BHD 1.250"},
		{cost.Money{Currency: "BHD", Micros: 1_000_500}, "BHD 1.001", "+BHD 1.001"},
		{cost.Money{Currency: "KWD", Micros: -7}, "KWD 0.000", "KWD 0.000"},
		{cost.Money{Currency: "XYZ", Micros: 1_005_000}, "XYZ 1.01", "+XYZ 1.01"},
		{cost.Money{Currency: "USD", Micros: math.MaxInt64}, "USD 9223372036854.78", "+USD 9223372036854.78"},
		{cost.Money{Currency: "USD", Micros: math.MinInt64}, "USD -9223372036854.78", "-USD 9223372036854.78"},
	}

	for _, tc := range cases {
		if got := tc.m.String(); got != tc.plain {
			t.Fatalf("String(%s %d) = %q, want %q", tc.m.Currency, tc.m.Micros, got, tc.plain)
		}

		if got := tc.m.Delta(); got != tc.delta {
			t.Fatalf("Delta(%s %d) = %q, want %q", tc.m.Currency, tc.m.Micros, got, tc.delta)
		}
	}
}

func TestMinorUnits(t *testing.T) {
	t.Parallel()

	for currency, want := range map[string]int{"USD": 2, "EUR": 2, "JPY": 0, "KRW": 0, "BHD": 3, "XYZ": 2} {
		if got := cost.MinorUnits(currency); got != want {
			t.Fatalf("MinorUnits(%s) = %d, want %d", currency, got, want)
		}
	}
}

func TestMoneyArithmetic(t *testing.T) {
	t.Parallel()

	usd := func(m int64) cost.Money { return cost.Money{Currency: "USD", Micros: m} }
	none := cost.Money{Currency: "", Micros: 0}
	cases := []struct {
		name    string
		op      func(a, b cost.Money) (cost.Money, error)
		a, b    cost.Money
		want    cost.Money
		wantErr error
	}{
		{"add", cost.Money.Add, usd(2), usd(3), usd(5), nil},
		{"add of two currencies", cost.Money.Add, usd(1), eur(1, 0), none, cost.ErrCurrencyMismatch},
		{"add above the int64 range", cost.Money.Add, usd(math.MaxInt64), usd(1), none, cost.ErrInvalid},
		{"add below the int64 range", cost.Money.Add, usd(math.MinInt64), usd(-1), none, cost.ErrInvalid},
		{"sub", cost.Money.Sub, usd(2), usd(3), usd(-1), nil},
		{"sub of two currencies", cost.Money.Sub, usd(1), eur(1, 0), none, cost.ErrCurrencyMismatch},
		{"sub above the int64 range", cost.Money.Sub, usd(math.MaxInt64), usd(-2), none, cost.ErrInvalid},
		{"sub below the int64 range", cost.Money.Sub, usd(math.MinInt64), usd(1), none, cost.ErrInvalid},
		{"sub of the int64 minimum", cost.Money.Sub, usd(0), usd(math.MinInt64), none, cost.ErrInvalid},
		{"sub of the int64 minimum from -1", cost.Money.Sub, usd(-1), usd(math.MinInt64), usd(math.MaxInt64), nil},
	}

	for _, tc := range cases {
		got, err := tc.op(tc.a, tc.b)
		if !errors.Is(err, tc.wantErr) || got != tc.want {
			t.Fatalf("%s = %+v (%v), want %+v (%v)", tc.name, got, err, tc.want, tc.wantErr)
		}
	}
}

func TestValidateCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		m  cost.Money
		ok bool
	}{
		{cost.Money{Currency: "EUR", Micros: 0}, true},
		{cost.Money{Currency: "EUR", Micros: cost.MaxMicros}, true},
		{cost.Money{Currency: "EUR", Micros: cost.MaxMicros + 1}, false},
		{cost.Money{Currency: "EUR", Micros: -1}, false},
		{cost.Money{Currency: "EUR", Micros: math.MinInt64}, false},
		{cost.Money{Currency: "eur", Micros: 1}, false},
	}

	for _, tc := range cases {
		err := tc.m.ValidateCost()
		if tc.ok != (err == nil) || (err != nil && !errors.Is(err, cost.ErrInvalid)) {
			t.Fatalf("ValidateCost(%s %d) = %v, want ok %t", tc.m.Currency, tc.m.Micros, err, tc.ok)
		}
	}
}

func plan() cost.Subject {
	return cost.Subject{Job: "job-1", Phase: 0, Blueprint: "", BlueprintVersion: ""}
}

func TestNewEstimate(t *testing.T) {
	t.Parallel()

	resources := []cost.ResourceCost{
		{
			Key:      "aws_instance.web",
			Type:     "aws_instance",
			Action:   cost.ActionCreate,
			Before:   eur(0, 0),
			After:    eur(25, 0),
			Unpriced: false,
			Note:     "",
		},
		{
			Key:      "aws_ebs_volume.data",
			Type:     "aws_ebs_volume",
			Action:   cost.ActionUpdate,
			Before:   eur(4, 0),
			After:    eur(10, 20),
			Unpriced: false,
			Note:     "",
		},
		{
			Key:      "aws_lambda_function.f",
			Type:     "aws_lambda_function",
			Action:   cost.ActionCreate,
			Before:   cost.Zero("EUR"),
			After:    cost.Zero("EUR"),
			Unpriced: true,
			Note:     "depends on usage",
		},
	}

	e, err := cost.Summed(unsummed("est-1", "EUR", resources))
	if err != nil {
		t.Fatalf("Summed: %v", err)
	}

	d, err := e.Delta()
	if err != nil || d.Delta() != "+EUR 31.20" || e.Unpriced() != 1 {
		t.Fatalf("delta = %s (%v) with %d unpriced, want +EUR 31.20 with 1", d.Delta(), err, e.Unpriced())
	}

	bad := slices.Clone(resources)
	bad[0].After = cost.Money{Currency: "USD", Micros: 1}

	_, err = cost.Summed(unsummed("est-2", "EUR", bad))
	if !errors.Is(err, cost.ErrCurrencyMismatch) {
		t.Fatalf("mixed currency: err = %v, want ErrCurrencyMismatch", err)
	}

	e.After = eur(1, 0)
	if err := e.Validate(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("sums that do not match: err = %v, want ErrInvalid", err)
	}

	both := cost.Subject{Job: "job-1", Phase: 0, Blueprint: "vpc", BlueprintVersion: "1"}
	if err := both.Validate(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("a subject with a job and a blueprint: err = %v, want ErrInvalid", err)
	}
}

func TestEstimateAmounts(t *testing.T) {
	t.Parallel()

	one := func(before, after int64) []cost.ResourceCost {
		return []cost.ResourceCost{{
			Key: "k", Type: "t", Action: cost.ActionUpdate,
			Before: cost.Money{Currency: "EUR", Micros: before}, After: cost.Money{Currency: "EUR", Micros: after},
			Unpriced: false, Note: "",
		}}
	}
	big := func(n int) []cost.ResourceCost {
		r := make([]cost.ResourceCost, 0, n)
		for range n {
			r = append(r, one(0, cost.MaxMicros)...)
		}

		return r
	}

	cases := []struct {
		name      string
		resources []cost.ResourceCost
		wantErr   error
	}{
		{"amounts at the cap", one(cost.MaxMicros, cost.MaxMicros), nil},
		{"a negative amount", one(0, -1), cost.ErrInvalid},
		{"an amount above the cap", one(0, cost.MaxMicros+1), cost.ErrInvalid},
		{"the int64 maximum and a second amount", append(one(0, math.MaxInt64), one(0, 2)...), cost.ErrInvalid},
		{"sums at the cap", append(one(0, cost.MaxMicros/2), one(0, cost.MaxMicros/2)...), nil},
		{"a sum above the cap", big(2), cost.ErrInvalid},
		{"the most resources at the cap", big(cost.MaxResources), cost.ErrInvalid},
	}

	for _, tc := range cases {
		_, err := cost.Summed(unsummed("est-1", "EUR", tc.resources))
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
	}

	// An estimate that bypasses Summed with sums that overflow fails
	// Validate, and Delta never wraps around.
	e := unsummed("est-1", "EUR", nil)
	e.After, e.Before = cost.Money{Currency: "EUR", Micros: math.MaxInt64}, cost.Money{Currency: "EUR", Micros: -2}

	if _, err := e.Delta(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("Delta of MaxInt64 and -2: err = %v, want ErrInvalid", err)
	}

	if err := e.Validate(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("Validate of sums that do not match: err = %v, want ErrInvalid", err)
	}

	e = unsummed("est-1", "EUR", nil)
	e.Namespace = ""

	if err := e.Validate(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("Validate with no namespace: err = %v, want ErrInvalid", err)
	}

	e.Namespace = "@server"
	if err := e.Validate(); !errors.Is(err, cost.ErrInvalid) {
		t.Fatalf("Validate of a plan in the server scope: err = %v, want ErrInvalid", err)
	}
}

func budget() cost.Budget {
	return cost.Budget{
		ID: "bgt-1", Namespace: "shop", Collection: "", Name: "Shop", Amount: eur(4000, 0),
		Thresholds: cost.DefaultThresholds(), CreatedBy: "alice", Version: 1,
	}
}

func estimate(t *testing.T, delta cost.Money) cost.Estimate {
	t.Helper()

	before, after := cost.Zero(delta.Currency), delta
	if delta.Micros < 0 {
		before, after = cost.Money{Currency: delta.Currency, Micros: -delta.Micros}, cost.Zero(delta.Currency)
	}

	r := []cost.ResourceCost{
		{
			Key:      "k",
			Type:     "t",
			Action:   cost.ActionUpdate,
			Before:   before,
			After:    after,
			Unpriced: false,
			Note:     "",
		},
	}

	e, err := cost.Summed(unsummed("est-1", delta.Currency, r))
	if err != nil {
		t.Fatalf("Summed: %v", err)
	}

	return e
}

// unsummed returns an estimate of the plan with the ID id, in currency,
// with no sums.
func unsummed(id, currency string, resources []cost.ResourceCost) cost.Estimate {
	return cost.Estimate{
		ID: id, Subject: plan(), Namespace: "shop", Unit: "opentofu:shop/web", Source: "api", Currency: currency,
		Before: cost.Zero(currency), After: cost.Zero(currency), Resources: resources, CreatedAt: time.Now(),
	}
}

// pushed returns e as an estimate that a client sent.
func pushed(e cost.Estimate) cost.Estimate {
	e.ID, e.Source = "est-2", cost.SourceAPI

	return e
}

// crossed returns a status of the budget bgt-1 in p that crossed the
// thresholds th.
func crossed(p cost.Period, th ...int) cost.Status {
	var st cost.Status

	st.Budget, st.Period, st.Crossed = "bgt-1", p, th

	return st
}

func TestEvaluate(t *testing.T) {
	t.Parallel()

	p := cost.Period{Year: 2026, Month: time.October}
	line := func(actual, forecast cost.Money) cost.SpendLine {
		return cost.SpendLine{Unit: "opentofu:shop/web", Actual: actual, Forecast: forecast, AsOf: time.Now()}
	}

	cases := []struct {
		name    string
		lines   []cost.SpendLine
		pending []cost.Estimate
		percent int64
		level   cost.Level
		crossed []int
		skipped int
		// negative is the part of skipped with a negative amount.
		negative int
	}{
		{
			"below every threshold",
			[]cost.SpendLine{line(eur(1000, 0), cost.Zero("EUR"))},
			nil,
			25,
			cost.LevelOK,
			nil,
			0,
			0,
		},
		{
			"the forecast counts",
			[]cost.SpendLine{line(eur(1000, 0), eur(3300, 0))},
			nil,
			82,
			cost.LevelWarning,
			[]int{80},
			0,
			0,
		},
		{
			"a pending change counts",
			[]cost.SpendLine{line(eur(3000, 0), cost.Zero("EUR"))},
			[]cost.Estimate{estimate(t, eur(1000, 0))},
			100,
			cost.LevelExceeded,
			[]int{80, 100},
			0,
			0,
		},
		{
			"a change that saves money lowers the projection",
			[]cost.SpendLine{line(eur(3300, 0), cost.Zero("EUR"))},
			[]cost.Estimate{estimate(t, eur(-200, 0))},
			77,
			cost.LevelOK,
			nil,
			0,
			0,
		},
		{
			"spend in another currency is skipped",
			[]cost.SpendLine{
				line(eur(3300, 0), cost.Zero("EUR")),
				line(cost.Money{Currency: "USD", Micros: 9 * cost.MicrosPerUnit}, cost.Zero("USD")),
			},
			nil,
			82,
			cost.LevelWarning,
			[]int{80},
			1,
			0,
		},
		{
			"an estimate in another currency is skipped and the warning stays",
			[]cost.SpendLine{line(eur(3300, 0), cost.Zero("EUR"))},
			[]cost.Estimate{estimate(t, cost.Money{Currency: "USD", Micros: 500 * cost.MicrosPerUnit})},
			82,
			cost.LevelWarning,
			[]int{80},
			1,
			0,
		},
		{
			"a negative spend is skipped with its own reason",
			[]cost.SpendLine{line(eur(3300, 0), cost.Zero("EUR")), line(eur(-3000, 0), cost.Zero("EUR"))},
			nil,
			82,
			cost.LevelWarning,
			[]int{80},
			1,
			1,
		},
		{
			"a negative forecast is skipped with its own reason",
			[]cost.SpendLine{line(eur(3300, 0), cost.Zero("EUR")), line(eur(1, 0), eur(-5, 0))},
			nil,
			82,
			cost.LevelWarning,
			[]int{80},
			1,
			1,
		},
		{
			"a change with two estimates counts once, with the larger delta",
			[]cost.SpendLine{line(eur(3000, 0), cost.Zero("EUR"))},
			[]cost.Estimate{pushed(estimate(t, eur(200, 0))), estimate(t, eur(400, 0))},
			85,
			cost.LevelWarning,
			[]int{80},
			0,
			0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, err := cost.Evaluate(budget(), p, tc.lines, tc.pending, time.Now())
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			if st.Percent != tc.percent || st.Level != tc.level || !slices.Equal(st.Crossed, tc.crossed) ||
				st.Skipped != tc.skipped || st.SkippedNegative != tc.negative {
				t.Fatalf(
					"Evaluate = %d%%, %s, crossed %v, skipped %d (%d negative); want %d%%, %s, %v, %d (%d)",
					st.Percent, st.Level, st.Crossed, st.Skipped, st.SkippedNegative,
					tc.percent, tc.level, tc.crossed, tc.skipped, tc.negative,
				)
			}
		})
	}
}

func TestEvaluateRefusesBadSpend(t *testing.T) {
	t.Parallel()

	p := cost.Period{Year: 2026, Month: time.October}
	cases := []struct {
		name  string
		lines []cost.SpendLine
	}{
		{
			"a spend above the cap",
			[]cost.SpendLine{{
				Unit: "u", Actual: cost.Money{Currency: "EUR", Micros: cost.MaxMicros + 1},
				Forecast: cost.Zero("EUR"), AsOf: time.Now(),
			}},
		},
	}

	for _, tc := range cases {
		if _, err := cost.Evaluate(budget(), p, tc.lines, nil, time.Now()); !errors.Is(err, cost.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", tc.name, err)
		}
	}
}

// TestPercentDoesNotWrap evaluates a projection whose percent does not fit
// an int64. The percent stays at its maximum and the budget is exceeded.
func TestPercentDoesNotWrap(t *testing.T) {
	t.Parallel()

	b := budget()
	b.Amount = cost.Money{Currency: "EUR", Micros: 1}

	lines := make([]cost.SpendLine, 100)
	for i := range lines {
		lines[i] = cost.SpendLine{
			Unit: "u", Actual: cost.Money{Currency: "EUR", Micros: cost.MaxMicros}, Forecast: cost.Zero("EUR"),
			AsOf: time.Now(),
		}
	}

	st, err := cost.Evaluate(b, cost.Period{Year: 2026, Month: time.October}, lines, nil, time.Now())
	if err != nil || st.Percent != math.MaxInt64 || st.Level != cost.LevelExceeded {
		t.Fatalf("Evaluate = %d%%, %s (%v); want %d%%, exceeded", st.Percent, st.Level, err, int64(math.MaxInt64))
	}
}

func TestThresholdKey(t *testing.T) {
	t.Parallel()

	oct := cost.Period{Year: 2026, Month: time.October}
	keys := map[string]bool{
		cost.ThresholdKey("bgt-1", oct, 80):                                           true,
		cost.ThresholdKey("bgt-1", oct, 100):                                          true,
		cost.ThresholdKey("bgt-1", cost.Period{Year: 2026, Month: time.November}, 80): true,
		cost.ThresholdKey("bgt-2", oct, 80):                                           true,
	}

	if len(keys) != 4 || cost.ThresholdKey("bgt-1", oct, 80) != "budget.threshold:bgt-1:2026-10:80" {
		t.Fatalf("ThresholdKey gives %d distinct keys, want 4: %v", len(keys), keys)
	}
}

func TestMarkWarned(t *testing.T) {
	t.Parallel()

	oct := cost.Period{Year: 2026, Month: time.October}
	nov := cost.Period{Year: 2026, Month: time.November}

	// Each step is the status of one evaluation, in order, with the
	// thresholds that warn and the thresholds warned after it.
	steps := []struct {
		name   string
		st     cost.Status
		warn   []int
		warned []int
	}{
		{"85 percent warns at 80", crossed(oct, 80), []int{80}, []int{80}},
		{"70 percent warns at nothing", crossed(oct), nil, []int{80}},
		{"85 percent again warns at nothing", crossed(oct, 80), nil, []int{80}},
		{"100 percent warns at 100", crossed(oct, 80, 100), []int{100}, []int{80, 100}},
		{"a new month warns again", crossed(nov, 80), []int{80}, []int{80}},
	}

	var prev *cost.Status

	for _, s := range steps {
		cur, warn := cost.MarkWarned(prev, s.st)
		if !slices.Equal(warn, s.warn) || !slices.Equal(cur.Warned, s.warned) {
			t.Fatalf("%s: warn %v, warned %v; want %v, %v", s.name, warn, cur.Warned, s.warn, s.warned)
		}

		prev = &cur
	}
}

func TestImpactOf(t *testing.T) {
	t.Parallel()

	p := cost.Period{Year: 2026, Month: time.October}

	st, err := cost.Evaluate(
		budget(),
		p,
		[]cost.SpendLine{{Unit: "", Actual: eur(3000, 0), Forecast: cost.Zero("EUR"), AsOf: time.Now()}},
		nil,
		time.Now(),
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}

	im, err := cost.ImpactOf(budget(), st, estimate(t, eur(680, 0)))
	if err != nil || im.Before != 75 || im.After != 92 || im.Level != cost.LevelWarning {
		t.Fatalf("ImpactOf = %+v (%v), want 75 to 92, warning", im, err)
	}
}

func TestBudgetValidate(t *testing.T) {
	t.Parallel()

	bad := map[string]func(b *cost.Budget){
		"no amount":                func(b *cost.Budget) { b.Amount.Micros = 0 },
		"a bad currency":           func(b *cost.Budget) { b.Amount.Currency = "euro" },
		"thresholds that decrease": func(b *cost.Budget) { b.Thresholds = []int{100, 80} },
		"no threshold":             func(b *cost.Budget) { b.Thresholds = nil },
		"a threshold of 0":         func(b *cost.Budget) { b.Thresholds = []int{0} },
	}

	for name, change := range bad {
		b := budget()
		change(&b)

		if err := b.Validate(); !errors.Is(err, cost.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}
