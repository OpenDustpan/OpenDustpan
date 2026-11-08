package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// JobFact is what the job token rule reads of the job of a token at each
// check.
type JobFact struct {
	// Unit is the unit of the job.
	Unit Object
	// Changes reports whether the job can change the unit.
	Changes bool
	// Source is the source kind of the job.
	Source string
}

// JobFacts reads the facts of a job by its ID.
type JobFacts interface {
	JobFact(ctx context.Context, jobID string) (JobFact, error)
}

// JobTokenRefusal is what the rule records of a refusal: the subject and the
// job of the token, the unit of the token, the action and a reason code.
type JobTokenRefusal struct {
	Subject string
	JobID   string
	Unit    Object
	Action  Action
	Reason  string
}

// JobTokenRefusals records a refusal of the job token rule.
type JobTokenRefusals interface {
	RecordJobTokenRefusal(ctx context.Context, r JobTokenRefusal) error
}

// ErrJobTokenAction reports an action that a job token may not ask for. It
// wraps ErrDenied. The text names the action.
var ErrJobTokenAction error = jobTokenActionError{action: ""}

// ErrSecretsWithheld reports a read of secret values that a job token of this
// job may not do. It wraps ErrDenied.
var ErrSecretsWithheld = fmt.Errorf("%w: a job token may not read secret values", ErrDenied)

// errJobTokenUpdate reports an update that saves state that a job token may
// not start. It wraps ErrJobTokenAction.
var errJobTokenUpdate = fmt.Errorf("%w: a job token may not start an update that saves state", ErrJobTokenAction)

// errJobTokenCancel reports the cancel of the update of another job, which a
// job token may not do. It wraps ErrJobTokenAction.
var errJobTokenCancel = fmt.Errorf("%w: a job token may not cancel the update of another job", ErrJobTokenAction)

// errJobTokenStart reports the start of the update of another job, which a
// job token may not do. It wraps ErrJobTokenAction.
var errJobTokenStart = fmt.Errorf("%w: a job token may not start the update of another job", ErrJobTokenAction)

// errJobTokenTags reports an edit of the tags of a unit, which a job token may
// not do. It wraps ErrJobTokenAction.
var errJobTokenTags = fmt.Errorf("%w: a job token may not edit the tags of a unit", ErrJobTokenAction)

// jobTokenActionError is the refusal of an action outside the allowlist of a
// job token.
type jobTokenActionError struct {
	action Action
}

// Error implements error.
func (e jobTokenActionError) Error() string {
	if e.action == "" {
		return fmt.Sprintf("%s: a job token may not ask for this action", ErrDenied)
	}

	return fmt.Sprintf("%s: a job token may not %s", ErrDenied, e.action)
}

// Is reports that the error is an ErrJobTokenAction.
func (e jobTokenActionError) Is(target error) bool {
	_, ok := target.(jobTokenActionError)

	return ok
}

// Unwrap returns ErrDenied.
func (e jobTokenActionError) Unwrap() error { return ErrDenied }

// The reason codes that the rule records with a refusal.
const (
	jobTokenRefusalDenied  = "access.denied"
	jobTokenRefusalAction  = "access.job-token-action"
	jobTokenRefusalSecrets = "access.secrets-withheld"
)

// errNoJobFacts reports a rule with no job facts port.
var errNoJobFacts = errors.New("job token rule has no job facts")

// IsJobToken reports whether p is a consistent job token: the credential is
// a job token, the job ID is not empty, and the job unit is a unit reference.
func (p Principal) IsJobToken() bool {
	return p.Credential == CredentialJobToken && p.JobID != "" && p.JobUnit.IsUnit()
}

// CarriesJobToken reports whether p carries any part of a job token: the
// credential, a job ID or a job unit. A route that must refuse every such
// principal asks this before it reads the unit of the request.
func (p Principal) CarriesJobToken() bool {
	var zero Object

	return p.Credential == CredentialJobToken || p.JobID != "" || p.JobUnit != zero
}

