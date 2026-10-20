package gate

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"
)

// The limits of one scope and of one object. Above a limit, a save fails
// with ErrLimit.
const (
	MaxGatesPerScope          = 256
	MaxAccessLimitsPerScope   = 64
	MaxApprovalRulesPerScope  = 64
	MaxOwnershipRulesPerScope = 256
	MaxPathSetsPerScope       = 64
	// MaxConditionBytes bounds a condition, an unless body and the text of
	// an access limit.
	MaxConditionBytes = 4 << 10
	// MaxPathSetGlobs is the largest count of globs of a path set. A path
	// set holds at least one glob.
	MaxPathSetGlobs = 64
	// MaxFactDocumentBytes bounds a fact document. Above it, the server
	// empties the largest sets of strings, and names each one in
	// document.truncated.
	MaxFactDocumentBytes = 256 << 10
	// MaxSimulateRecords bounds the stored records of one simulate call.
	MaxSimulateRecords = 1000
)

// AccessRefusalWindow is the window in which refusals of one principal, one
// action and one access limit share one decision record.
const AccessRefusalWindow = 60 * time.Second

// The bounds of an approval count, an approval timeout and a fresh
// authentication window.
const (
	MinApprovalCount = 1
	MaxApprovalCount = 10
	// MaxApproverEntries is the most groups, and the most users, in the
	// approvers of one rule or default.
	MaxApproverEntries = 100
	// MaxApproverBytes is the most bytes in the name of one approver.
	MaxApproverBytes    = 256
	MinApprovalTimeout  = time.Hour
	MaxApprovalTimeout  = 30 * 24 * time.Hour
	DefaultApprovalWait = 7 * 24 * time.Hour
	MinFreshAuthWindow  = 60 * time.Second
	MaxFreshAuthWindow  = 900 * time.Second
)

// AccessLimit is one Cedar forbid policy of a scope. The server evaluates
// the limits of each scope that holds the object of a call after the
// grants allow the call. A limit only takes away what a grant gives. A
// policy with the effect permit is refused at save.
type AccessLimit struct {
	Scope   Scope
	ID      ID
	Version int64
	// Policy is the text of one Cedar forbid policy over the access
	// schema.
	Policy string
}

// PolicyID returns the Cedar policy ID of l.
func (l AccessLimit) PolicyID() PolicyID {
	return PolicyID{Scope: l.Scope, ID: l.ID}
}

// Ref returns the scope, the ID and the version of l.
func (l AccessLimit) Ref() ObjectRef {
	return ObjectRef{Scope: l.Scope, ID: l.ID, Version: l.Version}
}

// FreshAuth is an optional fresh authentication window: none, 0 or 60 to
// 900 seconds. None asks for no fresh authentication of its own. 0 asks
// for one at each step. No value turns the check off.
//
// Its JSON form is null for none and the count of seconds otherwise, under
// the name freshAuthSeconds.
type FreshAuth struct {
	// Set is false for none.
	Set bool
	// Seconds is the window when Set is true.
	Seconds int
}

// The JSON form of a fresh authentication window.
var (
	_ json.Marshaler   = FreshAuth{Set: false, Seconds: 0}
	_ json.Unmarshaler = (*FreshAuth)(nil)
)

// MarshalJSON returns null for none, and the count of seconds otherwise.
func (f FreshAuth) MarshalJSON() ([]byte, error) {
	if !f.Set {
		return []byte("null"), nil
	}

	return []byte(strconv.Itoa(f.Seconds)), nil
}

// UnmarshalJSON reads null as none, and a number with ParseFreshAuth.
func (f *FreshAuth) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*f = NoFreshAuth()

		return nil
	}

	var seconds int
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("%w: freshAuthSeconds: %w", ErrInvalid, err)
	}

	parsed, err := ParseFreshAuth(seconds)
	if err != nil {
		return err
	}

	*f = parsed

	return nil
}

// NoFreshAuth returns the window none.
func NoFreshAuth() FreshAuth {
	return FreshAuth{Set: false, Seconds: 0}
}

