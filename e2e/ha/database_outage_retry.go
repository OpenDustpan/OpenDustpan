package ha

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

var (
	errUnknownVersion  = errors.New("write attempt names no CLI version of the matrix")
	errWriteNotRefused = errors.New("retry attempt did not answer 503")
	errNoAttemptTime   = errors.New("retry attempt has no time")
)

// WriteAttempt is one retry of one state version write during the outage.
// Round names the outage of the attempt: a retry window never spans two
// outages.
type WriteAttempt struct {
	Version string
	Method  string
	Path    string
	Status  int
	Round   int
	At      time.Time
}

// roundKey names the attempts of one CLI version in one outage.
type roundKey struct {
	version string
	round   int
}

// RetryReport holds the retry window of each CLI version of the matrix: the
// smallest window of the version over the outages that it retried in.
type RetryReport struct {
	order     []string
	attempted map[string]bool
	first     map[roundKey]time.Time
	last      map[roundKey]time.Time
}

// VersionLabel names one CLI version in the retry report.
func VersionLabel(tool, version string) string {
	return tool + " " + version
}

// NewRetryReport returns an empty report for the CLI matrix.
func NewRetryReport(matrix []CLIVersion) *RetryReport {
	report := &RetryReport{
		order:     make([]string, 0, len(matrix)),
		attempted: map[string]bool{},
		first:     map[roundKey]time.Time{},
		last:      map[roundKey]time.Time{},
	}

	for _, cli := range matrix {
		report.order = append(report.order, VersionLabel(cli.Tool, cli.Version))
	}

	return report
}

// Add records one refused write attempt of one CLI version.
func (r *RetryReport) Add(attempt WriteAttempt) error {
	if !slices.Contains(r.order, attempt.Version) {
		return fmt.Errorf("%w: %q", errUnknownVersion, attempt.Version)
	}

	if attempt.Status != http.StatusServiceUnavailable || attempt.Method == http.MethodGet {
		return fmt.Errorf("%w: %s %s answered %d", errWriteNotRefused,
			attempt.Method, attempt.Path, attempt.Status)
	}

	if attempt.At.IsZero() {
		return errNoAttemptTime
	}

	r.attempted[attempt.Version] = true

	key := roundKey{version: attempt.Version, round: attempt.Round}

	first, seen := r.first[key]
	if !seen || attempt.At.Before(first) {
		r.first[key] = attempt.At
	}

	last, seen := r.last[key]
	if !seen || attempt.At.After(last) {
		r.last[key] = attempt.At
	}

	return nil
}

// Shortest returns the smallest retry window of the versions that have a
// refused attempt. It returns zero when no version has one, and also when a
// version has only one attempt: that version has no measured window.
func (r *RetryReport) Shortest() time.Duration {
	shortest := time.Duration(0)
	found := false

	for _, label := range r.order {
		if !r.attempted[label] {
			continue
		}

		window := r.window(label)
		if !found || window < shortest {
			shortest = window
			found = true
		}
	}

	return shortest
}

// Unmeasured returns the label of each CLI version of the matrix that has no
// refused write attempt, in matrix order.
func (r *RetryReport) Unmeasured() []string {
	var labels []string

	for _, label := range r.order {
		if !r.attempted[label] {
			labels = append(labels, label)
		}
	}

	return labels
}

// String names each CLI version and the seconds it retried one write. A
// version with no refused attempt is "not measured".
func (r *RetryReport) String() string {
	parts := make([]string, 0, len(r.order))

	for _, label := range r.order {
		if !r.attempted[label] {
			parts = append(parts, label+": not measured")

			continue
		}

		parts = append(parts, fmt.Sprintf("%s: %s", label, r.window(label).Round(time.Second)))
	}

	return strings.Join(parts, ", ")
}

// window returns the smallest window of version over its rounds, from the
// first to the last attempt of one round.
func (r *RetryReport) window(version string) time.Duration {
	window := time.Duration(0)
	found := false

	for key, first := range r.first {
		if key.version != version {
			continue
		}

		round := r.last[key].Sub(first)
		if !found || round < window {
			window = round
			found = true
		}
	}

	return window
}