// JobTokenRule limits a job token to the unit, the job and the actions of the
// run. It decides every check of a principal that carries any part of a job
// token and asks Next for every other principal.
//
// A consistent job token may act only on its own unit, and only with the
// grants of the starter: the rule clears the administrator flags before it
// asks Next. ActionJobWriteCost is the one exception: only a consistent job
// token holds it, on its unit, and Next is never asked. Every other action
// that the token asks for needs a grant, so the token adds no right of its
// own.
//
// The rule reads the job of the token at each check. A job can end or change,
// and the token must follow the live job, not a copy in the token. It denies
// when the job cannot be read, when the job holds another unit, when the job
// cannot change the unit and the action encrypts, and when the job cannot read
// secrets and the action decrypts or reads them.
type JobTokenRule struct {
	Next     Authorizer
	Jobs     JobFacts
	Refusals JobTokenRefusals
}

var _ Authorizer = JobTokenRule{Next: nil, Jobs: nil, Refusals: nil}

// jobTokenAllowsAction reports whether a consistent job token may ask for
// action. The list is explicit, so a new action is refused until someone adds
// it.
func jobTokenAllowsAction(action Action) bool {
	switch string(action) {
	case string(ActionUnitRead), string(ActionUnitWrite), string(ActionUnitReadOutputs),
		string(ActionUnitEncrypt), string(ActionUnitDecrypt), string(ActionUnitReadSecrets),
		string(ActionJobWriteCost):
		return true
	default:
		return false
	}
}

// jobTokenSourceAllowsSecrets reports whether a job of source may read
// secrets. The list is explicit, so a new source is refused until someone adds
// it.
func jobTokenSourceAllowsSecrets(source string) bool {
	switch source {
	case "api", "agent", "self-service", "schedule":
		return true
	default:
		return false
	}
}

// Authorize implements Authorizer.
func (r JobTokenRule) Authorize(ctx context.Context, p Principal, action Action, obj Object) error {
	if !p.CarriesJobToken() {
		if action == ActionJobWriteCost {
			return &DeniedError{Subject: p.Subject, Action: action, Object: obj}
		}

		return r.Next.Authorize(ctx, p, action, obj)
	}

	denied := &DeniedError{Subject: p.Subject, Action: action, Object: obj}

	if !p.IsJobToken() || !sameUnitObject(obj, p.JobUnit) {
		return r.refuse(ctx, p, action, jobTokenRefusalDenied, denied)
	}

	if !jobTokenAllowsAction(action) {
		return r.refuse(ctx, p, action, jobTokenRefusalAction, jobTokenActionError{action: action})
	}

	fact, err := r.jobFact(ctx, p)
	if err != nil || !jobTokenFactHoldsUnit(fact, p.JobUnit) {
		return r.refuse(ctx, p, action, jobTokenRefusalDenied, denied)
	}

	if reason := r.jobTokenLimit(ctx, p, fact, action, obj); reason != "" {
		if reason == jobTokenRefusalSecrets {
			return r.refuse(ctx, p, action, reason, ErrSecretsWithheld)
		}

		return r.refuse(ctx, p, action, reason, denied)
	}

	if action == ActionJobWriteCost {
		return nil
	}

	return r.Next.Authorize(ctx, withoutElevation(p), action, obj)
}

// withoutElevation returns p with no administrator flag and no temporary
// elevation.
//
// Defense in depth: jobtoken.Service.Authenticate clears the flags, and the
// temporary administrator decorator passes a job token through unchanged.
// This clear stays so that no job token reaches an authorizer with a flag,
// also when a new authenticator builds the principal.
func withoutElevation(p Principal) Principal {
	p.Admin = false
	p.TemporaryAdministrator = false
	p.TemporaryExpiresAt = time.Time{}

	return p
}

