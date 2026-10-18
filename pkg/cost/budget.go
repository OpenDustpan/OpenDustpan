package cost

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
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
// order. A crossed threshold gives a warning: a notification and a mark in
// the console. A budget never blocks a change. A policy can read the cost
// of a change and the budget, and block it.
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
	case b.Namespace == "" || b.Namespace == audit.ServerScope:
		return fmt.Errorf("%w: a budget needs a namespace", ErrInvalid)
	case b.Name == "":
		return fmt.Errorf("%w: a budget needs a name", ErrInvalid)
	case b.Amount.ValidateCost() != nil || b.Amount.Micros == 0:
		return fmt.Errorf(
			"%w: a budget needs an amount from 1 to %d micros in an ISO 4217 currency", ErrInvalid, int64(MaxMicros),
		)
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
//
// Skipped is the count of spend lines and pending estimates in another
// currency than the budget, which the status does not count. Warned holds
// the thresholds that sent a warning in the period, in increasing order.
// The store keeps it with the status, so that each threshold warns once a
// month also when the spend goes down and up again.
type Status struct {
	Budget    string
	Period    Period
	Actual    Money
	Forecast  Money
	Pending   Money
	Projected Money
	Percent   int64
	Crossed   []int
	Warned    []int
	Skipped   int
	Level     Level
	AsOf      time.Time
}

// Evaluate returns the status of b in p from the spend lines and the
// pending estimates. A line or an estimate in another currency than the
// budget does not count, and Status.Skipped counts it. Warned is empty:
// MarkWarned sets it. It returns ErrInvalid for a budget that fails
// Validate, for a spend amount below 0 or above MaxMicros, and for a sum
// that does not fit.
func Evaluate(b Budget, p Period, lines []SpendLine, pending []Estimate, asOf time.Time) (Status, error) {
	if err := b.Validate(); err != nil {
		return Status{}, err
	}

	cur := b.Amount.Currency
	st := Status{
		Budget: b.ID, Period: p, Actual: Zero(cur), Forecast: Zero(cur), Pending: Zero(cur), Projected: Zero(cur),
		Percent: 0, Crossed: nil, Warned: nil, Skipped: 0, Level: LevelOK, AsOf: asOf,
	}

	if err := st.addLines(cur, lines); err != nil {
		return Status{}, err
	}

	if err := st.addPending(cur, pending); err != nil {
		return Status{}, err
	}

	projected, err := st.Forecast.Add(st.Pending)
	if err != nil {
		return Status{}, err
	}

	st.Projected, st.Percent = projected, percent(projected, b.Amount)
	st.Crossed, st.Level = b.levelAt(st.Percent)

	return st, nil
}

// addLines adds the spend lines in cur to Actual and Forecast, and counts
// the other lines in Skipped. A line with no forecast counts its actual
// spend as its forecast.
func (st *Status) addLines(cur string, lines []SpendLine) error {
	for i := range lines {
		l := &lines[i]
		if l.Actual.Currency != cur || (l.Forecast.Micros != 0 && l.Forecast.Currency != cur) {
			st.Skipped++

			continue
		}

		forecast := l.Forecast
		if forecast.Micros == 0 {
			forecast = l.Actual
		}

		if err := l.Actual.ValidateCost(); err != nil {
			return fmt.Errorf("spend of %q: %w", l.Unit, err)
		}

		if err := forecast.ValidateCost(); err != nil {
			return fmt.Errorf("forecast of %q: %w", l.Unit, err)
		}

		var err error
		if st.Actual, err = st.Actual.Add(l.Actual); err != nil {
			return err
		}

		if st.Forecast, err = st.Forecast.Add(forecast); err != nil {
			return err
		}
	}

	st.Forecast.Micros = max(st.Forecast.Micros, st.Actual.Micros)

	return nil
}

// addPending adds the deltas of the estimates in cur to Pending, and
// counts the other estimates in Skipped.
func (st *Status) addPending(cur string, pending []Estimate) error {
	for i := range pending {
		if pending[i].Currency != cur {
			st.Skipped++

			continue
		}

		d, err := pending[i].Delta()
		if err != nil {
			return fmt.Errorf("estimate %s: %w", pending[i].ID, err)
		}

		if st.Pending, err = st.Pending.Add(d); err != nil {
			return err
		}
	}

	return nil
}

// MarkWarned returns cur with Warned set, and the thresholds that warn
// now: those that cur crossed and that no status of the same period
// warned about before. prev is the last stored status of the budget, or
// nil. The server sends one notification for each threshold that warns,
// and saves the result, so a threshold warns once a month: a projection
// that goes from 85 to 70 and to 85 percent again warns at 80 once. A
// status of a new period starts with no warned threshold.
func MarkWarned(prev *Status, cur Status) (Status, []int) {
	var warned []int
	if prev != nil && prev.Period == cur.Period && prev.Budget == cur.Budget {
		warned = slices.Clone(prev.Warned)
	}

	var now []int

	for _, th := range cur.Crossed {
		if !slices.Contains(warned, th) {
			now = append(now, th)
			warned = append(warned, th)
		}
	}

	slices.Sort(warned)
	cur.Warned = warned

	return cur, now
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
// ErrCurrencyMismatch for an estimate in another currency, and ErrInvalid
// for a sum that does not fit.
func ImpactOf(b Budget, st Status, e Estimate) (Impact, error) {
	d, err := e.Delta()
	if err != nil {
		return Impact{}, err
	}

	after, err := st.Projected.Add(d)
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
