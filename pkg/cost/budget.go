package cost

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// Bounds of a budget.
const (
	// BudgetIDPrefix starts every budget ID.
	BudgetIDPrefix = "bgt-"
	// MaxThresholds caps the thresholds of a budget.
	MaxThresholds = 5
	// MaxThreshold caps a threshold, in percent of the amount.
	MaxThreshold = 1000
)

// DefaultThresholds are the thresholds of a budget that names none, in
// percent of the amount.
func DefaultThresholds() []int { return []int{80, 100} }

// Budget is a limit of spend for each month for a namespace, or for one
// collection of it. Thresholds are in percent of Amount, in increasing
// order. A crossed threshold gives a warning: an event and a mark in the
// console. A budget never blocks a change. A policy can read the cost of
// a change and the budget, and block it.
type Budget struct {
	ID         string
	Namespace  namespace.Name
	Collection string
	Name       string
	Amount     Money
	Thresholds []int
	CreatedBy  string
	Version    int64
}

// Validate returns ErrInvalid for a budget that breaks a bound.
func (b Budget) Validate() error {
	switch {
	case !strings.HasPrefix(b.ID, BudgetIDPrefix):
		return fmt.Errorf("%w: a budget ID starts with %q", ErrInvalid, BudgetIDPrefix)
	case b.Namespace == "":
		return fmt.Errorf("%w: a budget needs a namespace", ErrInvalid)
	case b.Name == "":
		return fmt.Errorf("%w: a budget needs a name", ErrInvalid)
	case !ValidCurrency(b.Amount.Currency) || b.Amount.Micros <= 0:
		return fmt.Errorf("%w: a budget needs an amount above 0 in an ISO 4217 currency", ErrInvalid)
	case len(b.Thresholds) == 0 || len(b.Thresholds) > MaxThresholds:
		return fmt.Errorf("%w: a budget has 1 to %d thresholds", ErrInvalid, MaxThresholds)
	}

	return b.validateThresholds()
}

// validateThresholds returns ErrInvalid for thresholds that do not
// increase, or that are outside 1 to MaxThreshold percent.
func (b Budget) validateThresholds() error {
	for i, th := range b.Thresholds {
		if th <= 0 || th > MaxThreshold || (i > 0 && th <= b.Thresholds[i-1]) {
			return fmt.Errorf("%w: thresholds increase, from 1 to %d percent", ErrInvalid, MaxThreshold)
		}
	}

	return nil
}

// Level is how a budget stands.
type Level string

// Levels of a status.
const (
	// LevelOK is below every threshold.
	LevelOK Level = "ok"
	// LevelWarning is at or above a threshold, and below 100 percent.
	LevelWarning Level = "warning"
	// LevelExceeded is at or above 100 percent of the amount.
	LevelExceeded Level = "exceeded"
)

// Status is a budget in a month. Actual is the spend so far. Forecast is
// the larger of Actual and the forecasts of the spend tool. Pending is the
// sum of the deltas of the plans that wait or run. Projected is Forecast
// plus Pending, and Percent is Projected in percent of the amount, rounded
// down. A pending delta is the cost of a whole month, so Projected can be
// above what the month costs: a warning comes early, never late.
type Status struct {
	Budget    string
	Period    Period
	Actual    Money
	Forecast  Money
	Pending   Money
	Projected Money
	Percent   int64
	Crossed   []int
	Level     Level
	AsOf      time.Time
}

// Evaluate returns the status of b in p from the spend lines and the
// pending estimates. It returns ErrCurrencyMismatch for a line or an
// estimate in another currency than the budget, and ErrInvalid for a
// budget that fails Validate.
func Evaluate(b Budget, p Period, lines []SpendLine, pending []Estimate, asOf time.Time) (Status, error) {
	if err := b.Validate(); err != nil {
		return Status{}, err
	}

	cur := b.Amount.Currency
	actual, forecast, delta := Zero(cur), Zero(cur), Zero(cur)

	for _, l := range lines {
		var err error
		if actual, err = actual.Add(l.Actual); err != nil {
			return Status{}, err
		}

		if l.Forecast.Micros == 0 {
			l.Forecast = l.Actual
		}

		if forecast, err = forecast.Add(l.Forecast); err != nil {
			return Status{}, err
		}
	}

	forecast.Micros = max(forecast.Micros, actual.Micros)

	for i := range pending {
		var err error
		if delta, err = delta.Add(pending[i].Delta()); err != nil {
			return Status{}, err
		}
	}

	projected, err := forecast.Add(delta)
	if err != nil {
		return Status{}, err
	}

	st := Status{
		Budget: b.ID, Period: p, Actual: actual, Forecast: forecast, Pending: delta, Projected: projected,
		Percent: percent(projected, b.Amount), Crossed: nil, Level: LevelOK, AsOf: asOf,
	}
	st.Crossed, st.Level = b.levelAt(st.Percent)

	return st, nil
}

// NewlyCrossed returns the thresholds that cur crossed and prev did not,
// in one period. The server sends one event for each, so a threshold
// warns once a month. A status of a new period has no previous one.
func NewlyCrossed(prev *Status, cur Status) []int {
	if prev == nil || prev.Period != cur.Period {
		return slices.Clone(cur.Crossed)
	}

	var out []int

	for _, th := range cur.Crossed {
		if !slices.Contains(prev.Crossed, th) {
			out = append(out, th)
		}
	}

	return out
}

// Impact is what one change does to a budget: the percent before and
// after the delta of its estimate, and the level after.
type Impact struct {
	Before int64
	After  int64
	Level  Level
}

// ImpactOf returns the impact of e on b, from the status st that does not
// count e. The work order shows it next to the delta. It returns
// ErrCurrencyMismatch for an estimate in another currency.
func ImpactOf(b Budget, st Status, e Estimate) (Impact, error) {
	after, err := st.Projected.Add(e.Delta())
	if err != nil {
		return Impact{}, err
	}

	p := percent(after, b.Amount)
	_, level := b.levelAt(p)

	return Impact{Before: st.Percent, After: p, Level: level}, nil
}

func (b Budget) levelAt(p int64) ([]int, Level) {
	const full = 100

	var crossed []int

	for _, th := range b.Thresholds {
		if p >= int64(th) {
			crossed = append(crossed, th)
		}
	}

	switch {
	case p >= full:
		return crossed, LevelExceeded
	case len(crossed) > 0:
		return crossed, LevelWarning
	default:
		return crossed, LevelOK
	}
}

// percent returns v in percent of total, rounded down, and 0 for a
// negative v.
func percent(v, total Money) int64 {
	if v.Micros <= 0 || total.Micros <= 0 {
		return 0
	}

	n := new(big.Int).Mul(big.NewInt(v.Micros), big.NewInt(100)) //nolint:mnd // percent
	n.Quo(n, big.NewInt(total.Micros))

	return n.Int64()
}