// sameUnitObject reports whether obj names the unit unit. When both
// references carry a stable unit ID, the IDs must be equal too.
func sameUnitObject(obj, unit Object) bool {
	if !obj.IsUnit() || !unit.IsUnit() {
		return false
	}

	if obj.Namespace() != unit.Namespace() || obj.Tool() != unit.Tool() ||
		obj.UnitName() != unit.UnitName() {
		return false
	}

	// Defense in depth: a unit name can name another unit after a delete and
	// a recreate. The authorizer of grants checks the stable unit ID too.
	// This check stays so that the rule alone keeps a token to the unit of
	// its job.
	if obj.UnitID() != "" && unit.UnitID() != "" && obj.UnitID() != unit.UnitID() {
		return false
	}

	return true
}

// ListObjects implements Authorizer. A consistent job token lists only its
// own unit, and only when the scope of Next names that unit, its namespace
// or every object. An inconsistent job token lists nothing.
func (r JobTokenRule) ListObjects(ctx context.Context, p Principal, action Action) (Scope, error) {
	if p.CarriesJobToken() {
		return r.listJobToken(ctx, p, action)
	}

	if action == ActionJobWriteCost {
		return emptyScope(), nil
	}

	scope, err := r.Next.ListObjects(ctx, p, action)
	if err != nil {
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	return scope, nil
}

// emptyScope returns the scope that names no object.
func emptyScope() Scope {
	return Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}
}

// unitScope returns the scope that names only unit.
func unitScope(unit Object) Scope {
	return Scope{Namespaces: nil, Collections: nil, Units: []Object{unit}, Principals: nil, All: false}
}

// scopeHasNamespace reports whether scope names the namespace ns.
func scopeHasNamespace(scope []Object, ns namespace.Name) bool {
	for i := range scope {
		if scope[i].IsNamespace() && scope[i].Namespace() == ns {
			return true
		}
	}

	return false
}

// scopeHasUnit reports whether scope names the unit unit.
func scopeHasUnit(scope []Object, unit Object) bool {
	for i := range scope {
		if sameUnitObject(scope[i], unit) {
			return true
		}
	}

	return false
}

// ScopeExact reports that the scope is exact when the scope of Next is exact.
func (r JobTokenRule) ScopeExact() bool {
	return HasExactScope(r.Next)
}

// jobTokenFactHoldsUnit reports whether fact holds the unit of the token in
// both directions of the comparison.
func jobTokenFactHoldsUnit(fact JobFact, unit Object) bool {
	return sameUnitObject(fact.Unit, unit) && sameUnitObject(unit, fact.Unit)
}

// jobFact reads the facts of the job of p at this check.
func (r JobTokenRule) jobFact(ctx context.Context, p Principal) (JobFact, error) {
	if r.Jobs == nil {
		return JobFact{}, errNoJobFacts
	}

	fact, err := r.Jobs.JobFact(ctx, p.JobID)
	if err != nil {
		return fact, fmt.Errorf("read the job of the token: %w", err)
	}

	return fact, nil
}

// jobFactForList reads the facts of the job of p for a list. ok is false when
// the rule cannot read the job: the list is then empty, not an error.
func (r JobTokenRule) jobFactForList(ctx context.Context, p Principal) (JobFact, bool) {
	fact, err := r.jobFact(ctx, p)

	return fact, err == nil
}

// jobTokenLimit returns the reason of a limit that refuses action for fact, or
// the empty string when the fact allows the action.
func (r JobTokenRule) jobTokenLimit(ctx context.Context, p Principal, fact JobFact, action Action, unit Object) string {
	if action == ActionUnitEncrypt && !fact.Changes {
		return jobTokenRefusalDenied
	}

	if action != ActionUnitDecrypt && action != ActionUnitReadSecrets {
		return ""
	}

	if !jobTokenSourceAllowsSecrets(fact.Source) || !r.nextAllowsSecrets(ctx, p, unit) {
		return jobTokenRefusalSecrets
	}

	return ""
}

// nextAllowsSecrets reports whether Next allows the starter to read the
// secrets of unit.
func (r JobTokenRule) nextAllowsSecrets(ctx context.Context, p Principal, unit Object) bool {
	return r.Next.Authorize(ctx, withoutElevation(p), ActionUnitReadSecrets, unit) == nil
}

