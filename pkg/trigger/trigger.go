// Package trigger is the tool-neutral model of run triggers: the rule that
// starts a plan job of a unit on its own, and the ports that store the rules
// and fire them.
//
// A trigger has one unit, the unit that it starts, and one kind. A trigger of
// the kind review-request-push starts when code in a review request changes.
// A trigger of the kind schedule starts at the times of a frequency. A trigger
// of the kind follows starts after another unit, its source, applies.
//
// A trigger starts a plan and nothing else: the Starter port has no method
// that starts an apply. A job that a trigger starts passes through the job
// service, so the gates, the approval rules, the freezes and the manual locks
// apply to it as to any job. The package never reads a state and never talks
// to a forge. Triggers are tool-neutral: a unit of one tool can follow a unit
// of another tool.
package trigger

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/cron"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Sentinel errors of the trigger model and its ports.
var (
	// ErrInvalid reports a value that fails validation. The error text names
	// the field.
	ErrInvalid = errors.New("invalid trigger")
	// ErrNotFound reports a trigger that does not exist in the namespace that
	// a call names.
	ErrNotFound = errors.New("trigger not found")
	// ErrCycle reports a follows trigger that closes a loop of units. The
	// error is a *CycleError that holds the path.
	ErrCycle = errors.New("trigger closes a loop of units")
	// ErrDisabled reports a trigger that is switched off. The dispatcher
	// starts nothing.
	ErrDisabled = errors.New("trigger is off")
	// ErrVersionMismatch reports an upstream event whose state version is not
	// a final state version of the source unit. The dispatcher starts
	// nothing.
	ErrVersionMismatch = errors.New("state version is not a final state version of the unit")
)

// Bounds of a trigger.
const (
	// MaxIDBytes is the longest trigger ID.
	MaxIDBytes = 64
	// MaxVersionBytes is the longest state version ID in an Applied event.
	MaxVersionBytes = 128
	// MaxPathShown is the most units that the text of a CycleError names.
	MaxPathShown = 10
	// MaxSaverBytes is the longest subject of a last saver.
	MaxSaverBytes = 255
	// maxEchoBytes bounds the part of the input that an error text repeats.
	maxEchoBytes = 32
)

// ID is the ID of a trigger. The server makes it, and it is unique in its
// namespace. It has 1 to MaxIDBytes characters from a-z, 0-9, "-" and "_".
type ID string

// Validate returns an error that wraps ErrInvalid when id is not an ID.
func (id ID) Validate() error {
	if id == "" || len(id) > MaxIDBytes {
		return fmt.Errorf("%w: trigger ID length %d", ErrInvalid, len(id))
	}

	for i := range len(id) {
		c := id[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("%w: trigger ID has a byte that is not in a-z, 0-9, \"-\" or \"_\"", ErrInvalid)
		}
	}

	return nil
}

// Kind is the kind of a trigger.
type Kind string

// The kinds of a trigger.
const (
	// KindReviewRequestPush starts a plan when a push changes a review
	// request that has code of the unit.
	KindReviewRequestPush Kind = "review-request-push"
	// KindSchedule starts a plan at the times of a frequency.
	KindSchedule Kind = "schedule"
	// KindFollows starts a plan after the source unit applies.
	KindFollows Kind = "follows"
)

// Kinds returns the kinds of a trigger. Each call returns a new slice.
func Kinds() []Kind {
	return []Kind{KindReviewRequestPush, KindSchedule, KindFollows}
}

// Valid reports whether k is a kind of a trigger.
func (k Kind) Valid() bool {
	return slices.Contains(Kinds(), k)
}

// Frequency says when a schedule trigger starts. Exactly one of Preset and
// Cron is set. Cron is a custom expression of five fields that runs in UTC
// and never asks for two runs within an hour (package cron). A preset has an
// offset inside its period that a hash of the stable ID of the unit fixes.
type Frequency struct {
	Preset cron.Preset
	Cron   string
}

