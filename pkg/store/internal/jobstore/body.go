package jobstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// The JSON body of a stored job. The domain types carry no JSON names, and a
// stored body must not change when a domain field moves, so each part of the
// body is a type of this package. The columns of the row hold the state, the
// wait reason, the current phase, the key and the version. The body holds
// the other fields.

type unitBody struct {
	Tool      string `json:"tool"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UnitID    string `json:"unitID"`
}

type locationBody struct {
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Directory  string `json:"directory"`
	Revision   string `json:"revision"`
}

type outputBody struct {
	Kind string `json:"kind"`
	Path string `json:"path"`
}

type stepBody struct {
	Name             string            `json:"name"`
	Role             string            `json:"role"`
	Phase            string            `json:"phase"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env"`
	Dir              string            `json:"dir"`
	TimeoutNanos     int64             `json:"timeoutNanos"`
	AllowFailure     bool              `json:"allowFailure"`
	ChangesResources bool              `json:"changesResources"`
	Outputs          []outputBody      `json:"outputs"`
}

type editBody struct {
	Path string            `json:"path"`
	Keys map[string][]byte `json:"keys"`
}

type specBody struct {
	Unit      unitBody     `json:"unit"`
	Operation string       `json:"operation"`
	Purpose   string       `json:"purpose"`
	Executor  string       `json:"executor"`
	Location  locationBody `json:"location"`
	Steps     []stepBody   `json:"steps"`
	Pool      string       `json:"pool"`
	Profile   string       `json:"profile"`
	Edits     []editBody   `json:"edits"`
	Delivery  string       `json:"delivery"`
	// Defaulted is absent from a body that an older server wrote and from a
	// job whose request gave every value, and both decode to no list.
	Defaulted []string `json:"defaulted,omitempty"`
}

type logBody struct {
	ID         string    `json:"id"`
	ItemKey    string    `json:"itemKey"`
	Version    int       `json:"version"`
	Chunks     int       `json:"chunks"`
	SHA256     string    `json:"sha256"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"createdAt"`
	ClosedAt   time.Time `json:"closedAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	RedactedAt time.Time `json:"redactedAt"`
	ReplacedBy string    `json:"replacedBy"`
	Matches    int       `json:"matches"`
}

type stepRecordBody struct {
	Name       string    `json:"name"`
	Token      int64     `json:"token"`
	ExitCode   int       `json:"exitCode"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Log        *logBody  `json:"log"`
}

type planPartBody struct {
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Secret bool   `json:"secret"`
	// StoredSize and StoredMD5 are those of the sealed bytes in the blob store.
	StoredSize int64  `json:"storedSize"`
	StoredMD5  string `json:"storedMd5"`
}

type planBody struct {
	ID           string         `json:"id"`
	ItemKey      string         `json:"itemKey"`
	StateVersion string         `json:"stateVersion"`
	Parts        []planPartBody `json:"parts"`
	CreatedAt    time.Time      `json:"createdAt"`
	ExpiresAt    time.Time      `json:"expiresAt"`
	RedactedAt   time.Time      `json:"redactedAt"`
	// ReservePhase, ReserveOperation and ReserveToken name the phase and the
	// claim that recorded a reserve. Only the claim that made the reserve
	// fills it. A filled plan keeps no binding.
	ReservePhase     int    `json:"reservePhase"`
	ReserveOperation string `json:"reserveOperation"`
	ReserveToken     int64  `json:"reserveToken"`
}

type gateBody struct {
	Type         string   `json:"type"`
	Approvals    int      `json:"approvals"`
	Groups       []string `json:"groups"`
	GroupIDs     []string `json:"groupIds"`
	AllowSelf    bool     `json:"allowSelf"`
	TimeoutNanos int64    `json:"timeoutNanos"`
	BindPlan     bool     `json:"bindPlan"`
}

type bindingBody struct {
	Plan          string `json:"plan"`
	DisplaySHA256 string `json:"displaySha256"`
	ApplySHA256   string `json:"applySha256"`
	StateVersion  string `json:"stateVersion"`
}

