package cost_test

import (
	"errors"
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
	}

	for _, tc := range cases {
		if got := tc.m.String(); got != tc.plain {
			t.Fatalf("String(%d) = %q, want %q", tc.m.Micros, got, tc.plain)
		}

		if got := tc.m.Delta(); got != tc.delta {
			t.Fatalf("Delta(%d) = %q, want %q", tc.m.Micros, got, tc.delta)
		}
	}

	if _, err := eur(1, 0).Add(cost.Money{Currency: "USD", Micros: 1}); !errors.Is(err, cost.ErrCurrencyMismatch) {
		t.Fatalf("Add of two currencies: err = %v, want ErrCurrencyMismatch", err)
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

	if got := e.Delta().Delta(); got != "+EUR 31.20" || e.Unpriced() != 1 {
		t.Fatalf("delta = %s with %d unpriced, want +EUR 31.20 with 1", got, e.Unpriced())
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

func budget() cost.Budget {
	return cost.Budget{
		ID: "bgt-1", Namespace: "shop", Collection: "", Name: "Shop", Amount: eur(4000, 0),
		Thresholds: cost.DefaultThresholds(), CreatedBy: "alice", Version: 1,
	}
}

func estimate(t *testing.T, delta cost.Money) cost.Estimate {
	t.Helper()

	r := []cost.ResourceCost{
		{
			Key:      "k",
			Type:     "t",
			Action:   cost.ActionCreate,
			Before:   cost.Zero(delta.Currency),
			After:    delta,
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
		ID: id, Subject: plan(), Unit: "opentofu:shop/web", Source: "api", Currency: currency,
		Before: cost.Zero(currency), After: cost.Zero(currency), Resources: resources, CreatedAt: time.Now(),
	}
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
	}{
		{"below every threshold", []cost.SpendLine{line(eur(1000, 0), cost.Zero("EUR"))}, nil, 25, cost.LevelOK, nil},
		{
			"the forecast counts",
			[]cost.SpendLine{line(eur(1000, 0), eur(3300, 0))},
			nil,
			82,
			cost.LevelWarning,
			[]int{80},
		},
		{
			"a pending change counts",
			[]cost.SpendLine{line(eur(3000, 0), cost.Zero("EUR"))},
			[]cost.Estimate{estimate(t, eur(1000, 0))},
			100,
			cost.LevelExceeded,
			[]int{80, 100},
		},
		{
			"a change that saves money lowers the projection",
			[]cost.SpendLine{line(eur(3300, 0), cost.Zero("EUR"))},
			[]cost.Estimate{estimate(t, eur(-200, 0))},
			77,
			cost.LevelOK,
			nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, err := cost.Evaluate(budget(), p, tc.lines, tc.pending, time.Now())
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}

			if st.Percent != tc.percent || st.Level != tc.level || !slices.Equal(st.Crossed, tc.crossed) {
				t.Fatalf(
					"Evaluate = %d%%, %s, crossed %v; want %d%%, %s, %v",
					st.Percent,
					st.Level,
					st.Crossed,
					tc.percent,
					tc.level,
					tc.crossed,
				)
			}
		})
	}

	usd := []cost.SpendLine{line(cost.Money{Currency: "USD", Micros: 1}, cost.Zero("USD"))}
	if _, err := cost.Evaluate(budget(), p, usd, nil, time.Now()); !errors.Is(err, cost.ErrCurrencyMismatch) {
		t.Fatalf("spend in USD: err = %v, want ErrCurrencyMismatch", err)
	}
}

func TestNewlyCrossedAndImpact(t *testing.T) {
	t.Parallel()

	p := cost.Period{Year: 2026, Month: time.October}
	prev := crossed(p, 80)
	cur := crossed(p, 80, 100)

	if got := cost.NewlyCrossed(&prev, cur); !slices.Equal(got, []int{100}) {
		t.Fatalf("NewlyCrossed = %v, want [100]", got)
	}

	next := crossed(cost.Period{Year: 2026, Month: time.November}, 80)
	if got := cost.NewlyCrossed(&cur, next); !slices.Equal(got, []int{80}) {
		t.Fatalf("NewlyCrossed in a new month = %v, want [80]", got)
	}

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