// Validate returns an error that wraps ErrInvalid when f is not a valid
// frequency. Defense in depth: the HTTP adapter checks the same value and
// the dispatcher parses it again at each use, so a row that another version
// wrote cannot start a plan at a rate that the rules refuse.
func (f Frequency) Validate() error {
	switch {
	case f.Preset != "" && f.Cron != "":
		return fmt.Errorf("%w: a frequency has a preset or a cron expression, not both", ErrInvalid)
	case f.Preset != "":
		if !f.Preset.Valid() {
			return fmt.Errorf("%w: unknown preset", ErrInvalid)
		}

		return nil
	case f.Cron != "":
		if _, err := cron.Parse(f.Cron); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		return nil
	default:
		return fmt.Errorf("%w: a frequency needs a preset or a cron expression", ErrInvalid)
	}
}

// Expression returns the cron expression that the frequency means for unit.
// It returns an error that wraps ErrInvalid when f or unit is not valid.
func (f Frequency) Expression(unit graph.Unit) (string, error) {
	if err := f.Validate(); err != nil {
		return "", err
	}

	if err := unit.Validate(); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if f.Cron != "" {
		return f.Cron, nil
	}

	expr, err := cron.Expression(f.Preset, unit.ID)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return expr, nil
}

// Trigger is one rule that starts a plan of Unit. Unit is the unit that the
// trigger starts. Frequency is set for the kind schedule only. Source is set
// for the kind follows only: the unit that Unit follows.
//
// LastSaver is the subject of the principal that saved the trigger last. The
// server sets it at each save from the caller, and a client never gives it.
// A start that no person makes runs as the namespace service principal, and
// the grants that the trigger needs for reads and for the start check come
// from its last saver, at the time of each use. A trigger with no saver, and
// a trigger whose saver is the subject of a namespace service principal,
// does not validate.
//
// A disabled trigger keeps its rule and starts nothing.
type Trigger struct {
	ID        ID
	Unit      graph.Unit
	Kind      Kind
	Frequency Frequency
	Source    graph.Unit
	Enabled   bool
	LastSaver string
}

// Validate returns an error that wraps ErrInvalid when t is not a valid
// trigger. A follows trigger whose source is its own unit returns an error
// that wraps ErrCycle. A source in another namespace is refused, because a
// write in one namespace carries the fence of that namespace only.
func (t Trigger) Validate() error {
	if err := t.ID.Validate(); err != nil {
		return err
	}

	if err := t.Unit.Validate(); err != nil {
		return fmt.Errorf("%w: unit: %w", ErrInvalid, err)
	}

	if err := validateSaver(t.LastSaver); err != nil {
		return err
	}

	return t.validateKind()
}

// validateKind checks the kind of t and the fields that the kind allows.
func (t Trigger) validateKind() error {
	if !t.Kind.Valid() {
		return fmt.Errorf("%w: kind %s", ErrInvalid, quote(string(t.Kind)))
	}

	hasFrequency := t.Frequency != Frequency{Preset: "", Cron: ""}
	hasSource := t.Source != graph.Unit{Namespace: "", Tool: "", ID: ""}

	switch t.Kind {
	case KindSchedule:
		if hasSource {
			return fmt.Errorf("%w: a schedule trigger has no source", ErrInvalid)
		}

		return t.Frequency.Validate()
	case KindFollows:
		if hasFrequency {
			return fmt.Errorf("%w: a follows trigger has no frequency", ErrInvalid)
		}

		return t.validateSource()
	case KindReviewRequestPush:
		if hasFrequency || hasSource {
			return fmt.Errorf("%w: a review-request-push trigger has no frequency and no source", ErrInvalid)
		}

		return nil
	default:
		return fmt.Errorf("%w: kind %s", ErrInvalid, quote(string(t.Kind)))
	}
}

// validateSaver checks the subject of a last saver. Defense in depth: the
// route and the manager set the saver from the caller, and the store checks
// the value again, so a row that another version wrote cannot name a saver
// that the rules refuse.
func validateSaver(saver string) error {
	switch {
	case saver == "":
		return fmt.Errorf("%w: a trigger has a last saver", ErrInvalid)
	case len(saver) > MaxSaverBytes:
		return fmt.Errorf("%w: the last saver has at most %d bytes", ErrInvalid, MaxSaverBytes)
	case !utf8.ValidString(saver) || strings.ContainsFunc(saver, audit.IsHiddenRune):
		return fmt.Errorf("%w: the last saver has a byte that is not allowed", ErrInvalid)
	case identity.IsNamespaceServiceSubject(saver):
		return fmt.Errorf("%w: a namespace service principal cannot be the last saver", ErrInvalid)
	default:
		return nil
	}
}

