package unmanaged

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// day is the length of one day, to give MaxIgnore in days.
const day = 24 * time.Hour

// Bounds of findings and ignore rules.
const (
	// MaxReasonBytes bounds the reason of an ignore and of an ignore rule.
	MaxReasonBytes = 500
	// MaxIgnore is the longest time that an ignore with an end can last.
	// An ignore with no end lasts until a person stops it.
	MaxIgnore = 400 * 24 * time.Hour
	// MaxRules bounds the ignore rules of a namespace.
	MaxRules = 500
	// MaxFindingUnits bounds the units that one finding names. A resource
	// in more units than this names the first units by Unit.String and
	// counts the others.
	MaxFindingUnits = 20
	// MaxPage bounds a page of findings.
	MaxPage = 500
	// MinPending is the shortest wait of a finding in StatusPending
	// (PendingWait). It is longer than the 14 days after which a job that
	// waits for a state version expires, so a job ends first.
	MinPending = 15 * day
	// pendingRuns is how many runs of its target a pending finding waits
	// for, at least (PendingWait).
	pendingRuns = 2
)

// Status is where a finding is in its life.
type Status string

// The statuses of a finding.
const (
	// StatusOpen is a finding that waits for a person.
	StatusOpen Status = "open"
	// StatusIgnored is a finding that a person or an ignore rule ignores,
	// with a reason.
	StatusIgnored Status = "ignored"
	// StatusPending is a finding with an action that started and did not
	// end: a job, a choice of owner, or a mark for delete that waits for the
	// next sweep.
	StatusPending Status = "pending"
	// StatusResolved is a finding whose resource is now managed by exactly
	// one unit, or that a complete sweep no longer sees.
	StatusResolved Status = "resolved"
)

// Event is something that happens to a finding.
type Event string

// The events of a finding.
const (
	// EventObserve is a sweep or an index check that classifies the resource
	// as the same kind again.
	EventObserve Event = "observe"
	// EventReclassify is a classification with another kind than the
	// finding has, for example left behind that becomes not in any unit
	// after the purge of its unit. The finding takes the new kind.
	EventReclassify Event = "reclassify"
	// EventIgnore is an ignore by a person or by a rule.
	EventIgnore Event = "ignore"
	// EventUnignore is the end of an ignore by a person.
	EventUnignore Event = "unignore"
	// EventIgnoreExpired is the end of an ignore at its end time.
	EventIgnoreExpired Event = "ignore-expired"
	// EventAct is the start of an action that changes something: an import,
	// code generation, a choice of owner or a mark for delete.
	EventAct Event = "act"
	// EventActionEnded is the end of an action that did not resolve the
	// finding, for example a job that failed or a person who canceled it.
	EventActionEnded Event = "action-ended"
	// EventResolve is a classification as managed, or a complete sweep of
	// the scope of the finding that does not see the resource.
	EventResolve Event = "resolve"
	// EventCancel is a person who stops waiting for a pending action. It
	// needs ActionNamespaceTriageUnmanaged. It does not cancel the job of
	// the action: a person cancels the job with the actions of the job.
	EventCancel Event = "cancel"
	// EventPendingExpired is the end of a wait in StatusPending at
	// Pending.Until.
	EventPendingExpired Event = "pending-expired"
)

// transitions returns the state table of a finding. Next is the only place
// that reads it.
func transitions() map[Status]map[Event]Status {
	return map[Status]map[Event]Status{
		StatusOpen: {
			EventObserve: StatusOpen, EventReclassify: StatusOpen, EventIgnore: StatusIgnored,
			EventAct: StatusPending, EventResolve: StatusResolved,
		},
		StatusIgnored: {
			EventObserve: StatusIgnored, EventReclassify: StatusOpen, EventUnignore: StatusOpen,
			EventIgnoreExpired: StatusOpen, EventResolve: StatusResolved,
		},
		StatusPending: {
			EventObserve: StatusPending, EventReclassify: StatusOpen, EventActionEnded: StatusOpen,
			EventResolve: StatusResolved, EventCancel: StatusOpen, EventPendingExpired: StatusOpen,
		},
		StatusResolved: {
			EventObserve: StatusOpen, EventReclassify: StatusOpen, EventResolve: StatusResolved,
		},
	}
}

// Next returns the status after ev in status s. It returns ErrTransition for
// an event that the table does not allow.
func Next(s Status, ev Event) (Status, error) {
	to, ok := transitions()[s][ev]
	if !ok {
		return s, fmt.Errorf("%w: %s on a finding that is %s", ErrTransition, ev, s)
	}

	return to, nil
}

// Evidence is how the server knows of the resource of a finding.
type Evidence string

// The evidence of a finding.
const (
	// EvidenceState is the resource index alone: the state of a unit holds
	// the resource. No sweep confirmed that the resource exists.
	EvidenceState Evidence = "state"
	// EvidenceScan is a sweep that saw the resource.
	EvidenceScan Evidence = "sweep"
)

