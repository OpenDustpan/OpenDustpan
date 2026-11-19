// Package compare checks the measurements of the nightly performance job
// against the budgets kept in the repository, and keeps one issue open for each
// measurement that is over its budget.
//
// Every input is untrusted: the budgets file, the measurements and the
// reports of the harnesses. The package rejects an input that is not valid and
// never changes it to make it valid.
package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
)

// maxInput is the size limit of each input file.
const maxInput = 16 << 20

// ErrNoMeasurement means that a run produced no measurement. The job fails and
// no regression issue is filed.
var ErrNoMeasurement = errors.New("no measurement")

// Errors for an input that is not valid.
var (
	// ErrInvalidName means that a measurement or budget name is not on the allowlist.
	ErrInvalidName = errors.New("name is not valid")
	// ErrInvalidFigure means that a figure is negative, NaN or infinite.
	ErrInvalidFigure = errors.New("figure is negative or not finite")
	// ErrDuplicate means that a measurement name appears twice.
	ErrDuplicate = errors.New("measurement appears twice")
	// ErrTrailingData means that a JSON input holds data after its value.
	ErrTrailingData = errors.New("data after the JSON value")
)

// IssuePrefix starts the exact title of each regression issue.
const IssuePrefix = "[nightly-perf] "

// nameRE is the allowlist for a measurement name. A name goes into an issue
// title and a command argument, so it holds no space, no control character and
// no shell character.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Stats are the three figures of one measurement. Budget and Measurement share
// these fields.
type Stats struct {
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
	Unit  string  `json:"unit"`
	Scope string  `json:"scope"`
}

// Budget is the upper limit of one measurement.
type Budget = Stats

// Budgets maps a measurement name to its budget.
type Budgets map[string]Budget