type approvalBody struct {
	Gate    string       `json:"gate"`
	Subject string       `json:"subject"`
	Agent   string       `json:"agent"`
	Verdict string       `json:"verdict"`
	Comment string       `json:"comment"`
	Plan    *bindingBody `json:"plan"`
	At      time.Time    `json:"at"`
	Auth    string       `json:"auth"`
	AuthAt  time.Time    `json:"authAt"`
}

type phaseBody struct {
	Kind         string           `json:"kind"`
	Operation    string           `json:"operation"`
	Claims       int              `json:"claims"`
	Executor     string           `json:"executor"`
	Steps        []stepRecordBody `json:"steps"`
	StateVersion string           `json:"stateVersion"`
	Plan         *planBody        `json:"plan"`
	Gates        []gateBody       `json:"gates"`
	Approvals    []approvalBody   `json:"approvals"`
	StartedAt    time.Time        `json:"startedAt"`
	FinishedAt   time.Time        `json:"finishedAt"`
}

type deliveryBody struct {
	Delivery      string    `json:"delivery"`
	Commit        string    `json:"commit"`
	Branch        string    `json:"branch"`
	ReviewRequest string    `json:"reviewRequest"`
	MergedAt      time.Time `json:"mergedAt"`
	DeliveredAt   time.Time `json:"deliveredAt"`
}

type stateVersionBody struct {
	Unit      unitBody  `json:"unit"`
	VersionID string    `json:"versionID"`
	Commit    string    `json:"commit"`
	Job       string    `json:"job"`
	At        time.Time `json:"at"`
	Final     bool      `json:"final"`
}

type breakGlassBody struct {
	Reason string `json:"reason"`
	Freeze string `json:"freeze"`
}

type triggerBody struct {
	ID    string `json:"id"`
	Saver string `json:"saver"`
}

// jobBody is the part of a job that no column holds.
type jobBody struct {
	Spec         specBody          `json:"spec"`
	Reason       string            `json:"reason"`
	Phases       []phaseBody       `json:"phases"`
	StartedBy    string            `json:"startedBy"`
	Source       string            `json:"source"`
	Agent        string            `json:"agent"`
	AgentMode    string            `json:"agentMode"`
	BreakGlass   *breakGlassBody   `json:"breakGlass"`
	Trigger      *triggerBody      `json:"trigger,omitempty"`
	Delivery     *deliveryBody     `json:"delivery"`
	Closed       *stateVersionBody `json:"closed"`
	Verification string            `json:"verification"`
	RequestHash  string            `json:"requestHash"`
}

func encodeUnit(o identity.Object) unitBody {
	return unitBody{Tool: o.Tool(), Namespace: o.Namespace().String(), Name: o.UnitName(), UnitID: o.UnitID()}
}

func decodeUnit(b unitBody) (identity.Object, error) {
	ns, err := namespace.ParseName(b.Namespace)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit namespace: %w", err)
	}

	o, err := identity.UnitObject(b.Tool, ns, b.Name)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit object: %w", err)
	}

	return o.WithUnitID(b.UnitID), nil
}

func encodeSpec(s *job.Spec) specBody {
	out := specBody{
		Unit: encodeUnit(s.Unit), Operation: string(s.Operation), Purpose: string(s.Purpose),
		Executor: string(s.Executor),
		Location: locationBody(s.Location), Steps: nil, Pool: s.Pool, Profile: s.Profile, Edits: nil,
		Delivery: string(s.Delivery), Defaulted: slices.Clone(s.Defaulted),
	}

	for i := range s.Steps {
		st := &s.Steps[i]
		sb := stepBody{
			Name: st.Name, Role: string(st.Role), Phase: string(st.Phase), Args: st.Args, Env: st.Env, Dir: st.Dir,
			TimeoutNanos: int64(st.Timeout), AllowFailure: st.AllowFailure, ChangesResources: st.ChangesResources,
			Outputs: nil,
		}

		for _, o := range st.Outputs {
			sb.Outputs = append(sb.Outputs, outputBody{Kind: string(o.Kind), Path: o.Path})
		}

		out.Steps = append(out.Steps, sb)
	}

	for _, e := range s.Edits {
		out.Edits = append(out.Edits, editBody{Path: e.Path, Keys: e.Keys})
	}

	return out
}