// Ignore is why and until when a finding is ignored. Until is the zero time
// for an ignore with no end. Rule is the ID of the ignore rule, or empty for
// an ignore by a person.
type Ignore struct {
	Until  time.Time
	Reason string
	By     string
	Rule   string
}

// ActionKind is an action on a finding.
type ActionKind string

// The actions on a finding.
const (
	// ActionImport starts a job on a target unit that brings the resource
	// into the state of the unit.
	ActionImport ActionKind = "import"
	// ActionWriteCode starts a job on a target unit that generates the code
	// of the resource and delivers it as a review request.
	ActionWriteCode ActionKind = "write-code"
	// ActionDelete records the decision to delete the resource. For a
	// resource left behind in a unit in its recovery window, it can start
	// the restore of the unit and a destroy job. The server never deletes a
	// cloud resource itself.
	ActionDelete ActionKind = "delete"
	// ActionChooseOwner records the unit that keeps a resource that is
	// managed twice, and starts a job on each other unit that removes the
	// resource from its state and does not delete it.
	ActionChooseOwner ActionKind = "choose-owner"
	// ActionIgnore ignores the finding with a reason.
	ActionIgnore ActionKind = "ignore"
	// ActionCancel stops the wait of a pending finding, which opens again.
	ActionCancel ActionKind = "cancel"
)

// ActionSpec says to which kinds an action applies and which actions of
// package identity it needs, beside ActionNamespaceTriageUnmanaged on the
// namespace. UnitActions are checked on each unit that the action changes:
// the target unit of an import, or each unit that gives up a resource.
type ActionSpec struct {
	Action      ActionKind
	Kinds       []Kind
	UnitActions []identity.Action
	StartsJob   bool
}

// ActionCatalog returns the catalog of the actions on a finding. Each call
// returns a new slice, so a caller cannot change the catalog.
func ActionCatalog() []ActionSpec {
	return []ActionSpec{
		{
			Action: ActionImport, Kinds: []Kind{KindNotInAnyUnit, KindLeftBehind},
			UnitActions: []identity.Action{identity.ActionUnitWrite}, StartsJob: true,
		},
		{
			Action: ActionWriteCode, Kinds: []Kind{KindNotInAnyUnit, KindLeftBehind},
			UnitActions: []identity.Action{identity.ActionUnitWrite}, StartsJob: true,
		},
		{Action: ActionDelete, Kinds: []Kind{KindNotInAnyUnit, KindLeftBehind}, UnitActions: nil, StartsJob: false},
		{
			Action: ActionChooseOwner, Kinds: []Kind{KindManagedTwice},
			UnitActions: []identity.Action{identity.ActionUnitWrite}, StartsJob: true,
		},
		{
			Action:      ActionIgnore,
			Kinds:       []Kind{KindNotInAnyUnit, KindLeftBehind, KindManagedTwice},
			UnitActions: nil,
			StartsJob:   false,
		},
		{
			Action:      ActionCancel,
			Kinds:       []Kind{KindNotInAnyUnit, KindLeftBehind, KindManagedTwice},
			UnitActions: nil,
			StartsJob:   false,
		},
	}
}

// SpecOf returns the spec of action a for a finding of kind k. It returns
// ErrActionKind when a does not apply to k or does not exist.
func SpecOf(a ActionKind, k Kind) (ActionSpec, error) {
	for _, s := range ActionCatalog() {
		if s.Action != a {
			continue
		}

		if slices.Contains(s.Kinds, k) {
			return s, nil
		}
	}

	return ActionSpec{}, fmt.Errorf("%w: %s on %s", ErrActionKind, a, k)
}

// Pending is the action that a pending finding waits for. Job is the ID of
// the job that the action started, or empty. Keep is the unit that keeps a
// resource that is managed twice. Target is the unit of an import or of code
// generation. Until is the end of the wait, after At: At plus PendingWait.
type Pending struct {
	At     time.Time
	Until  time.Time
	Keep   *graph.Unit
	Target *graph.Unit
	Action ActionKind
	Job    string
	By     string
}

// PendingWait returns how long a finding waits in StatusPending for an
// action, when the longest Every of the enabled targets whose scopes cover
// the finding is every: two times every, and at least MinPending. every is
// zero when no such target sweeps on a schedule. At Pending.Until the
// schedule applies EventPendingExpired, and the finding opens again, so an
// action whose job or whose sweep never came does not hide the finding for
// ever. Two times every gives the sweep that shows the result one missed
// run: a target that sweeps every 30 days needs up to 30 days to see a
// delete, and 15 days would open the finding again before it.
func PendingWait(every time.Duration) time.Duration {
	return max(MinPending, pendingRuns*every)
}