// ParseFreshAuth returns the window of seconds, or ErrInvalid for a value
// that is not 0 and not 60 to 900.
func ParseFreshAuth(seconds int) (FreshAuth, error) {
	d := time.Duration(seconds) * time.Second
	if seconds != 0 && (d < MinFreshAuthWindow || d > MaxFreshAuthWindow) {
		return FreshAuth{}, fmt.Errorf("%w: freshAuthSeconds %d: give 0 or 60 to 900", ErrInvalid, seconds)
	}

	return FreshAuth{Set: true, Seconds: seconds}, nil
}

// Approvers names who may approve: groups, users, and the owners of the
// unit in its context. An approver also needs the approve action of the
// object, and is a person in a session.
type Approvers struct {
	Groups []string `json:"groups"`
	Users  []string `json:"users"`
	// UnitOwners reads the owners of the unit from the context at the
	// scope of the rule or at a wider scope only.
	UnitOwners bool `json:"unitOwners"`
}

// Empty reports whether a names no approver.
func (a Approvers) Empty() bool {
	return len(a.Groups) == 0 && len(a.Users) == 0 && !a.UnitOwners
}

// Validate returns an error that wraps ErrInvalid for approvers that name an
// empty or a too long entry, or more than MaxApproverEntries groups or
// users. It does not check that the approvers are not empty: a default may
// name none.
func (a Approvers) Validate() error {
	for _, l := range []struct {
		kind string
		list []string
	}{{"group", a.Groups}, {"user", a.Users}} {
		if len(l.list) > MaxApproverEntries {
			return fmt.Errorf("%w: %d %ss: give at most %d", ErrInvalid, len(l.list), l.kind, MaxApproverEntries)
		}

		for _, name := range l.list {
			if name == "" || len(name) > MaxApproverBytes || !utf8.ValidString(name) {
				return fmt.Errorf("%w: a %s name is empty, too long, or not UTF-8", ErrInvalid, l.kind)
			}
		}
	}

	return nil
}

// ApprovalRule says who may approve and how many people must approve. A
// gate names it in its outcome as approval:<ID>. The server resolves the
// ID at the scope of the gate, then at each wider scope, never at a
// narrower scope.
type ApprovalRule struct {
	Scope   Scope
	ID      ID
	Version int64
	// Approvers holds at least one entry.
	Approvers Approvers
	// Count is 1 to 10.
	Count int
	// AuthorMayApprove lets the author approve. The default is false.
	AuthorMayApprove bool
	// FreshAuth is the window of each approval of the rule.
	FreshAuth FreshAuth
	// Timeout is 1 hour to 30 days. The zero value means the default of 7
	// days.
	Timeout time.Duration
}

// Ref returns the scope, the ID and the version of r.
func (r ApprovalRule) Ref() ObjectRef {
	return ObjectRef{Scope: r.Scope, ID: r.ID, Version: r.Version}
}

// validateCount returns an error that wraps ErrInvalid for a count outside
// MinApprovalCount to MaxApprovalCount.
func validateCount(count int) error {
	if count < MinApprovalCount || count > MaxApprovalCount {
		return fmt.Errorf("%w: count %d: give %d to %d", ErrInvalid, count, MinApprovalCount, MaxApprovalCount)
	}

	return nil
}

// validateFresh returns an error that wraps ErrInvalid for a window that
// ParseFreshAuth refuses.
func validateFresh(f FreshAuth) error {
	if !f.Set {
		return nil
	}

	_, err := ParseFreshAuth(f.Seconds)

	return err
}

// Validate returns an error that wraps ErrInvalid for a rule that holds no
// approver, a count outside 1 to 10, a window that ParseFreshAuth refuses, a
// timeout outside MinApprovalTimeout to MaxApprovalTimeout (zero takes the
// default), or an ID that ParseRuleID refuses. Each write path calls it, in
// the service and in the store.
func (r ApprovalRule) Validate() error {
	if _, err := ParseRuleID(string(r.ID)); err != nil {
		return err
	}

	if r.Approvers.Empty() {
		return fmt.Errorf("%w: an approval rule names at least one group, user or unitOwners", ErrInvalid)
	}

	if err := r.Approvers.Validate(); err != nil {
		return err
	}

	if err := validateCount(r.Count); err != nil {
		return err
	}

	if err := validateFresh(r.FreshAuth); err != nil {
		return err
	}

	if r.Timeout != 0 && (r.Timeout < MinApprovalTimeout || r.Timeout > MaxApprovalTimeout) {
		return fmt.Errorf(
			"%w: timeout %s: give %s to %s",
			ErrInvalid,
			r.Timeout,
			MinApprovalTimeout,
			MaxApprovalTimeout,
		)
	}

	return nil
}