func decodeSpec(b *specBody) (job.Spec, error) {
	unit, err := decodeUnit(b.Unit)
	if err != nil {
		return job.Spec{}, fmt.Errorf("%w: %w", errBodyUnit, err)
	}

	out := job.Spec{
		Unit: unit, Operation: job.Operation(b.Operation), Purpose: job.Purpose(b.Purpose),
		Executor: job.ExecutorKind(b.Executor),
		Location: job.Location(b.Location), Steps: nil, Pool: b.Pool, Profile: b.Profile, Edits: nil,
		Delivery: job.Delivery(b.Delivery), Defaulted: slices.Clone(b.Defaulted),
	}

	for i := range b.Steps {
		sb := &b.Steps[i]
		st := job.Step{
			Name: sb.Name, Role: job.StepRole(sb.Role), Phase: job.PhaseKind(sb.Phase), Args: sb.Args, Env: sb.Env,
			Dir: sb.Dir, Timeout: time.Duration(sb.TimeoutNanos), AllowFailure: sb.AllowFailure,
			ChangesResources: sb.ChangesResources, Outputs: nil,
		}

		for _, o := range sb.Outputs {
			st.Outputs = append(st.Outputs, job.Output{Kind: job.OutputKind(o.Kind), Path: o.Path})
		}

		out.Steps = append(out.Steps, st)
	}

	for _, e := range b.Edits {
		out.Edits = append(out.Edits, job.FileEdit{Path: e.Path, Keys: e.Keys})
	}

	return out, nil
}

func encodeLog(l *job.LogRef) *logBody {
	if l == nil {
		return nil
	}

	return &logBody{
		ID: l.ID, ItemKey: string(l.ItemKey), Version: l.Version, Chunks: l.Chunks, SHA256: l.SHA256, Size: l.Size,
		CreatedAt: l.CreatedAt, ClosedAt: l.ClosedAt, ExpiresAt: l.ExpiresAt, RedactedAt: l.RedactedAt,
		ReplacedBy: l.ReplacedBy, Matches: l.Matches,
	}
}

func decodeLog(b *logBody) *job.LogRef {
	if b == nil {
		return nil
	}

	return &job.LogRef{
		ID: b.ID, ItemKey: secret.ItemKeyID(b.ItemKey), Version: b.Version, Chunks: b.Chunks, SHA256: b.SHA256,
		Size: b.Size, CreatedAt: b.CreatedAt, ClosedAt: b.ClosedAt, ExpiresAt: b.ExpiresAt,
		RedactedAt: b.RedactedAt, ReplacedBy: b.ReplacedBy, Matches: b.Matches,
	}
}

func encodePlan(p *job.PlanRef) *planBody {
	if p == nil {
		return nil
	}

	out := &planBody{
		ID: p.ID, ItemKey: string(p.ItemKey), StateVersion: p.StateVersion, Parts: nil, CreatedAt: p.CreatedAt,
		ExpiresAt: p.ExpiresAt, RedactedAt: p.RedactedAt,
		ReservePhase: 0, ReserveOperation: "", ReserveToken: 0,
	}

	for _, part := range p.Parts {
		out.Parts = append(out.Parts, planPartBody{
			Kind: string(part.Kind), Size: part.Size, SHA256: part.SHA256, Secret: part.Secret,
			StoredSize: part.StoredSize, StoredMD5: part.StoredMD5,
		})
	}

	return out
}