// validateSource checks the source of a follows trigger.
func (t Trigger) validateSource() error {
	if err := t.Source.Validate(); err != nil {
		return fmt.Errorf("%w: source: %w", ErrInvalid, err)
	}

	if t.Source.Namespace != t.Unit.Namespace {
		return fmt.Errorf("%w: a unit follows a unit of its own namespace", ErrInvalid)
	}

	if t.Source == t.Unit {
		return &CycleError{Path: []graph.Unit{t.Unit, t.Unit}, Names: nil}
	}

	return nil
}

// CycleError is the error of a follows trigger that closes a loop. Path
// starts and ends with the same unit. It lists the units in the order of the
// events: when the first unit applies, the second starts a plan, and so on
// until the last unit, which is the first again.
type CycleError struct {
	Path []graph.Unit
	// Names optionally maps a unit of Path to the name that the tool of the
	// unit uses. An adapter that knows the names fills it, so the text says
	// "stack" or "workspace" words. Error uses the unit ID for a unit with no
	// entry.
	Names map[graph.Unit]string
}

// Error names the path. It shows at most MaxPathShown units, by name or by unit ID.
func (e *CycleError) Error() string {
	shown := e.Path[:min(len(e.Path), MaxPathShown)]
	ids := make([]string, 0, len(shown))

	for _, u := range shown {
		name, ok := e.Names[u]
		if !ok || name == "" {
			name = u.ID
		}

		ids = append(ids, name)
	}

	more := ""
	if len(e.Path) > len(shown) {
		more = fmt.Sprintf(" (%d more)", len(e.Path)-len(shown))
	}

	return fmt.Sprintf("%v: %s%s", ErrCycle, strings.Join(ids, " -> "), more)
}

// Unwrap lets errors.Is(err, ErrCycle) report true.
func (e *CycleError) Unwrap() error {
	return ErrCycle
}

// CheckCycle reports whether t, added to existing, closes a loop of follows
// triggers. existing holds the triggers of the namespace of t. Only enabled
// follows triggers count, and a trigger of existing with the ID of t is
// replaced by t. It returns a *CycleError, which wraps ErrCycle, with the
// path of the loop, and nil when t is not an enabled follows trigger or
// closes no loop.
//
// Every store adapter calls it in the transaction of Put, over the triggers
// that it reads in that transaction. So all adapters apply one rule. It is a
// layer of its own: the depth check of the dispatcher at run time, which the
// limits slice adds, does not replace it.
func CheckCycle(existing []Trigger, t Trigger) error {
	if t.Kind != KindFollows || !t.Enabled {
		return nil
	}

	// next maps a source unit to the units that follow it.
	next := map[graph.Unit][]graph.Unit{}

	for i := range existing {
		if e := &existing[i]; e.Kind == KindFollows && e.Enabled && e.ID != t.ID {
			next[e.Source] = append(next[e.Source], e.Unit)
		}
	}

	next[t.Source] = append(next[t.Source], t.Unit)

	// The new edge is source -> unit. A loop exists when the source can be
	// reached from the unit.
	if path, ok := findPath(next, t.Unit, t.Source); ok {
		return &CycleError{Path: append([]graph.Unit{t.Source}, path...), Names: nil}
	}

	return nil
}

// findPath returns a path of units from `from` to `to` over next, and
// whether one exists. The path includes both ends. The search visits each
// unit once, so its work is bounded by the number of triggers.
func findPath(next map[graph.Unit][]graph.Unit, from, to graph.Unit) ([]graph.Unit, bool) {
	type step struct {
		unit graph.Unit
		prev int
	}

	queue := []step{{unit: from, prev: -1}}
	seen := map[graph.Unit]bool{from: true}

	for i := 0; i < len(queue); i++ {
		if queue[i].unit == to {
			var path []graph.Unit

			for j := i; j >= 0; j = queue[j].prev {
				path = append(path, queue[j].unit)
			}

			slices.Reverse(path)

			return path, true
		}

		for _, n := range next[queue[i].unit] {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, step{unit: n, prev: i})
			}
		}
	}

	return nil, false
}

// quote repeats a part of the input in an error text: cut to 32 bytes and
// escaped, so an error text is one short line.
func quote(text string) string {
	if len(text) > maxEchoBytes {
		text = text[:maxEchoBytes]
	}

	return fmt.Sprintf("%q", text)
}