// ApprovalDefault is the requirement that a namespace, a collection or a
// unit sets for each change below it, with no gate. With none set, no
// change needs an approval. Each default holds on its own.
type ApprovalDefault struct {
	Scope   Scope
	Version int64
	// Count is 1 to 10.
	Count     int
	FreshAuth FreshAuth
	// Approvers is optional. With none, any holder of the approve action
	// who is not the author may approve, and the default merges into the
	// requirement "default".
	Approvers Approvers
	// Unless is an optional Cedar body over the facts of change.planned. It
	// exempts a change from this default only.
	Unless string
	// Reads holds the IDs of the path sets that Unless reads.
	Reads []ID
}

// Validate returns an error that wraps ErrInvalid for a default with a count
// outside 1 to 10, a window that ParseFreshAuth refuses, or approvers that
// Approvers.Validate refuses. Each write path calls it, in the service and in
// the store.
func (d ApprovalDefault) Validate() error {
	if err := validateCount(d.Count); err != nil {
		return err
	}

	if err := validateFresh(d.FreshAuth); err != nil {
		return err
	}

	return d.Approvers.Validate()
}

// OwnershipRule names who owns a part of a change. It needs no gate. Each
// rule in enforce mode whose Paths and When match gives one requirement
// owners/<ID>.
type OwnershipRule struct {
	Scope   Scope
	ID      ID
	Version int64
	Mode    Mode
	// Paths are glob patterns that the server matches in Go against the
	// changed paths. Empty means any change.
	Paths []string
	// When is a Cedar condition over the facts of change.planned. Empty
	// means true.
	When string
	// Approvers holds the groups and the users. An ownership rule names no
	// unit owners.
	Approvers Approvers
	// Count is 1 to 10.
	Count     int
	FreshAuth FreshAuth
	// Reads holds the IDs of the path sets that When reads.
	Reads []ID
}

// Ref returns the scope, the ID and the version of r.
func (r OwnershipRule) Ref() ObjectRef {
	return ObjectRef{Scope: r.Scope, ID: r.ID, Version: r.Version}
}

// PathSet is a named set of 1 to 64 glob patterns of a scope. Go matches
// the globs against the changed paths and gives the facts
// paths.touches["<ID>"] and paths.only["<ID>"].
type PathSet struct {
	Scope   Scope
	ID      ID
	Version int64
	Globs   []string
}

// Ref returns the scope, the ID and the version of p.
func (p PathSet) Ref() ObjectRef {
	return ObjectRef{Scope: p.Scope, ID: p.ID, Version: p.Version}
}

// CarryOver is the signature carry-over setting of one scope.
type CarryOver string

// The values of CarryOver. The installation takes CarryOverOff and
// CarryOverForbidden only.
const (
	// CarryOverOff voids each signature at a new plan revision. It is the
	// default.
	CarryOverOff CarryOver = "off"
	// CarryOverOn lets a signature carry over to a revision with identical
	// plan digest inputs.
	CarryOverOn CarryOver = "on"
	// CarryOverForbidden stops each scope below from setting on.
	CarryOverForbidden CarryOver = "forbidden"
)

// ParseCarryOver returns the value s for scope, or ErrInvalid. The
// installation refuses on.
func ParseCarryOver(scope Scope, s string) (CarryOver, error) {
	switch c := CarryOver(s); c {
	case CarryOverOff, CarryOverForbidden:
		return c, nil
	case CarryOverOn:
		if scope.Level() == LevelInstallation {
			return "", fmt.Errorf("%w: the installation takes off or forbidden", ErrInvalid)
		}

		return c, nil
	default:
		return "", fmt.Errorf("%w: unknown signature carry-over %q", ErrInvalid, s)
	}
}

// CarryOverSetting is the stored setting of one scope.
type CarryOverSetting struct {
	Scope   Scope
	Version int64
	Value   CarryOver
}