// Measurement is one figure set that a harness produced.
type Measurement struct {
	Name  string  `json:"name"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
	Unit  string  `json:"unit"`
	Scope string  `json:"scope"`
}

// Breach is one field of one measurement that is above its budget.
type Breach struct {
	Field    string
	Measured float64
	Budget   float64
	Unit     string
	Scope    string
}

// Result is the outcome of one comparison.
type Result struct {
	// Breaches holds, for each measurement over budget, its breached fields.
	Breaches map[string][]Breach
	// Checked lists the measurements that have a budget, sorted.
	Checked []string
	// RecordOnly lists the measurements with no budget, sorted.
	RecordOnly []string
	// Stale lists the budget names that the run did not produce, sorted.
	Stale []string
}

func validFigure(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

// ParseBudgets reads the budgets file. It rejects unknown fields, trailing
// data, a bad name and a figure that is negative, NaN or infinite.
func ParseBudgets(r io.Reader) (Budgets, error) {
	var budgets Budgets
	if err := decodeStrict(r, &budgets); err != nil {
		return nil, fmt.Errorf("read budgets: %w", err)
	}
	for name, b := range budgets {
		if err := checkEntry(name, b.P50, b.P95, b.Max); err != nil {
			return nil, fmt.Errorf("budget: %w", err)
		}
	}
	return budgets, nil
}

// ParseMeasurements reads the list of measurements of one job.
func ParseMeasurements(r io.Reader) ([]Measurement, error) {
	var ms []Measurement
	if err := decodeStrict(r, &ms); err != nil {
		return nil, fmt.Errorf("read measurements: %w", err)
	}
	return ms, nil
}

func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(io.LimitReader(r, maxInput))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return ErrTrailingData
	}
	return nil
}

// checkEntry checks the name and the three figures of one entry.
func checkEntry(name string, figures ...float64) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("%q: %w", name, ErrInvalidName)
	}
	for _, v := range figures {
		if !validFigure(v) {
			return fmt.Errorf("%q: %w", name, ErrInvalidFigure)
		}
	}
	return nil
}

// Evaluate compares each measurement with its budget. A figure above its
// budget field is a breach, with zero tolerance. A measurement with no budget
// is record-only. A budget with no measurement is stale. Each measurement is
// judged alone.
func Evaluate(budgets Budgets, ms []Measurement) (Result, error) {
	if len(ms) == 0 {
		return Result{}, ErrNoMeasurement
	}
	// Defense in depth: ParseBudgets checks the names too; this check stays so
	// that a caller that builds Budgets in code cannot skip it.
	for name := range budgets {
		if err := checkEntry(name); err != nil {
			return Result{}, fmt.Errorf("budget: %w", err)
		}
	}
	res := Result{Breaches: map[string][]Breach{}, Checked: nil, RecordOnly: nil, Stale: nil}
	seen := map[string]bool{}
	for _, m := range ms {
		if err := checkEntry(m.Name, m.P50, m.P95, m.Max); err != nil {
			return Result{}, fmt.Errorf("measurement: %w", err)
		}
		if seen[m.Name] {
			return Result{}, fmt.Errorf("%q: %w", m.Name, ErrDuplicate)
		}
		seen[m.Name] = true
		b, ok := budgets[m.Name]
		if !ok {
			res.RecordOnly = append(res.RecordOnly, m.Name)
			continue
		}
		res.Checked = append(res.Checked, m.Name)
		if breaches := breachesOf(m, b); len(breaches) > 0 {
			res.Breaches[m.Name] = breaches
		}
	}
	res.Stale = staleBudgets(budgets, seen)
	slices.Sort(res.Checked)
	slices.Sort(res.RecordOnly)
	return res, nil
}

// staleBudgets returns the sorted budget names that no measurement used.
func staleBudgets(budgets Budgets, seen map[string]bool) []string {
	var stale []string
	for name := range budgets {
		if !seen[name] {
			stale = append(stale, name)
		}
	}
	slices.Sort(stale)
	return stale
}

// breachesOf returns the fields of m that are above the budget b.
func breachesOf(m Measurement, b Budget) []Breach {
	var out []Breach
	pairs := []struct {
		field    string
		measured float64
		budget   float64
	}{{"p50", m.P50, b.P50}, {"p95", m.P95, b.P95}, {"max", m.Max, b.Max}}
	for _, p := range pairs {
		if p.measured > p.budget {
			out = append(
				out,
				Breach{Field: p.field, Measured: p.measured, Budget: p.budget, Unit: b.Unit, Scope: b.Scope},
			)
		}
	}
	return out
}

// Table renders the breaches of one run as one Markdown table. It names the
// run and the command that shows it.
func Table(res Result, runID string) string {
	var b strings.Builder
	if len(res.Breaches) == 0 {
		b.WriteString("No measurement is over its budget.\n")
		return b.String()
	}
	names := make([]string, 0, len(res.Breaches))
	for n := range res.Breaches {
		names = append(names, n)
	}
	slices.Sort(names)
	b.WriteString(
		"| Measurement | Figure | Measured | Budget | Unit | Scope | Run |\n| --- | --- | --- | --- | --- | --- | --- |\n",
	)
	for _, n := range names {
		for _, br := range res.Breaches[n] {
			fmt.Fprintf(
				&b,
				"| %s | %s | %g | %g | %s | %s | %s |\n",
				n,
				br.Field,
				br.Measured,
				br.Budget,
				br.Unit,
				br.Scope,
				cell(runID),
			)
		}
	}
	fmt.Fprintf(&b, "\nShow the run: `gh run view %s`\n", cell(runID))
	return b.String()
}

// cell removes characters that would break a table cell or a command line.
func cell(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return -1
	}, s)
}

// Issues is the port to the issue tracker. Each method returns an error when
// the tracker call fails.
type Issues interface {
	// Find returns the number of the open issue with exactly this title.
	Find(ctx context.Context, title string) (number int, found bool, err error)
	// Open creates an issue.
	Open(ctx context.Context, title, body string) error
	// Comment adds a comment to an issue.
	Comment(ctx context.Context, number int, body string) error
	// Close closes an issue with a comment.
	Close(ctx context.Context, number int, body string) error
}

// Sync opens, updates or closes one issue for each measurement that has a
// budget. A new breach opens an issue, a repeat breach adds a comment and a
// recovery closes the issue. A record-only measurement and a stale budget
// touch no issue. The first failed call ends Sync with an error (fail closed).
func Sync(ctx context.Context, issues Issues, res Result, runID string) error {
	for _, name := range res.Checked {
		title := IssuePrefix + name
		number, found, err := issues.Find(ctx, title)
		if err != nil {
			return fmt.Errorf("find issue %q: %w", title, err)
		}
		breaches := res.Breaches[name]
		switch {
		case len(breaches) > 0 && !found:
			if err := issues.Open(ctx, title, issueBody(name, breaches, runID)); err != nil {
				return fmt.Errorf("open issue %q: %w", title, err)
			}
		case len(breaches) > 0:
			if err := issues.Comment(ctx, number, issueBody(name, breaches, runID)); err != nil {
				return fmt.Errorf("comment on issue %q: %w", title, err)
			}
		case found:
			body := fmt.Sprintf("Recovered: %s is within its budget in run %s.", name, cell(runID))
			if err := issues.Close(ctx, number, body); err != nil {
				return fmt.Errorf("close issue %q: %w", title, err)
			}
		default:
			// Within budget and no open issue: nothing to do.
		}
	}
	return nil
}

func issueBody(name string, breaches []Breach, runID string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Breach of %s in run %s.\n\n", name, cell(runID))
	b.WriteString("| Figure | Measured | Budget | Unit | Scope |\n| --- | --- | --- | --- | --- |\n")
	for _, br := range breaches {
		fmt.Fprintf(&b, "| %s | %g | %g | %s | %s |\n", br.Field, br.Measured, br.Budget, br.Unit, br.Scope)
	}
	fmt.Fprintf(&b, "\nShow the run: `gh run view %s`\n", cell(runID))
	return b.String()
}
