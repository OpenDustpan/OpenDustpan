package job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// missingPort reports whether cfg lacks a required port.
func (c *ServiceConfig) missingPort() bool {
	return c.Store == nil || c.Units == nil || c.Planner == nil || c.Decider == nil || c.Authorizer == nil ||
		c.Gates == nil || c.Fences == nil || c.Log == nil || len(c.Executors) == 0
}

// indexExecutors returns the executors by kind. It returns ErrInvalid for a
// nil executor, an unknown kind and two executors of one kind.
func indexExecutors(list []Executor) (map[ExecutorKind]Executor, error) {
	executors := make(map[ExecutorKind]Executor, len(list))

	for _, e := range list {
		if e == nil || !e.Kind().Valid() || executors[e.Kind()] != nil {
			return nil, fmt.Errorf("%w: the job service needs one executor for each kind", ErrInvalid)
		}

		executors[e.Kind()] = e
	}

	return executors, nil
}

// checkRequest returns the source kind of the start and the error of a
// request that is not valid. It changes nothing in req.
func checkRequest(a Actor, req *StartRequest) (SourceKind, error) {
	if a.Principal.Subject == "" {
		return "", fmt.Errorf("%w: a start needs a principal", ErrInvalid)
	}

	// A job runs with the job token of its phase. A job that could start a job
	// would let any step widen what it runs, and loop. Defense in depth: the
	// grants of a job token hold no start action. This check stays so that a
	// role with too many actions never lets a job token start a job.
	if a.Principal.Credential == identity.CredentialJobToken {
		// The refusal is not a missing permission, so it is a policy refusal
		// of the job and not a denial.
		return "", fmt.Errorf("%w: a job token cannot start a job", ErrPolicyDenied)
	}

	err := checkKinds(req)
	if err != nil {
		return "", err
	}

	err = checkText(req)
	if err != nil {
		return "", err
	}

	err = checkBreakGlass(a, req.BreakGlass)
	if err != nil {
		return "", err
	}

	source, err := sourceOf(a, req.Source)
	if err != nil {
		return "", err
	}

	err = checkTrigger(a, source, req.Trigger)
	if err != nil {
		return "", err
	}

	return source, nil
}

// checkKinds checks the operation, the executor and the purpose of req, and that
// a drift check is a refresh.
func checkKinds(req *StartRequest) error {
	if !req.Operation.Valid() {
		return fmt.Errorf("%w: unknown operation %q", ErrInvalid, req.Operation)
	}

	if req.Executor != "" && !req.Executor.Valid() {
		return fmt.Errorf("%w: unknown executor %q", ErrInvalid, req.Executor)
	}

	if !req.Purpose.Valid() {
		return fmt.Errorf("%w: unknown purpose %q", ErrInvalid, req.Purpose)
	}

	if req.Purpose == PurposeDriftCheck && req.Operation != OperationRefresh {
		return fmt.Errorf("%w: a %s has the operation %s, not %s", ErrInvalid, req.Purpose, OperationRefresh,
			req.Operation)
	}

	return nil
}

// checkText checks the idempotency key and the reason of req.
func checkText(req *StartRequest) error {
	err := checkKey(req.IdempotencyKey)
	if err != nil {
		return err
	}

	err = audit.ValidateReason(req.Reason)
	if err != nil {
		return fmt.Errorf("%w: reason: %w", ErrInvalid, err)
	}

	return nil
}

// sourceOf returns the source kind of a start: the kind that the caller
// gave, or the default for the actor. The start of an agent has the source
// kind agent: it returns ErrInvalid when the caller gives another kind for an
// agent, so that a policy never sees the start of an agent as a start of a
// person, a schedule or a feature of the server.
func sourceOf(a Actor, given SourceKind) (SourceKind, error) {
	switch {
	case given == "" && a.Agent != "":
		return SourceAgent, nil
	case given == "":
		return SourceAPI, nil
	case !given.Valid():
		return "", fmt.Errorf("%w: %q is not a source kind", ErrInvalid, given)
	case a.Agent != "" && given != SourceAgent:
		return "", fmt.Errorf("%w: the start of an agent has the source kind %s, not %s", ErrInvalid, SourceAgent,
			given)
	default:
		return given, nil
	}
}

