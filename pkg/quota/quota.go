// Package quota counts what a namespace uses and refuses a change above a
// limit. Version 1 counts two things: the units made from a blueprint, and
// the requests in a time window. A rule of a third kind is a custom check in
// Expr, which the caller runs with the usage of the other rules.
//
// The usage of a unit count is the settled count, which follows the
// inventory, plus the reserved count: the reservations of the creates in
// flight. A reservation holds one unit of each of its keys from the submit
// of a create to its end, so two submits at the same time never both take
// the last unit.
//
// The package holds the port Counter. The default adapter is a counter in
// the database of the namespace, in the SQL store adapter that also holds
// the Catalog records, so that a reservation and the revision that holds it
// commit in one transaction (Tx). The package imports no transport and no
// store.
package quota

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

var (
	// ErrExceeded reports a reservation or a request above a limit. The
	// error is an *ExceededError, which names each key, its usage and its
	// maximum.
	ErrExceeded = errors.New("quota exceeded")
	// ErrNotFound reports a reservation ID that the counter does not know.
	ErrNotFound = errors.New("quota reservation not found")
	// ErrEnded reports a settle of a released reservation, or a release of
	// a settled one.
	ErrEnded = errors.New("quota reservation already ended the other way")
	// ErrConflict reports a reservation ID that the counter knows with other
	// keys.
	ErrConflict = errors.New("quota reservation ID names other keys")
	// ErrInvalid reports a rule, a key or a reservation with an empty or bad
	// field.
	ErrInvalid = errors.New("quota rule is not valid")
	// ErrRuleFailed reports a rule of KindExpr whose expression failed to
	// run. The check cannot run: it is never a pass and never a failure of
	// the request, and the caller changes nothing.
	ErrRuleFailed = errors.New("quota rule failed to run")
)

// Kind names what a rule counts.
type Kind string

// The kinds of rule.
const (
	// KindUnits counts the units made from a blueprint.
	KindUnits Kind = "units"
	// KindRequests counts the submits of revisions in a time window.
	KindRequests Kind = "requests"
	// KindExpr is a custom check: an Expr expression that gives a boolean.
	KindExpr Kind = "expr"
)

// Bounds of a rule.
const (
	MinWindow       = time.Minute
	MaxWindow       = 31 * 24 * time.Hour
	MaxExprBytes    = 4 << 10
	MaxMessageBytes = 1 << 10
	// MaxRules bounds the rules of one blueprint version.
	MaxRules = 16
	// ReservationGrace is the time after the longest wait of a revision
	// that its reservation still counts. The end time of a reservation is
	// the deadline of its revision, which adds this grace. After it, a lost
	// release cannot hold a slot.
	ReservationGrace = 24 * time.Hour
)

// Scope is who a rule of KindUnits or KindRequests counts together.
type Scope string

// The scopes of a rule. A group scope is ScopeGroupPrefix followed by the
// name of a group, for example "group:dba".
const (
	// ScopeNamespace counts every request of the blueprint in the namespace.
	ScopeNamespace Scope = "namespace"
	// ScopeRequester counts the requests of each subject apart.
	ScopeRequester Scope = "requester"
	// ScopeGroupPrefix starts a scope that counts the requests of the
	// members of one group together.
	ScopeGroupPrefix = "group:"
)

// Group returns the group of a group scope, and false for another scope.
func (s Scope) Group() (string, bool) {
	g, ok := strings.CutPrefix(string(s), ScopeGroupPrefix)

	return g, ok && g != ""
}

// Valid reports whether s is one of the scopes of this package.
func (s Scope) Valid() bool {
	_, group := s.Group()

	return s == ScopeNamespace || s == ScopeRequester || group
}

// Rule is one quota rule of a blueprint version. Max and Scope apply to
// KindUnits and KindRequests, Window to KindRequests only, and Expr and
// Message to KindExpr only.
type Rule struct {
	Kind    Kind
	Scope   Scope
	Expr    string
	Message string
	Window  time.Duration
	Max     int
}

// Validate reports whether r is a rule that a blueprint version can hold.
// It returns ErrInvalid otherwise.
func (r Rule) Validate() error {
	switch r.Kind {
	case KindUnits, KindRequests:
		return r.validateCount()
	case KindExpr:
		return r.validateExpr()
	default:
		return fmt.Errorf("%w: kind %q", ErrInvalid, r.Kind)
	}
}