// encodeReservePlan returns the body of a reserve: the plan fields, and the
// phase, the operation and the token of the claim that made the reserve.
func encodeReservePlan(p *job.PlanRef, pc job.PhaseClaim) *planBody {
	out := encodePlan(p)
	out.ReservePhase, out.ReserveOperation, out.ReserveToken = pc.Phase, string(pc.Claim.Operation), pc.Claim.Token

	return out
}

func decodePlan(b *planBody) *job.PlanRef {
	if b == nil {
		return nil
	}

	out := &job.PlanRef{
		ID: b.ID, ItemKey: secret.ItemKeyID(b.ItemKey), StateVersion: b.StateVersion, Parts: nil,
		CreatedAt: b.CreatedAt, ExpiresAt: b.ExpiresAt, RedactedAt: b.RedactedAt,
	}

	for _, part := range b.Parts {
		out.Parts = append(out.Parts, job.PlanPart{
			Kind: job.OutputKind(part.Kind), Size: part.Size, SHA256: part.SHA256, Secret: part.Secret,
			StoredSize: part.StoredSize, StoredMD5: part.StoredMD5,
		})
	}

	return out
}

func encodeBinding(p *job.PlanBinding) *bindingBody {
	if p == nil {
		return nil
	}

	b := bindingBody(*p)

	return &b
}

func decodeBinding(b *bindingBody) *job.PlanBinding {
	if b == nil {
		return nil
	}

	p := job.PlanBinding(*b)

	return &p
}

func encodePhase(p *job.Phase) phaseBody {
	out := phaseBody{
		Kind: string(p.Kind), Operation: p.Operation, Claims: p.Claims, Executor: p.Executor, Steps: nil,
		StateVersion: p.StateVersion, Plan: encodePlan(p.Plan), Gates: nil, Approvals: nil, StartedAt: p.StartedAt,
		FinishedAt: p.FinishedAt,
	}

	for i := range p.Steps {
		s := &p.Steps[i]
		out.Steps = append(out.Steps, stepRecordBody{
			Name: s.Name, Token: s.Token, ExitCode: s.ExitCode, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
			Log: encodeLog(s.Log),
		})
	}

	for _, g := range p.Gates {
		out.Gates = append(out.Gates, gateBody{
			Type: string(g.Type), Approvals: g.Approvals, Groups: g.Groups, GroupIDs: g.GroupIDs,
			AllowSelf: g.AllowSelf, TimeoutNanos: int64(g.Timeout), BindPlan: g.BindPlan,
		})
	}

	for i := range p.Approvals {
		a := &p.Approvals[i]
		out.Approvals = append(out.Approvals, approvalBody{
			Gate: string(a.Gate), Subject: a.Subject, Agent: a.Agent, Verdict: string(a.Verdict),
			Comment: a.Comment, Plan: encodeBinding(a.Plan), At: a.At, Auth: string(a.Auth), AuthAt: a.AuthAt,
		})
	}

	return out
}

func decodePhase(b *phaseBody) job.Phase {
	out := job.Phase{
		Kind: job.PhaseKind(b.Kind), Operation: b.Operation, Claims: b.Claims, Executor: b.Executor, Steps: nil,
		StateVersion: b.StateVersion, Plan: decodePlan(b.Plan), Gates: nil, Approvals: nil, StartedAt: b.StartedAt,
		FinishedAt: b.FinishedAt,
	}

	for i := range b.Steps {
		s := &b.Steps[i]
		out.Steps = append(out.Steps, job.StepRecord{
			Name: s.Name, Token: s.Token, ExitCode: s.ExitCode, StartedAt: s.StartedAt, FinishedAt: s.FinishedAt,
			Log: decodeLog(s.Log),
		})
	}

	for _, g := range b.Gates {
		out.Gates = append(out.Gates, job.Gate{
			Type: job.GateType(g.Type), Approvals: g.Approvals, Groups: g.Groups, GroupIDs: g.GroupIDs,
			AllowSelf: g.AllowSelf, Timeout: time.Duration(g.TimeoutNanos), BindPlan: g.BindPlan,
		})
	}

	for i := range b.Approvals {
		a := &b.Approvals[i]
		out.Approvals = append(out.Approvals, job.Approval{
			Gate: job.GateType(a.Gate), Subject: a.Subject, Agent: a.Agent, Verdict: job.Verdict(a.Verdict),
			Comment: a.Comment, Plan: decodeBinding(a.Plan), At: a.At, Auth: job.ConfirmAuth(a.Auth),
			AuthAt: a.AuthAt,
		})
	}

	return out
}