// checkBreakGlass checks a request for break-glass. An agent cannot ask for
// it, and it needs a reason.
func checkBreakGlass(a Actor, bg *BreakGlass) error {
	if bg == nil {
		return nil
	}

	if a.Agent != "" {
		return fmt.Errorf("%w: an agent cannot ask for break-glass", ErrAgentDenied)
	}

	if bg.Reason == "" {
		return fmt.Errorf("%w: a break-glass needs a reason", ErrInvalid)
	}

	err := audit.ValidateReason(bg.Reason)
	if err != nil {
		return fmt.Errorf("%w: break-glass reason: %w", ErrInvalid, err)
	}

	return nil
}

// checkKey returns ErrInvalid for an idempotency key that is longer than
// MaxIdempotencyKey bytes or holds a byte that is not printable ASCII. An
// empty key is valid.
func checkKey(key string) error {
	if len(key) > MaxIdempotencyKey {
		return fmt.Errorf("%w: an idempotency key has at most %d characters", ErrInvalid, MaxIdempotencyKey)
	}

	for i := range len(key) {
		if key[i] < ' ' || key[i] > '~' {
			return fmt.Errorf("%w: an idempotency key has printable ASCII characters only", ErrInvalid)
		}
	}

	return nil
}

// errNoUnit is the one text for a unit that does not exist and a unit that
// the caller may not start a job on, so that the answer is no oracle for the
// names of units.
const errNoUnit = "the unit does not exist, or you cannot start a job on it"

// unitOf finds the unit. A unit that does not exist gives an error that wraps
// identity.ErrDenied with the text that a refusal of the actions has, so that
// the answer is no oracle for the names of units.
func (s *service) unitOf(ctx context.Context, ref identity.Object) (identity.Object, error) {
	if !ref.IsUnit() {
		return identity.Object{}, fmt.Errorf("%w: the object of a job must be a unit", ErrInvalid)
	}

	// Defense in depth: the handler of the API checks the name of the unit
	// too. This check stays so that a feature of the server never puts a name
	// with a control character in the audit entry of a job, or in a lookup.
	if !utf8.ValidString(ref.UnitName()) || strings.ContainsFunc(ref.UnitName(), audit.IsHiddenRune) {
		return identity.Object{}, fmt.Errorf("%w: the name of the unit has a character that it cannot have",
			ErrInvalid)
	}

	unit, err := s.units.Unit(ctx, ref)

	switch {
	case errors.Is(err, ErrNotFound):
		return identity.Object{}, fmt.Errorf("%w: %s", identity.ErrDenied, errNoUnit)
	case err != nil:
		return identity.Object{}, fmt.Errorf("find unit: %w", err)
	default:
	}

	// Defense in depth: the resolver and the planner check the unit too.
	// This check stays so that a new resolver cannot give the service a unit
	// of another namespace or a unit with no stable ID.
	if !unit.IsUnit() || unit.UnitID() == "" || unit.Namespace() != ref.Namespace() || unit.Tool() != ref.Tool() {
		return identity.Object{}, fmt.Errorf("%w: the unit resolver gave another unit than %s", ErrInvalid,
			ref.String())
	}

	return unit, nil
}