func (r Rule) validateCount() error {
	switch {
	case !r.Scope.Valid():
		return fmt.Errorf("%w: scope %q", ErrInvalid, r.Scope)
	case r.Max < 0:
		return fmt.Errorf("%w: max %d is below 0", ErrInvalid, r.Max)
	case r.Expr != "" || r.Message != "":
		return fmt.Errorf("%w: a %s rule has no expression", ErrInvalid, r.Kind)
	case r.Kind == KindRequests && (r.Window < MinWindow || r.Window > MaxWindow):
		return fmt.Errorf("%w: window %v, give %v to %v", ErrInvalid, r.Window, MinWindow, MaxWindow)
	case r.Kind == KindUnits && r.Window != 0:
		return fmt.Errorf("%w: a units rule has no window", ErrInvalid)
	default:
		return nil
	}
}

func (r Rule) validateExpr() error {
	switch {
	case r.Expr == "" || len(r.Expr) > MaxExprBytes:
		return fmt.Errorf("%w: expression of %d bytes, give 1 to %d", ErrInvalid, len(r.Expr), MaxExprBytes)
	case r.Message == "" || len(r.Message) > MaxMessageBytes:
		return fmt.Errorf("%w: message of %d bytes, give 1 to %d", ErrInvalid, len(r.Message), MaxMessageBytes)
	case r.Scope != "" || r.Max != 0 || r.Window != 0:
		return fmt.Errorf("%w: an expr rule has no scope, max or window", ErrInvalid)
	default:
		return nil
	}
}

// Key is what the counter counts apart: one rule kind and one scope value
// for one blueprint of one namespace. Scope is the scope with its value:
// "namespace", "requester:<subject>" or "group:<name>". A rule of a later
// blueprint version with the same kind and scope counts the same key.
type Key struct {
	Namespace namespace.Name
	Blueprint string
	Kind      Kind
	Scope     string
}

// String returns the key as <namespace>/<blueprint>/<kind>/<scope>.
func (k Key) String() string {
	return k.Namespace.String() + "/" + k.Blueprint + "/" + string(k.Kind) + "/" + k.Scope
}

// KeyFor returns the key of rule r for a request of subject, a member of
// groups, from blueprint in ns. It returns false when r does not count the
// request: a rule of KindExpr, or a group scope of a group that the subject
// is not a member of.
func KeyFor(ns namespace.Name, blueprint string, r Rule, subject string, groups []string) (Key, bool) {
	k := Key{Namespace: ns, Blueprint: blueprint, Kind: r.Kind, Scope: ""}

	if r.Kind != KindUnits && r.Kind != KindRequests {
		return k, false
	}

	if g, ok := r.Scope.Group(); ok {
		if !slices.Contains(groups, g) {
			return k, false
		}

		k.Scope = string(r.Scope)

		return k, true
	}

	switch r.Scope {
	case ScopeNamespace:
		k.Scope = string(ScopeNamespace)
	case ScopeRequester:
		k.Scope = string(ScopeRequester) + ":" + subject
	default:
		return k, false
	}

	return k, true
}

// Check is one limit that a reservation or a request must pass: at most
// Max for Key. Window is the window of a key of KindRequests.
type Check struct {
	Key    Key
	Window time.Duration
	Max    int
}

// Usage is what one key uses at one time. Settled and Reserved are for a
// key of KindUnits, and Requests for a key of KindRequests. A reservation
// whose end time passed counts in no usage.
type Usage struct {
	Key      Key
	Settled  int
	Reserved int
	Requests int
}

// Used returns the usage of the key: the settled and the reserved units, or
// the requests in the window.
func (u Usage) Used() int {
	return u.Settled + u.Reserved + u.Requests
}

// Exceeded is one limit that a change would pass.
type Exceeded struct {
	Key  Key
	Used int
	Max  int
}

// ExceededError is the error of a change above one or more limits. It
// matches ErrExceeded with errors.Is.
type ExceededError struct {
	Exceeded []Exceeded
}

// Error names each key with its usage and its maximum.
func (e *ExceededError) Error() string {
	parts := make([]string, 0, len(e.Exceeded))
	for _, x := range e.Exceeded {
		parts = append(parts, fmt.Sprintf("%s uses %d of %d", x.Key, x.Used, x.Max))
	}

	return ErrExceeded.Error() + ": " + strings.Join(parts, ", ")
}

// Is reports whether target is ErrExceeded.
func (*ExceededError) Is(target error) bool {
	return target == ErrExceeded
}

// Evaluate returns each check that a change of adding more would pass, with
// the usage of usages: Used plus adding above Max. A check whose key has no
// usage counts 0. It returns nil when each check passes.
func Evaluate(checks []Check, usages []Usage, adding int) []Exceeded {
	used := make(map[Key]int, len(usages))
	for _, u := range usages {
		used[u.Key] += u.Used()
	}

	var out []Exceeded

	for _, c := range checks {
		if n := used[c.Key]; n+adding > c.Max {
			out = append(out, Exceeded{Key: c.Key, Used: n, Max: c.Max})
		}
	}

	return out
}