func encodeStateVersion(v *job.StateVersion) *stateVersionBody {
	if v == nil {
		return nil
	}

	return &stateVersionBody{
		Unit: encodeUnit(v.Unit), VersionID: v.VersionID, Commit: v.Commit, Job: string(v.Job), At: v.At,
		Final: v.Final,
	}
}

func decodeStateVersion(b *stateVersionBody) (*job.StateVersion, error) {
	if b == nil {
		return nil, nil //nolint:nilnil // No closing version is not an error.
	}

	unit, err := decodeUnit(b.Unit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errBodyUnit, err)
	}

	return &job.StateVersion{
		Unit: unit, VersionID: b.VersionID, Commit: b.Commit, Job: job.ID(b.Job), At: b.At, Final: b.Final,
	}, nil
}

// encodeBody returns the JSON body of j.
func encodeBody(j *job.Job) (string, error) {
	b := jobBody{
		Spec:         encodeSpec(&j.Spec),
		Reason:       j.Reason,
		Phases:       nil,
		StartedBy:    j.StartedBy,
		Source:       string(j.Source),
		Agent:        j.Agent,
		AgentMode:    string(j.AgentMode),
		BreakGlass:   nil,
		Trigger:      nil,
		Delivery:     nil,
		Closed:       encodeStateVersion(j.Closed),
		Verification: string(j.Verification),
		RequestHash:  j.RequestHash,
	}

	for i := range j.Phases {
		b.Phases = append(b.Phases, encodePhase(&j.Phases[i]))
	}

	if j.BreakGlass != nil {
		bg := breakGlassBody(*j.BreakGlass)
		b.BreakGlass = &bg
	}

	if j.Trigger != nil {
		b.Trigger = &triggerBody{ID: j.Trigger.ID, Saver: j.Trigger.Saver}
	}

	if d := j.Delivery; d != nil {
		b.Delivery = &deliveryBody{
			Delivery: string(d.Delivery), Commit: d.Commit, Branch: d.Branch, ReviewRequest: d.ReviewRequest,
			MergedAt: d.MergedAt, DeliveredAt: d.DeliveredAt,
		}
	}

	// Defense in depth: the decode checks each stored body again. This check
	// stays so that a write of an unknown value fails at the write and never
	// stores a row that cannot decode.
	if err := checkEnums(&b, job.ErrInvalid); err != nil {
		return "", fmt.Errorf("encode job %s: %w", j.ID, err)
	}

	raw, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("encode job %s: %w", j.ID, err)
	}

	return string(raw), nil
}

// checkEnums returns an error that wraps sentinel for a source kind, an
// agent mode or a verification result in b that this server never writes.
// The error names the field and never the value. The decode wraps ErrDamaged,
// because a stored row is untrusted: a restore or another version may have
// written it. The encode wraps job.ErrInvalid, because there the value comes
// from the caller. The empty agent mode is accepted, the empty source is
// refused, and the empty verification is valid.
func checkEnums(b *jobBody, sentinel error) error {
	source := job.SourceKind(b.Source)
	if !source.Valid() {
		return fmt.Errorf("source: %w", sentinel)
	}

	mode := job.AgentMode(b.AgentMode)
	if mode != "" && !mode.Valid() {
		return fmt.Errorf("agent mode: %w", sentinel)
	}

	verification := job.Verification(b.Verification)
	if !verification.Valid() {
		return fmt.Errorf("verification: %w", sentinel)
	}

	return nil
}