// preAuthorize returns nil when the principal of a holds the start action of
// one executor that can do the operation of req, and a *DeniedError for the
// last action that the authorizer refused when it holds none. A request that
// names an executor is checked for that executor alone.
func (s *service) preAuthorize(ctx context.Context, a Actor, unit identity.Object, req *StartRequest) error {
	kinds := []ExecutorKind{ExecutorRunner, ExecutorWriteBack}
	if req.Executor != "" {
		kinds = []ExecutorKind{req.Executor}
	}

	var denied error

	for _, k := range kinds {
		action, err := startActionFor(k, req.Operation, req.Purpose)
		if err != nil {
			if len(kinds) == 1 {
				return err
			}

			continue
		}

		err = s.authorizer.Authorize(ctx, a.Principal, action, unit)

		switch {
		case err == nil:
			return nil
		case errors.Is(err, identity.ErrDenied):
			denied = &DeniedError{Action: action, Object: unit, Err: err}
		default:
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	if denied == nil {
		return fmt.Errorf("%w: no executor does %s", ErrUnsupported, req.Operation)
	}

	return denied
}

// authorizeFields checks the extra actions of the pool and the location of
// req (see RequestRequirements). It runs before the planner: a caller without
// them learns nothing about the pools or the code of the unit from the errors
// of the planner.
func (s *service) authorizeFields(ctx context.Context, a Actor, unit identity.Object, req *StartRequest) error {
	return s.authorizeAll(ctx, a, RequestRequirements(req, unit))
}

// authorize checks the start actions of spec for the principal of a, and
// returns a *DeniedError for the first one that the authorizer refuses. It
// fails closed: an error of the authorizer refuses.
func (s *service) authorize(ctx context.Context, a Actor, spec *Spec, req *StartRequest) error {
	reqs, err := StartRequirements(*spec, req.BreakGlass)
	if err != nil {
		return err
	}

	// Defense in depth: authorizeFields checked the actions of the pool and
	// the location before the planner ran. This check stays so that the
	// actions hold for the spec whatever the order of the calls.
	reqs = append(reqs, RequestRequirements(req, spec.Unit)...)

	return s.authorizeAll(ctx, a, reqs)
}

// authorizeAll asks the authorizer for each requirement, in order, and
// returns a *DeniedError for the first one that the authorizer refuses.
func (s *service) authorizeAll(ctx context.Context, a Actor, reqs []Requirement) error {
	for i := range reqs {
		r := &reqs[i]

		err := s.authorizer.Authorize(ctx, a.Principal, r.Action, r.Object)

		switch {
		case err == nil:
		case errors.Is(err, identity.ErrDenied):
			return &DeniedError{Action: r.Action, Object: r.Object, Err: err}
		default:
			return fmt.Errorf("authorize %s: %w", r.Action, err)
		}
	}

	return nil
}

// checkGates returns an error that wraps ErrGateEnforced when an enforced
// gate at change.propose or state.write covers unit. Defense in depth: the
// gate service refuses the writes of state with these gates too. This check
// stays so that a job never starts on a unit whose gate nobody evaluates at
// the start, and it fails closed when the guard cannot answer.
func (s *service) checkGates(ctx context.Context, unit identity.Object) error {
	enforced, err := s.gates.Enforced(ctx, unit)
	if err != nil {
		// The text of the cause may hold detail of a driver. The log keeps it
		// and the caller reads a fixed text.
		s.log.ErrorContext(ctx, "read the gates of a unit", slog.String("unit", unit.String()),
			slog.String("error", err.Error()))

		return fmt.Errorf("%w: the gates of the unit cannot be read, so the start is refused", ErrGateEnforced)
	}

	if len(enforced) > 0 {
		return &GateError{Gates: enforced}
	}

	return nil
}

// decided is what a start decided before the job is stored.
type decided struct {
	source SourceKind
	req    *StartRequest
	spec   Spec
	kinds  []PhaseKind
	gates  [][]Gate
	bg     *BreakGlass
}

// create decides, builds the job and stores it.
func (s *service) create(
	ctx context.Context, a Actor, source SourceKind, req *StartRequest, spec Spec,
) (Job, bool, error) {
	exec, ok := s.executors[spec.Executor]
	if !ok {
		return Job{}, false, fmt.Errorf("%w: no executor of kind %q is installed", ErrUnsupported, spec.Executor)
	}

	kinds, err := exec.Phases(ctx, spec)
	if err != nil {
		return Job{}, false, fmt.Errorf("phases: %w", err)
	}

	confirm, err := s.checkAgent(ctx, a, &spec)
	if err != nil {
		return Job{}, false, err
	}

	decision, err := s.decider.Decide(ctx, DecisionInput{
		Principal: a.Principal, Agent: a.Agent, AgentMode: a.Mode, Source: source, Spec: spec, Keys: editKeys(&spec),
	})
	if err != nil {
		return Job{}, false, fmt.Errorf("policy: %w", err)
	}

	if !decision.Allow {
		return Job{}, false, fmt.Errorf("%w: %s", ErrPolicyDenied, decision.Reason)
	}

	fence, err := s.fences.Fence(ctx, spec.Unit.Namespace())
	if err != nil {
		return Job{}, false, fmt.Errorf("fence of %s: %w", spec.Unit.Namespace(), err)
	}

	bg, err := s.breakGlass(ctx, req.BreakGlass, fence.Namespace)
	if err != nil {
		return Job{}, false, err
	}

	gates := PolicyGates(kinds, decision)
	if confirm {
		gates = WithConfirmations(kinds, gates)
	}

	j := s.newJob(a, &decided{source: source, req: req, spec: spec, kinds: kinds, gates: gates, bg: bg})

	e, err := s.entry(a, req, &j)
	if err != nil {
		return Job{}, false, err
	}

	return s.persist(ctx, &j, fence, e)
}

// checkAgent applies the agent mode with the switches of the user.
func (s *service) checkAgent(ctx context.Context, a Actor, spec *Spec) (bool, error) {
	var sw DirectSwitches
	if a.Agent != "" && s.switches != nil {
		sw = s.switches(ctx, spec.Unit.Namespace(), a.Principal.Subject)
	}

	return CheckAgent(spec, a, sw)
}

// breakGlass returns the break-glass of the job: the request, with the ID of
// the freeze that it passes. It returns nil for no request. A request needs
// an active freeze, so a record of a break-glass always names what it broke.
func (s *service) breakGlass(ctx context.Context, bg *BreakGlass, ns namespace.Name) (*BreakGlass, error) {
	if bg == nil {
		return nil, nil //nolint:nilnil // No request is no break-glass and no error.
	}

	if s.freezes == nil {
		return nil, fmt.Errorf("%w: this server holds no freezes, so break-glass has nothing to pass", ErrInvalid)
	}

	now := s.now()

	freezes, err := s.freezes.ListFreezes(ctx, ns, now)
	if err != nil {
		return nil, fmt.Errorf("list freezes: %w", err)
	}

	i := slices.IndexFunc(freezes, func(f Freeze) bool { return f.ActiveAt(now) })
	if i < 0 {
		return nil, fmt.Errorf("%w: no change freeze of %s is active, so break-glass has nothing to pass",
			ErrInvalid, ns)
	}

	return &BreakGlass{Reason: bg.Reason, Freeze: freezes[i].ID}, nil
}

// editKeys returns the key paths that the edits of spec change.
func editKeys(spec *Spec) []string {
	var keys []string

	for i := range spec.Edits {
		for k := range spec.Edits[i].Keys {
			keys = append(keys, spec.Edits[i].Path+"#"+k)
		}
	}

	slices.Sort(keys)

	return keys
}

// newJob builds the job as it is stored: queued, with its phases and their
// gates.
func (s *service) newJob(a Actor, c *decided) Job {
	j := Job{
		ID: s.newID(), NamespaceID: "", Spec: c.spec, State: StateQueued, Wait: WaitNone, Reason: "",
		Phases: make([]Phase, len(c.kinds)), Current: 0, StartedBy: a.Principal.Subject, Source: c.source,
		Agent: a.Agent, AgentMode: a.Mode, BreakGlass: c.bg, Trigger: copyTrigger(c.req.Trigger), Delivery: nil,
		Closed:       nil,
		Verification: VerificationNone, IdempotencyKey: scopedKey(a.Principal.Subject, c.req.IdempotencyKey),
		RequestHash: requestHash(c.source, c.req), Version: 0, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
		StateChangedAt: time.Time{},
	}

	for i, k := range c.kinds {
		j.Phases[i].Kind = k

		if i < len(c.gates) {
			j.Phases[i].Gates = c.gates[i]
		}
	}

	return j
}

// copyTrigger returns a copy of ref, so that the job never shares memory with
// the request.
func copyTrigger(ref *TriggerRef) *TriggerRef {
	if ref == nil {
		return nil
	}

	c := *ref

	return &c
}

// scopedKey returns the key that the store keeps for the idempotency key of
// a principal: a hash of both, so that another principal with the same
// string gets another job, and the table never holds the key of a client.
func scopedKey(subject, key string) string {
	if key == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(subject + "\x00" + key))

	return "k1:" + hex.EncodeToString(sum[:])
}

// requestHash returns the hash of the whole request of a start, or "" for a
// request with no idempotency key. The store compares it when a key names a
// job that exists: the same key with another reason, another pool name,
// another code location or another source kind is another request, and fails
// with ErrKeyConflict. The store compares the spec too. The spec alone is not
// enough, because it holds neither the reason nor the pool name as the caller
// gave it.
func requestHash(source SourceKind, req *StartRequest) string {
	if req.IdempotencyKey == "" {
		return ""
	}

	var b strings.Builder

	// Each field has its length in front, so that no two requests share the
	// bytes that are hashed.
	put := func(s string) {
		b.WriteString(strconv.Itoa(len(s)))
		b.WriteByte(':')
		b.WriteString(s)
		b.WriteByte(';')
	}

	put(string(source))
	put(req.Unit.String())
	put(string(req.Operation))
	put(string(req.Purpose))
	put(string(req.Executor))
	put(req.Pool)

	loc := &req.Location
	for _, f := range []string{loc.Connection, loc.Repository, loc.Ref, loc.Directory, loc.Revision} {
		put(f)
	}

	put(string(req.Delivery))
	put(req.Reason)

	if req.BreakGlass != nil {
		put(req.BreakGlass.Reason)
	} else {
		put("")
	}

	// A marker says whether the request has a trigger, so that the fields of
	// a trigger never read as the edits of a request without one.
	if req.Trigger != nil {
		put("trigger")
		put(req.Trigger.ID)
		put(req.Trigger.Saver)
	} else {
		put("")
	}

	// The count of the keys of each edit joins the hash, so that the keys of
	// one edit never read as the keys of another. The edits are last, so
	// these counts separate them without a count of the edits.
	for i := range req.Edits {
		put(req.Edits[i].Path)
		put(strconv.Itoa(len(req.Edits[i].Keys)))

		for _, k := range slices.Sorted(maps.Keys(req.Edits[i].Keys)) {
			put(k)
			put(string(req.Edits[i].Keys[k]))
		}
	}

	sum := sha256.Sum256([]byte(b.String()))

	return "r1:" + hex.EncodeToString(sum[:])
}

// entry returns the audit entry of the start of j: the principal, the start
// action, the unit, and the job ID, the source and the operation in its
// changes.
func (s *service) entry(a Actor, req *StartRequest, j *Job) (audit.Entry, error) {
	actions, err := StartRequirements(j.Spec, j.BreakGlass)
	if err != nil {
		return audit.Entry{}, err
	}

	kind, reason := audit.KindChange, req.Reason
	if j.BreakGlass != nil {
		kind, reason = audit.KindOverride, j.BreakGlass.Reason
	}

	channel := req.Channel
	if channel == "" {
		channel = audit.ChannelAPI
	}

	return audit.Entry{
		At: s.now().UTC(), Namespace: j.Spec.Unit.Namespace(), Subject: a.Principal.Subject, IDPSubject: "",
		TokenID: a.Principal.TokenID, Action: string(actions[0].Action), Object: j.Spec.Unit.String(), Version: "",
		ValuePath: "", ValueCount: nil, Changes: startChanges(a, j), SourceIP: req.SourceIP,
		UserAgent: req.UserAgent, Channel: channel, Kind: kind, Outcome: audit.OutcomeSuccess,
		RequestID: req.RequestID, ObjectID: j.Spec.Unit.UnitID(), PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: string(a.Principal.Kind), JobID: "", Reason: reason,
	}, nil
}

// startChanges returns the changes of the audit entry of a start.
func startChanges(a Actor, j *Job) []audit.Change {
	changes := []audit.Change{
		{Key: "job", Old: nil, New: string(j.ID), Layer: "", TakesEffect: ""},
		{Key: "source", Old: nil, New: string(j.Source), Layer: "", TakesEffect: ""},
		{Key: "operation", Old: nil, New: string(j.Spec.Operation), Layer: "", TakesEffect: ""},
	}

	if j.Spec.Purpose != PurposeNone {
		changes = append(changes, audit.Change{
			Key: "purpose", Old: nil, New: string(j.Spec.Purpose), Layer: "", TakesEffect: "",
		})
	}

	if a.Agent != "" {
		changes = append(changes, audit.Change{Key: "agent", Old: nil, New: a.Agent, Layer: "", TakesEffect: ""})
	}

	if j.BreakGlass != nil {
		changes = append(changes, audit.Change{
			Key: "freeze", Old: nil, New: j.BreakGlass.Freeze, Layer: "", TakesEffect: "",
		})
	}

	if j.Trigger != nil {
		changes = append(changes,
			audit.Change{Key: "trigger", Old: nil, New: j.Trigger.ID, Layer: "", TakesEffect: ""},
			audit.Change{Key: "trigger-saver", Old: nil, New: j.Trigger.Saver, Layer: "", TakesEffect: ""},
		)
	}

	return changes
}

// persist stores the new job. A job whose first phase has no open gate
// starts dispatched. A unit that another change holds, or a manual lock,
// queues the job with the wait reason instead of failing it. A gate that
// needs approval stores the job in awaiting-approval in the same
// transaction as the create, so a failure never leaves a queued job with a
// closed gate, and a retry with the key finds the job as it should be.
func (s *service) persist(ctx context.Context, j *Job, fence namespace.Fence, e audit.Entry) (Job, bool, error) {
	entries := []audit.Entry{e}
	// No approval exists yet, so the policy counts no signature.
	closedPolicy := gate.ApproverPolicy{Production: true, IsDev: nil}
	open := len(j.Phases) > 0 && GatesStatus(j.Phases[0].Gates, nil, j.StartedBy, closedPolicy) == GateOpen

	var dispatch *durable.StartRequest
	if open {
		dispatch = dispatchOf(j)
	} else {
		j.State = StateAwaitingApproval
	}

	stored, isNew, err := s.store.Create(ctx, *j, dispatch, fence, entries)

	if wait := waitOf(dispatch, err); wait != WaitNone {
		j.Wait = wait
		stored, isNew, err = s.store.Create(ctx, *j, nil, fence, entries)
	}

	if err != nil {
		return Job{}, false, fmt.Errorf("create job: %w", err)
	}

	return stored, isNew, nil
}

// waitOf returns the wait reason for a create with a dispatch that failed
// with err: the unit is busy or a manual lock covers it. The job then queues
// with no dispatch. It returns WaitNone for any other result.
func waitOf(dispatch *durable.StartRequest, err error) WaitReason {
	switch {
	case dispatch == nil:
		return WaitNone
	case errors.Is(err, ErrUnitBusy):
		return WaitUnitBusy
	case errors.Is(err, ErrLocked):
		return WaitLocked
	default:
		return WaitNone
	}
}

// dispatchOf returns the durable start request of the current phase of j.
func dispatchOf(j *Job) *durable.StartRequest {
	kind := RunnerKind(j.Spec.Pool)
	if j.Spec.Executor == ExecutorWriteBack {
		kind = KindWriteBack
	}

	return &durable.StartRequest{Key: PhaseKey(j.ID, j.Current), Kind: kind, Version: PhaseVersion, Input: nil}
}
