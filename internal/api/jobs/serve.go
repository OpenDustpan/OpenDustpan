package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/runner"
)

// jobResponse is the answered document of a job read. A step answers its
// name, role and phase, and never its arguments or its variables. The name of
// the unit is the name at the start of the job; the unit ID stays the same
// after a rename. Pool is the name of the runner pool, the value that a start
// request takes. PoolID is the ID of the pool. A pool that no longer exists, or
// that belongs to another namespace, has no name in the answer.
type jobResponse struct {
	ID           string            `json:"id"`
	Namespace    string            `json:"namespace"`
	Unit         unitResponse      `json:"unit"`
	Operation    string            `json:"operation"`
	Purpose      string            `json:"purpose,omitempty"`
	Executor     string            `json:"executor"`
	State        string            `json:"state"`
	Wait         string            `json:"wait,omitempty"`
	Reason       string            `json:"reason,omitempty"`
	Location     locationResponse  `json:"location"`
	Pool         string            `json:"pool,omitempty"`
	PoolID       string            `json:"poolID,omitempty"`
	Profile      string            `json:"profile,omitempty"`
	Defaulted    []string          `json:"defaulted,omitempty"`
	Steps        []stepResponse    `json:"steps"`
	Phases       []phaseResponse   `json:"phases"`
	Current      int               `json:"current"`
	StartedBy    string            `json:"startedBy,omitempty"`
	Source       string            `json:"source,omitempty"`
	Agent        string            `json:"agent,omitempty"`
	AgentMode    string            `json:"agentMode,omitempty"`
	BreakGlass   *breakGlassBody   `json:"breakGlass,omitempty"`
	Trigger      *triggerResponse  `json:"trigger,omitempty"`
	Delivery     *deliveryResponse `json:"delivery,omitempty"`
	Closed       *closedResponse   `json:"closed,omitempty"`
	Verification string            `json:"verification,omitempty"`
	Version      int64             `json:"version"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

type unitResponse struct {
	ID   string `json:"id"`
	Tool string `json:"tool"`
	Name string `json:"name"`
}

type locationResponse struct {
	Connection string `json:"connection,omitempty"`
	Repository string `json:"repository,omitempty"`
	Ref        string `json:"ref,omitempty"`
	Directory  string `json:"directory,omitempty"`
	Revision   string `json:"revision,omitempty"`
	Source     string `json:"source,omitempty"`
}

// The sources of the effective code location of a job.
const (
	locationSourceRequest   = "request"
	locationSourceUnit      = "unit"
	locationSourceNamespace = "namespace"
)

type stepResponse struct {
	Name             string `json:"name"`
	Role             string `json:"role"`
	Phase            string `json:"phase"`
	ChangesResources bool   `json:"changesResources"`
}

type phaseResponse struct {
	Kind         string               `json:"kind"`
	Claims       int                  `json:"claims"`
	Executor     string               `json:"executor,omitempty"`
	StateVersion string               `json:"stateVersion,omitempty"`
	Steps        []stepRecordResponse `json:"steps"`
	Plan         *planResponse        `json:"plan,omitempty"`
	Gates        []gateResponse       `json:"gates"`
	Approvals    []approvalResponse   `json:"approvals"`
	StartedAt    *time.Time           `json:"startedAt,omitempty"`
	FinishedAt   *time.Time           `json:"finishedAt,omitempty"`
}

type stepRecordResponse struct {
	Name       string     `json:"name"`
	ExitCode   int        `json:"exitCode"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Log        *logRef    `json:"log,omitempty"`
}

type logRef struct {
	ID       string `json:"id"`
	Version  int    `json:"version"`
	Size     int64  `json:"size"`
	Closed   bool   `json:"closed"`
	Redacted bool   `json:"redacted"`
}

type planResponse struct {
	ID           string             `json:"id"`
	StateVersion string             `json:"stateVersion"`
	Parts        []planPartResponse `json:"parts"`
	CreatedAt    time.Time          `json:"createdAt"`
	ExpiresAt    *time.Time         `json:"expiresAt,omitempty"`
	RedactedAt   *time.Time         `json:"redactedAt,omitempty"`
}