// Finding is one unmanaged resource in one namespace. A namespace has at most
// one finding for each resource key. Units are the holders of the key that
// the finding names, in the order of Unit.String: the two or more live units
// of a resource managed twice, or the deleted units of a resource left
// behind. Elsewhere is true when a unit of a linked namespace also holds the
// key; the finding does not name it. Scope is the account scope of the
// resource: the scope of its key, or, for a key with no account scope of its
// own, the scope of the sweep that saw it (RecordScope). It is empty until a
// sweep sees such a key, and a finding with no scope never resolves by
// ResolveUnseen. Region is the region of the record, empty for a global
// resource.
type Finding struct {
	FirstSeen time.Time
	LastSeen  time.Time
	Ignore    *Ignore
	Pending   *Pending
	Tags      map[string]string
	Key       ResourceKey
	ID        string
	Namespace namespace.Name
	Kind      Kind
	Status    Status
	Evidence  Evidence
	Type      string
	Scope     string
	Region    string
	Name      string
	Scan      string
	Units     []graph.Unit
	Elsewhere bool
}

// CheckReason reports whether s is a valid reason: 1 to MaxReasonBytes of
// text with no control character. It returns ErrInvalidReason.
func CheckReason(s string) error {
	if strings.TrimSpace(s) == "" || len(s) > MaxReasonBytes || !utf8.ValidString(s) ||
		strings.ContainsFunc(s, unicode.IsControl) {
		return fmt.Errorf("%w: 1 to %d characters of text", ErrInvalidReason, MaxReasonBytes)
	}

	return nil
}

// CheckIgnore reports whether i is a valid ignore at now: a valid reason and
// an end in the future within MaxIgnore, or no end. It returns
// ErrInvalidReason.
func CheckIgnore(i Ignore, now time.Time) error {
	if err := CheckReason(i.Reason); err != nil {
		return err
	}

	if !i.Until.IsZero() && (!i.Until.After(now) || i.Until.After(now.Add(MaxIgnore))) {
		return fmt.Errorf("%w: the end of an ignore is in the next %d days", ErrInvalidReason,
			int(MaxIgnore/day))
	}

	return nil
}

// IgnoreRule ignores each finding that it matches, with its reason. An empty
// selector field matches any value. Type matches the type of the finding
// exactly, or as a prefix when it ends with "*". Kinds empty matches every
// kind.
type IgnoreRule struct {
	Until     time.Time
	ID        string
	Namespace namespace.Name
	Provider  string
	Scope     string
	Type      string
	TagKey    string
	TagValue  string
	Reason    string
	By        string
	Kinds     []Kind
}

// Validate reports whether r is a valid rule at now. A rule must select by
// at least a provider, so that no rule ignores every finding. It returns
// ErrInvalidRule and ErrInvalidReason.
func (r IgnoreRule) Validate(now time.Time) error {
	if !validProvider(r.Provider) {
		return fmt.Errorf("%w: a rule names a provider", ErrInvalidRule)
	}

	if r.TagValue != "" && r.TagKey == "" {
		return fmt.Errorf("%w: a tag value needs a tag key", ErrInvalidRule)
	}

	if strings.Contains(strings.TrimSuffix(r.Type, "*"), "*") {
		return fmt.Errorf("%w: a type pattern has one * at its end", ErrInvalidRule)
	}

	for _, k := range r.Kinds {
		if !k.Valid() {
			return fmt.Errorf("%w: kind %q", ErrInvalidRule, k)
		}
	}

	return CheckIgnore(Ignore{Reason: r.Reason, Until: r.Until, By: "", Rule: ""}, now)
}

// Matches reports whether r ignores f at now. A rule past its end matches
// nothing.
func (r IgnoreRule) Matches(f Finding, now time.Time) bool {
	if !r.Until.IsZero() && !now.Before(r.Until) {
		return false
	}

	if r.Provider != f.Key.Provider || (r.Scope != "" && r.Scope != f.Scope) {
		return false
	}

	if len(r.Kinds) > 0 && !containsKind(r.Kinds, f.Kind) {
		return false
	}

	return r.matchesType(f.Type) && r.matchesTags(f.Tags)
}

// matchesType reports whether the type selector of r matches typ.
func (r IgnoreRule) matchesType(typ string) bool {
	if prefix, ok := strings.CutSuffix(r.Type, "*"); ok {
		return strings.HasPrefix(typ, prefix)
	}

	return r.Type == "" || r.Type == typ
}

// matchesTags reports whether the tag selector of r matches tags.
func (r IgnoreRule) matchesTags(tags map[string]string) bool {
	if r.TagKey == "" {
		return true
	}

	v, ok := tags[r.TagKey]

	return ok && (r.TagValue == "" || v == r.TagValue)
}

func containsKind(ks []Kind, k Kind) bool {
	return slices.Contains(ks, k)
}