// refuse records the refusal and returns denial. It denies also when the rule
// has no recorder or the record fails, and then the error wraps the denial and
// the other error.
func (r JobTokenRule) refuse(ctx context.Context, p Principal, action Action, reason string, denial error) error {
	if r.Refusals == nil {
		return fmt.Errorf("%w: the job token rule has no refusal recorder", denial)
	}

	err := r.Refusals.RecordJobTokenRefusal(ctx, JobTokenRefusal{
		Subject: p.Subject, JobID: p.JobID, Unit: p.JobUnit, Action: action, Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("%w: record the refusal: %w", denial, err)
	}

	return denial
}

// JobTokenUpdate is the update that a job token asks to create or start: the
// principal, the unit of the route and whether the update changes state.
type JobTokenUpdate struct {
	Principal Principal
	Unit      Object
	Mutates   bool
}

// CheckJobTokenUpdate refuses a principal that carries any part of a job token
// when the update may not save state for the job of the token.
//
// A principal that carries no part of a job token passes without a read. A
// consistent job token passes when the live job holds the unit, and either the
// update does not change state or the job can change it. Every other answer
// refuses and records one entry through refusals; a failed record still
// refuses.
func CheckJobTokenUpdate(
	ctx context.Context, facts JobFacts, refusals JobTokenRefusals, update JobTokenUpdate,
) error {
	p := update.Principal

	if !p.CarriesJobToken() {
		return nil
	}

	if !p.IsJobToken() || facts == nil {
		return refuseJobTokenUpdate(ctx, refusals, p)
	}

	// Defense in depth: the authorizer of the route keeps a job token to its
	// own unit too. This check stays so that the guard alone refuses an update
	// of another unit.
	if !sameUnitObject(update.Unit, p.JobUnit) || !sameUnitObject(p.JobUnit, update.Unit) {
		return refuseJobTokenUpdate(ctx, refusals, p)
	}

	fact, err := facts.JobFact(ctx, p.JobID)
	if err != nil || !jobTokenFactHoldsUnit(fact, update.Unit) || (update.Mutates && !fact.Changes) {
		return refuseJobTokenUpdate(ctx, refusals, p)
	}

	return nil
}

// JobTokenRefusalText returns the text of a job token refusal that an
// adapter may send to the client. It names the update or the action, and
// never the text of a wrapped error, such as a failed record of the refusal.
func JobTokenRefusalText(err error) string {
	switch {
	case errors.Is(err, errJobTokenUpdate):
		return "a job token may not start an update that saves state"
	case errors.Is(err, errJobTokenCancel):
		return "a job token may not cancel the update of another job"
	case errors.Is(err, errJobTokenStart):
		return "a job token may not start the update of another job"
	case errors.Is(err, errJobTokenTags):
		return "a job token may not edit the tags of a unit"
	}

	var actionErr jobTokenActionError
	if errors.As(err, &actionErr) && actionErr.action != "" {
		return fmt.Sprintf("a job token may not %s", actionErr.action)
	}

	return "a job token may not ask for this action"
}

// CheckJobTokenCancel refuses a principal that carries any part of a job token
// when updateJobID is not the job of the token. A principal that carries no
// part of a job token passes. A consistent job token passes when the update
// records its own job ID, which must not be empty. Every other answer refuses
// and records one entry through refusals; a failed record still refuses.
func CheckJobTokenCancel(
	ctx context.Context, refusals JobTokenRefusals, p Principal, updateJobID string,
) error {
	if !p.CarriesJobToken() {
		return nil
	}

	// Defense in depth: IsJobToken already needs a job ID, so an empty
	// updateJobID never matches. The empty check stays so that a change to
	// IsJobToken cannot let a job token cancel an update that no job created.
	if p.IsJobToken() && updateJobID != "" && updateJobID == p.JobID {
		return nil
	}

	return refuseJobTokenAction(ctx, refusals, p, errJobTokenCancel)
}

// CheckJobTokenStart refuses a principal that carries any part of a job token
// when updateJobID is not the job of the token. A principal that carries no
// part of a job token passes. A consistent job token passes when the update
// records its own job ID, which must not be empty. Every other answer refuses
// and records one entry through refusals; a failed record still refuses.
func CheckJobTokenStart(
	ctx context.Context, refusals JobTokenRefusals, p Principal, updateJobID string,
) error {
	if !p.CarriesJobToken() {
		return nil
	}

	// Defense in depth: IsJobToken already needs a job ID, so an empty
	// updateJobID never matches. The empty check stays so that a change to
	// IsJobToken cannot let a job token start an update that no job created.
	if p.IsJobToken() && updateJobID != "" && updateJobID == p.JobID {
		return nil
	}

	return refuseJobTokenAction(ctx, refusals, p, errJobTokenStart)
}

// CheckJobTokenTagEdit refuses a principal that carries any part of a job
// token: a job token may not edit the tags of a unit. It records one entry
// through refusals, and a failed record still refuses.
func CheckJobTokenTagEdit(ctx context.Context, refusals JobTokenRefusals, p Principal) error {
	if !p.CarriesJobToken() {
		return nil
	}

	return refuseJobTokenAction(ctx, refusals, p, errJobTokenTags)
}

// refuseJobTokenUpdate records the refusal of the update guard and returns the
// denial. A missing recorder or a failed record still refuses.
func refuseJobTokenUpdate(ctx context.Context, refusals JobTokenRefusals, p Principal) error {
	return refuseJobTokenAction(ctx, refusals, p, errJobTokenUpdate)
}

// refuseJobTokenAction records the refusal of an action that a job token may
// not ask for, and returns denial. A missing recorder or a failed record still
// refuses.
func refuseJobTokenAction(
	ctx context.Context, refusals JobTokenRefusals, p Principal, denial error,
) error {
	if refusals == nil {
		return fmt.Errorf("%w: the job token rule has no refusal recorder", denial)
	}

	err := refusals.RecordJobTokenRefusal(ctx, JobTokenRefusal{
		Subject: p.Subject, JobID: p.JobID, Unit: p.JobUnit, Action: ActionUnitWrite,
		Reason: jobTokenRefusalAction,
	})
	if err != nil {
		return fmt.Errorf("%w: record the refusal: %w", denial, err)
	}

	return denial
}

// listJobToken returns the scope of the list of a principal that carries a
// job token: its unit when Next names that unit, its namespace or every
// object, and nothing else. It returns the empty scope for an action that the
// token may not ask for, and when the live job does not hold the unit.
func (r JobTokenRule) listJobToken(ctx context.Context, p Principal, action Action) (Scope, error) {
	if !p.IsJobToken() || !jobTokenAllowsAction(action) {
		return emptyScope(), nil
	}

	fact, ok := r.jobFactForList(ctx, p)
	if !ok || !jobTokenFactHoldsUnit(fact, p.JobUnit) {
		return emptyScope(), nil
	}

	if action == ActionJobWriteCost {
		return unitScope(p.JobUnit), nil
	}

	if r.jobTokenLimit(ctx, p, fact, action, p.JobUnit) != "" {
		return emptyScope(), nil
	}

	scope, err := r.Next.ListObjects(ctx, withoutElevation(p), action)
	if err != nil {
		return emptyScope(), fmt.Errorf("list objects: %w", err)
	}

	if scope.All || scopeHasNamespace(scope.Namespaces, p.JobUnit.Namespace()) ||
		scopeHasUnit(scope.Units, p.JobUnit) {
		return unitScope(p.JobUnit), nil
	}

	return emptyScope(), nil
}

// AuthorizeJobClaim checks that a push for the phases of jobID carries the
// job token of that job. It returns nil only when p is a consistent job token
// and its job ID is jobID, which must not be empty. Every other answer is an
// error that wraps ErrDenied and names no job ID.
func AuthorizeJobClaim(p Principal, jobID string) error {
	if jobID != "" && p.IsJobToken() && p.JobID == jobID {
		return nil
	}

	return fmt.Errorf("%w: the job token does not claim this job", ErrDenied)
}