type planPartResponse struct {
	Kind   string `json:"kind"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Secret bool   `json:"secret"`
}

type gateResponse struct {
	Type      string   `json:"type"`
	Approvals int      `json:"approvals"`
	Groups    []string `json:"groups,omitempty"`
	AllowSelf bool     `json:"allowSelf"`
	BindPlan  bool     `json:"bindPlan"`
}

type approvalResponse struct {
	Gate    string    `json:"gate"`
	Subject string    `json:"subject"`
	Agent   string    `json:"agent,omitempty"`
	Verdict string    `json:"verdict"`
	Comment string    `json:"comment,omitempty"`
	At      time.Time `json:"at"`
}

// triggerResponse names the run trigger that started the job, and the subject
// of its last saver at the start.
type triggerResponse struct {
	ID    string `json:"id"`
	Saver string `json:"saver"`
}

type breakGlassBody struct {
	Reason string `json:"reason"`
	Freeze string `json:"freeze"`
}

type deliveryResponse struct {
	Delivery      string     `json:"delivery"`
	Commit        string     `json:"commit,omitempty"`
	Branch        string     `json:"branch,omitempty"`
	ReviewRequest string     `json:"reviewRequest,omitempty"`
	MergedAt      *time.Time `json:"mergedAt,omitempty"`
	DeliveredAt   *time.Time `json:"deliveredAt,omitempty"`
}

type closedResponse struct {
	VersionID string    `json:"versionID"`
	Commit    string    `json:"commit,omitempty"`
	At        time.Time `json:"at"`
}

// getJob answers GET /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}
// with the job. The route wrapper already allowed namespace:read on the
// namespace and job:read on the unit of the job.
func (h *Handler) getJob(w http.ResponseWriter, r *http.Request, _ identity.Principal, t *target) {
	setETag(w, &t.job)
	h.answerJob(r.Context(), w, http.StatusOK, &t.job)
}

// answerJob writes the document of j with the status. It names the pool of
// the job. A failed read of the pool answers 500, and never a document with a
// silent blank in place of the name.
func (h *Handler) answerJob(ctx context.Context, w http.ResponseWriter, status int, j *job.Job) {
	out := toJobResponse(j)

	name, err := h.poolName(ctx, j)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out.Pool = name

	h.writeJSON(ctx, w, status, out)
}

// answerStartedJob writes the document of a job that the start stored. A
// failed read of the pool is logged at Warn with the job ID, and the answer
// keeps the status, with poolID and without pool: a write that committed never
// gets a failure answer.
func (h *Handler) answerStartedJob(ctx context.Context, w http.ResponseWriter, status int, j *job.Job) {
	out := toJobResponse(j)

	name, err := h.poolName(ctx, j)
	if err != nil {
		h.logger.WarnContext(ctx, "the pool of a started job has no name in the answer",
			slog.String("job", string(j.ID)), slog.String("error", err.Error()))
	}

	out.Pool = name

	h.writeJSON(ctx, w, status, out)
}

// poolName returns the name of the runner pool of j, or an empty name when the
// job has no pool, the pool is gone, or the pool is not in the namespace of
// the job. The server without a pool reader names no pool.
func (h *Handler) poolName(ctx context.Context, j *job.Job) (string, error) {
	if j.Spec.Pool == "" || h.pools == nil {
		return "", nil
	}

	pool, err := h.pools.Pool(ctx, j.Spec.Pool)
	if errors.Is(err, runner.ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("pool %s of job %s: %w", j.Spec.Pool, j.ID, err)
	}

	// Defense in depth: the store reads a pool by its ID, and the start checks
	// the namespace of the pool. This check stays so that a pool of another
	// namespace never gives its name to the answer of a job.
	if pool.Namespace != j.Spec.Unit.Namespace() {
		return "", nil
	}

	return pool.Name, nil
}

// setETag sets the ETag header of j: its version, in quotes. The version grows
// at each change of the job record.
func setETag(w http.ResponseWriter, j *job.Job) {
	w.Header().Set("ETag", `"v`+strconv.FormatInt(j.Version, 10)+`"`)
}

// answerLocation maps the effective code location of s to its wire shape. It
// names the source of the location. A repository that job.CheckRepository
// refuses is answered empty.
//
// Defense in depth: the job service and the planner refuse such a repository
// at the start, so no new job holds one. This check stays so that a record
// from before that check, or from another writer of the store, never gives a
// caller the password or the user name that a repository URL can hold.
func answerLocation(s *job.Spec) locationResponse {
	l := s.Location
	out := locationResponse{
		Connection: l.Connection, Repository: l.Repository, Ref: l.Ref, Directory: l.Directory,
		Revision: l.Revision, Source: "",
	}

	if !l.IsZero() {
		out.Source = locationSourceOf(s)
	}

	if job.CheckRepository(l.Repository) != nil {
		out.Repository = ""
	}

	return out
}

// locationSourceOf returns the source of the effective code location of s. A
// job that a server did not record a source for gets the source that its list
// of defaulted values gives.
func locationSourceOf(s *job.Spec) string {
	if s.LocationSource == "" {
		if slices.Contains(s.Defaulted, job.DefaultedLocation) {
			return locationSourceUnit
		}

		return locationSourceRequest
	}

	switch s.LocationSource {
	case job.LocationSourceUnit:
		return locationSourceUnit
	case job.LocationSourceNamespace:
		return locationSourceNamespace
	case job.LocationSourceRequest:
		return locationSourceRequest
	default:
		// A source that this server does not know names no source.
		return ""
	}
}

// toJobResponse maps a job to its wire shape.
func toJobResponse(j *job.Job) jobResponse {
	out := jobResponse{
		ID:        string(j.ID),
		Namespace: j.Spec.Unit.Namespace().String(),
		Unit: unitResponse{
			ID: j.Spec.Unit.UnitID(), Tool: toolLabel(j.Spec.Unit.Tool()), Name: j.Spec.Unit.UnitName(),
		},
		Operation:    string(j.Spec.Operation),
		Purpose:      string(j.Spec.Purpose),
		Executor:     string(j.Spec.Executor),
		State:        string(j.State),
		Wait:         string(j.Wait),
		Reason:       j.Reason,
		Location:     answerLocation(&j.Spec),
		Pool:         "",
		PoolID:       j.Spec.Pool,
		Profile:      j.Spec.Profile,
		Defaulted:    slices.Clone(j.Spec.Defaulted),
		Steps:        make([]stepResponse, 0, len(j.Spec.Steps)),
		Phases:       make([]phaseResponse, 0, len(j.Phases)),
		Current:      j.Current,
		StartedBy:    j.StartedBy,
		Source:       string(j.Source),
		Agent:        j.Agent,
		AgentMode:    string(j.AgentMode),
		BreakGlass:   nil,
		Trigger:      nil,
		Delivery:     nil,
		Closed:       nil,
		Verification: string(j.Verification),
		Version:      j.Version,
		CreatedAt:    j.CreatedAt,
		UpdatedAt:    j.UpdatedAt,
	}

	for i := range j.Spec.Steps {
		s := &j.Spec.Steps[i]
		out.Steps = append(out.Steps, stepResponse{
			Name: s.Name, Role: string(s.Role), Phase: string(s.Phase), ChangesResources: s.ChangesResources,
		})
	}

	for i := range j.Phases {
		out.Phases = append(out.Phases, toPhaseResponse(&j.Phases[i]))
	}

	if j.BreakGlass != nil {
		bg := breakGlassBody(*j.BreakGlass)
		out.BreakGlass = &bg
	}

	if j.Trigger != nil {
		out.Trigger = &triggerResponse{ID: j.Trigger.ID, Saver: j.Trigger.Saver}
	}

	if d := j.Delivery; d != nil {
		out.Delivery = &deliveryResponse{
			Delivery: string(d.Delivery), Commit: d.Commit, Branch: d.Branch, ReviewRequest: d.ReviewRequest,
			MergedAt: timeOrNil(d.MergedAt), DeliveredAt: timeOrNil(d.DeliveredAt),
		}
	}

	if c := j.Closed; c != nil {
		out.Closed = &closedResponse{VersionID: c.VersionID, Commit: c.Commit, At: c.At}
	}

	return out
}

func toPhaseResponse(p *job.Phase) phaseResponse {
	out := phaseResponse{
		Kind: string(p.Kind), Claims: p.Claims, Executor: p.Executor, StateVersion: p.StateVersion,
		Steps: make([]stepRecordResponse, 0, len(p.Steps)), Plan: nil, Gates: make([]gateResponse, 0, len(p.Gates)),
		Approvals: make([]approvalResponse, 0, len(p.Approvals)), StartedAt: timeOrNil(p.StartedAt),
		FinishedAt: timeOrNil(p.FinishedAt),
	}

	for i := range p.Steps {
		s := &p.Steps[i]
		rec := stepRecordResponse{
			Name: s.Name, ExitCode: s.ExitCode, StartedAt: timeOrNil(s.StartedAt), FinishedAt: timeOrNil(s.FinishedAt),
			Log: nil,
		}

		if l := s.Log; l != nil {
			rec.Log = &logRef{
				ID: l.ID, Version: l.Version, Size: l.Size, Closed: !l.ClosedAt.IsZero(),
				Redacted: !l.RedactedAt.IsZero(),
			}
		}

		out.Steps = append(out.Steps, rec)
	}

	if pl := p.Plan; pl != nil {
		plan := &planResponse{
			ID: pl.ID, StateVersion: pl.StateVersion, Parts: make([]planPartResponse, 0, len(pl.Parts)),
			CreatedAt: pl.CreatedAt, ExpiresAt: timeOrNil(pl.ExpiresAt), RedactedAt: timeOrNil(pl.RedactedAt),
		}

		for _, part := range pl.Parts {
			plan.Parts = append(plan.Parts, planPartResponse{
				Kind: string(part.Kind), Size: part.Size, SHA256: part.SHA256, Secret: part.Secret,
			})
		}

		out.Plan = plan
	}

	for _, g := range p.Gates {
		out.Gates = append(out.Gates, gateResponse{
			Type: string(g.Type), Approvals: g.Approvals, Groups: g.Groups, AllowSelf: g.AllowSelf,
			BindPlan: g.BindPlan,
		})
	}

	for i := range p.Approvals {
		a := &p.Approvals[i]
		out.Approvals = append(out.Approvals, approvalResponse{
			Gate: string(a.Gate), Subject: a.Subject, Agent: a.Agent, Verdict: string(a.Verdict),
			Comment: a.Comment, At: a.At,
		})
	}

	return out
}

// timeOrNil returns nil for the zero time, so that a field that was never
// set is not in the answer.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

// toolLabel maps a stored lowercase tool code to the tool label of the
// glossary ("Pulumi" or "OpenTofu"). A code without a label answers
// unchanged, so that a new tool still reads.
func toolLabel(code string) string {
	switch code {
	case "pulumi":
		return "Pulumi"
	case "opentofu":
		return "OpenTofu"
	default:
		return code
	}
}
