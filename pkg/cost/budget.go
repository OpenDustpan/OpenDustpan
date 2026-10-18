package cost

import (
	"fmt"
	"math"
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
// Skipped is the count of spend lines and pending changes that the status
// does not count: a line or a change in another currency than the budget,
// and a line with a negative spend or forecast, for example a credit.
// SkippedNegative is the count of the negative lines in Skipped, so that a
// reader can name each reason. Warned holds
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
	// SkippedNegative is the part of Skipped with a negative amount.
	SkippedNegative int
	Level           Level
	AsOf            time.Time
}

// Evaluate returns the status of b in p from the spend lines and the
// pending estimates. A line or an estimate in another currency than the
// budget does not count, and Status.Skipped counts it. A line with a
// negative spend or forecast does not count, and Skipped and
// SkippedNegative count it. A subject with two estimates, one of each
// origin, counts once, with the larger delta. Warned is empty: MarkWarned
// sets it. It returns ErrInvalid for a budget that fails Validate, for a
// spend amount above MaxMicros, and for a sum that does not fit.
func Evaluate(b Budget, p Period, lines []SpendLine, pending []Estimate, asOf time.Time) (Status, error) {
	if err := b.Validate(); err != nil {
		return Status{}, err
	}

	cur := b.Amount.Currency
	st := Status{
		Budget: b.ID, Period: p, Actual: Zero(cur), Forecast: Zero(cur), Pending: Zero(cur), Projected: Zero(cur),
		Percent: 0, Crossed: nil, Warned: nil, Skipped: 0, SkippedNegative: 0, Level: LevelOK, AsOf: asOf,
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
// spend as its forecast. A negative line counts in Skipped and in
// SkippedNegative.
func (st *Status) addLines(cur string, lines []SpendLine) error {
	for i := range lines {
		l := &lines[i]
		if st.skip(cur, l) {
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

// skip reports whether the line l does not count, and counts it in
// Skipped: a line in another currency than cur, or a negative line, which
// SkippedNegative also counts.
func (st *Status) skip(cur string, l *SpendLine) bool {
	switch {
	case l.Actual.Currency != cur || (l.Forecast.Micros != 0 && l.Forecast.Currency != cur):
		st.Skipped++

		return true
	case l.Actual.Micros < 0 || l.Forecast.Micros < 0:
		st.Skipped++
		st.SkippedNegative++

		return true
	default:
		return false
	}
}

// addPending adds the deltas of the estimates in cur to Pending. A
// subject with two estimates in cur, one of each origin, adds the larger
// delta, so a warning comes early and a change counts once. A subject with
// no estimate in cur counts once in Skipped.
func (st *Status) addPending(cur string, pending []Estimate) error {
	l, err := largestDeltas(cur, pending)
	if err != nil {
		return err
	}

	st.Skipped += l.skipped

	for _, s := range l.order {
		if st.Pending, err = st.Pending.Add(l.deltas[s]); err != nil {
			return err
		}
	}

	return nil
}

// largest holds the subjects of the estimates in one currency in the
// order of the estimates, the larger delta of each, and the count of
// subjects with no estimate in that currency.
type largest struct {
	order   []Subject
	deltas  map[Subject]Money
	skipped int
}

// largestDeltas returns the largest deltas of the estimates in cur.
func largestDeltas(cur string, pending []Estimate) (largest, error) {
	deltas := map[Subject]Money{}
	other := map[Subject]bool{}

	var order []Subject

	for i := range pending {
		e := &pending[i]
		if e.Currency != cur {
			other[e.Subject] = true

			continue
		}

		d, err := e.Delta()
		if err != nil {
			return largest{}, fmt.Errorf("estimate %s: %w", e.ID, err)
		}

		old, ok := deltas[e.Subject]
		if !ok {
			order = append(order, e.Subject)
		}

		if !ok || d.Micros > old.Micros {
			deltas[e.Subject] = d
		}
	}

	skipped := 0

	for s := range other {
		if _, ok := deltas[s]; !ok {
			skipped++
		}
	}

	return largest{order: order, deltas: deltas, skipped: skipped}, nil
}

// MarkWarned returns cur with Warned set, and the thresholds that warn
// now: those that cur crossed and that no status of the same period
// warned about before. prev is the last stored status of the budget, or
// nil. A threshold warns once a month: a projection that goes from 85 to
// 70 and to 85 percent again warns at 80 once. A status of a new period
// starts with no warned threshold.
//
// The server first appends one notification for each threshold that
// warns, with the dedupe key ThresholdKey, and then saves the result with
// SaveStatus. When the save fails, for example with ErrConflict, the next
// evaluation warns at the same thresholds again, and the outbox skips the
// notification with the same key. So a crash between the two writes loses
// no warning and sends no second one.
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

// ThresholdKey returns the dedupe key of the notification of threshold th
// of the budget id in p. The key names the period, so it repeats only in
// one month.
func ThresholdKey(id string, p Period, th int) string {
	return fmt.Sprintf("budget.threshold:%s:%04d-%02d:%d", id, p.Year, int(p.Month), th)
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

// percent returns v in percent of total, rounded down, 0 for a negative
// v, and math.MaxInt64 for a percent that does not fit an int64.
func percent(v, total Money) int64 {
	if v.Micros <= 0 || total.Micros <= 0 {
		return 0
	}

	n := new(big.Int).Mul(big.NewInt(v.Micros), big.NewInt(100)) //nolint:mnd // percent
	n.Quo(n, big.NewInt(total.Micros))

	if !n.IsInt64() {
		return math.MaxInt64
	}

	return n.Int64()
}
