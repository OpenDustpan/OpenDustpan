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
	errNoWriteKind     = errors.New("retry attempt is no write kind of the retry report")
)

// WriteKind names one kind of state write that a CLI sends during an update.
type WriteKind string

// The write kinds of the retry report. Each kind has its own retry window,
// because a CLI can retry one kind and not retry another.
const (
	// WriteCheckpoint is a Pulumi checkpoint or journal save.
	WriteCheckpoint WriteKind = "checkpoint"
	// WriteEvents is a Pulumi engine event batch.
	WriteEvents WriteKind = "events"
	// WriteComplete is the Pulumi write that completes an update.
	WriteComplete WriteKind = "complete"
	// WriteStateVersion is an OpenTofu state version write.
	WriteStateVersion WriteKind = "state version"
)

// writeKinds returns the write kinds in the order of the report.
func writeKinds() []WriteKind {
	return []WriteKind{WriteCheckpoint, WriteEvents, WriteComplete, WriteStateVersion}
}

// Path lengths, in segments, of the update writes of the Pulumi
// Cloud-compatible API and of the state version write of the TFE-compatible
// API.
const (
	pulumiWriteSegments = 8 // api stacks <namespace> <project> <stack> update <id> <write>
	tofuWriteSegments   = 5 // api v2 workspaces <id> state-versions
)

// WriteKindOf returns the write kind of one request. It returns false for any
// request that is not one of the write kinds, for example a read, a lease
// renewal or a cancel.
func WriteKindOf(method, path string) (WriteKind, bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if slices.Contains(parts, "") {
		return "", false
	}

	switch {
	case len(parts) == tofuWriteSegments:
		return tofuWriteKind(method, parts)
	case len(parts) >= pulumiWriteSegments:
		return pulumiWriteKind(method, parts)
	default:
		return "", false
	}
}

// tofuWriteKind returns the write kind of the path segments of a
// TFE-compatible route.
func tofuWriteKind(method string, parts []string) (WriteKind, bool) {
	if method == http.MethodPost && slices.Equal(parts[:3], []string{"api", "v2", "workspaces"}) &&
		parts[4] == "state-versions" {
		return WriteStateVersion, true
	}

	return "", false
}

// pulumiWriteKind returns the write kind of the path segments of a Pulumi
// update route.
func pulumiWriteKind(method string, parts []string) (WriteKind, bool) {
	if !slices.Equal(parts[:2], []string{"api", "stacks"}) || parts[5] != "update" {
		return "", false
	}

	kind, found := pulumiWrites()[method+" "+strings.Join(parts[pulumiWriteSegments-1:], "/")]

	return kind, found
}

// pulumiWrites maps the method and the path after the update ID of each
// Pulumi update write to its write kind.
func pulumiWrites() map[string]WriteKind {
	return map[string]WriteKind{
		http.MethodPatch + " checkpoint":         WriteCheckpoint,
		http.MethodPatch + " checkpointverbatim": WriteCheckpoint,
		http.MethodPatch + " checkpointdelta":    WriteCheckpoint,
		http.MethodPatch + " journalentries":     WriteCheckpoint,
		http.MethodPost + " events/batch":        WriteEvents,
		http.MethodPost + " complete":            WriteComplete,
	}
}

// WriteAttempt is one retry of one state write during the outage. Round names
// the outage of the attempt: a retry window never spans two outages.
type WriteAttempt struct {
	Version string
	Method  string
	Path    string
	Status  int
	Round   int
	At      time.Time
}

// windowKey names the attempts of one write kind of one CLI version in one
// outage.
type windowKey struct {
	version string
	kind    WriteKind
	round   int
}

// RetryReport holds the retry window of each write kind of each CLI version
// of the matrix: the smallest window of the kind over the outages that it
// was refused in. A kind with one refused attempt in an outage is not
// retried, and its window is 0 s.
type RetryReport struct {
	order     []string
	attempted map[string]bool
	first     map[windowKey]time.Time
	last      map[windowKey]time.Time
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
		first:     map[windowKey]time.Time{},
		last:      map[windowKey]time.Time{},
	}

	for _, cli := range matrix {
		report.order = append(report.order, VersionLabel(cli.Tool, cli.Version))
	}

	return report
}

// Add records one refused write attempt of one CLI version. It refuses an
// attempt that is not one of the write kinds.
func (r *RetryReport) Add(attempt WriteAttempt) error {
	if !slices.Contains(r.order, attempt.Version) {
		return fmt.Errorf("%w: %q", errUnknownVersion, attempt.Version)
	}

	if attempt.Status != http.StatusServiceUnavailable || attempt.Method == http.MethodGet {
		return fmt.Errorf("%w: %s %s answered %d", errWriteNotRefused,
			attempt.Method, attempt.Path, attempt.Status)
	}

	kind, found := WriteKindOf(attempt.Method, attempt.Path)
	if !found {
		return fmt.Errorf("%w: %s %s", errNoWriteKind, attempt.Method, attempt.Path)
	}

	if attempt.At.IsZero() {
		return errNoAttemptTime
	}

	r.attempted[attempt.Version] = true

	key := windowKey{version: attempt.Version, kind: kind, round: attempt.Round}

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

// Shortest returns the smallest retry window above 0 s of all write kinds of
// all versions. It returns zero when no write kind has a window above 0 s.
func (r *RetryReport) Shortest() time.Duration {
	shortest := time.Duration(0)

	for _, label := range r.order {
		for _, kind := range writeKinds() {
			window, found := r.window(label, kind)
			if !found || window <= 0 {
				continue
			}

			if shortest == 0 || window < shortest {
				shortest = window
			}
		}
	}

	return shortest
}

// NotRetried returns each write kind of version that has a window of 0 s:
// the CLI did not retry the refused write. A kind with no refused attempt is
// not in the list.
func (r *RetryReport) NotRetried(version string) []WriteKind {
	var kinds []WriteKind

	for _, kind := range writeKinds() {
		if window, found := r.window(version, kind); found && window == 0 {
			kinds = append(kinds, kind)
		}
	}

	return kinds
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

// String names each CLI version and write kind and the seconds that the CLI
// retried one write of that kind. A kind with no refused attempt is not in
// the report. A version with no refused attempt is "not measured".
func (r *RetryReport) String() string {
	parts := make([]string, 0, len(r.order))

	for _, label := range r.order {
		if !r.attempted[label] {
			parts = append(parts, label+": not measured")

			continue
		}

		for _, kind := range writeKinds() {
			if window, found := r.window(label, kind); found {
				parts = append(parts, fmt.Sprintf("%s %s: %s", label, kind, window.Round(time.Second)))
			}
		}
	}

	return strings.Join(parts, ", ")
}

// window returns the smallest window of one write kind of version over its
// rounds, from the first to the last attempt of one round. It returns false
// when the kind has no refused attempt.
func (r *RetryReport) window(version string, kind WriteKind) (time.Duration, bool) {
	window := time.Duration(0)
	found := false

	for key, first := range r.first {
		if key.version != version || key.kind != kind {
			continue
		}

		round := r.last[key].Sub(first)
		if !found || round < window {
			window = round
			found = true
		}
	}

	return window, found
}