// The kinds of damage of a stored job row. DamageReason returns their text as
// the reason of a job.DamagedRow. The text never holds a value that the body
// stored.
var (
	// errBodyJSON reports a body that is not JSON.
	errBodyJSON = errors.New("the body is not JSON")
	// errBodyValue reports a body that holds a value that this version does
	// not know, for example a source kind, an agent mode or a verification
	// result that another version of the server wrote.
	errBodyValue = errors.New("the body holds a value that this version does not know")
	// errBodyUnit reports a body that holds a unit reference that this
	// version does not know.
	errBodyUnit = errors.New("the body holds a unit reference that this version does not know")
	// errBodyTrigger reports a body that holds a trigger reference that is
	// not valid.
	errBodyTrigger = errors.New("the body holds a trigger reference that is not valid")
	// errRowUnit reports a body that names another namespace or another unit
	// than its row.
	errRowUnit = errors.New("the body names another namespace or unit than its row")
)

// DamageReason returns the kind of a decode error, for the reason of a
// job.DamagedRow. It never returns a value that the body stored.
func DamageReason(err error) string {
	switch {
	case errors.Is(err, errBodyJSON):
		return errBodyJSON.Error()
	case errors.Is(err, errBodyValue):
		return errBodyValue.Error()
	case errors.Is(err, errBodyUnit):
		return errBodyUnit.Error()
	case errors.Is(err, errBodyTrigger):
		return errBodyTrigger.Error()
	case errors.Is(err, errRowUnit):
		return errRowUnit.Error()
	default:
		return "the body does not decode"
	}
}

// decodeBody fills the fields of j that the body holds.
func decodeBody(body string, j *job.Job) error {
	var b jobBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		return fmt.Errorf("decode job %s: %w: %w", j.ID, errBodyJSON, err)
	}

	// A stored row is untrusted: a restore or another version of the server
	// may have written it. The error names the field only, never the value.
	if err := checkEnums(&b, ErrDamaged); err != nil {
		return fmt.Errorf("decode job %s: %w: %w", j.ID, errBodyValue, err)
	}

	spec, err := decodeSpec(&b.Spec)
	if err != nil {
		return fmt.Errorf("decode job %s: %w", j.ID, err)
	}

	closed, err := decodeStateVersion(b.Closed)
	if err != nil {
		return fmt.Errorf("decode job %s: %w", j.ID, err)
	}

	j.Spec, j.Reason, j.StartedBy, j.Agent, j.AgentMode = spec, b.Reason, b.StartedBy, b.Agent, job.AgentMode(
		b.AgentMode,
	)
	j.Source = job.SourceKind(b.Source)
	j.Closed, j.Verification = closed, job.Verification(b.Verification)
	j.RequestHash = b.RequestHash
	j.Phases = make([]job.Phase, len(b.Phases))

	for i := range b.Phases {
		j.Phases[i] = decodePhase(&b.Phases[i])
	}

	if b.Trigger != nil {
		ref := &job.TriggerRef{ID: b.Trigger.ID, Saver: b.Trigger.Saver}

		// A stored row is untrusted: a restore or another version may have
		// written it. The read handler trusts this reference to find a saver.
		if err := ref.Validate(); err != nil {
			return fmt.Errorf("decode job %s: %w: trigger", j.ID, errBodyTrigger)
		}

		j.Trigger = ref
	}

	if b.BreakGlass != nil {
		bg := job.BreakGlass(*b.BreakGlass)
		j.BreakGlass = &bg
	}

	if d := b.Delivery; d != nil {
		j.Delivery = &job.DeliveryResult{
			Delivery: job.Delivery(d.Delivery), Commit: d.Commit, Branch: d.Branch, ReviewRequest: d.ReviewRequest,
			MergedAt: d.MergedAt, DeliveredAt: d.DeliveredAt,
		}
	}

	return nil
}
