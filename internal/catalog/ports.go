package catalog

import (
	"context"
	"encoding/json"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Store is the persistence port for requests and their revisions. Each SQL
// store adapter implements it in its own SQL and runs the suite in package
// catalogtest. Each write checks fence in its transaction and returns an
// error that wraps namespace.ErrFenced when the fence is stale, and appends
// its audit entries in the same transaction.
type Store interface {
	// Submit stores s: it creates the request at its first revision, with
	// s.Request.Version 0, or updates it at s.Request.Version, and stores
	// the revision in StateAccepted, in one transaction. For a revision of
	// OriginAPI it starts the durable operation of KindStart with the start
	// key of Revision.Key. The store sets the ID of a new request, the
	// number of the revision, which is the last number plus 1, and
	// Request.Last. In the same transaction, it:
	//   - moves the revision s.Supersedes to StateSuperseded, with
	//     CheckSupersede, and gives its unit and its reservation to the new
	//     revision when both are creates,
	//   - makes the empty unit of s.Provision with UnitTx.Provision on the
	//     same transaction, with Provision.Request set to the ID of the
	//     request, and sets Request.Unit,
	//   - writes s.Quota through the quota.Tx of the transaction: Reserve
	//     first, then Record, and sets Revision.Reservation.
	//
	// When one of these fails, the store keeps none of them: no request, no
	// revision, no unit, no reservation and no request in a window. It
	// returns ErrConflict when the request is not at s.Request.Version, also
	// for a create of a request that exists, and for a supersede that
	// CheckSupersede refuses or that names a revision that is not the last;
	// ErrInFlight when a revision of the request has not ended and
	// s.Supersedes is 0; ErrInvalid for a revision with no deadline, a
	// reservation whose end time is not the deadline, or a new reservation
	// next to a supersede that moves one; ErrUnitName for a unit of
	// s.Provision that another request or no request owns; and the errors
	// of quota.Tx, such as an *quota.ExceededError.
	Submit(ctx context.Context, s *Submission, fence namespace.Fence) (Request, Revision, error)
	// Get returns the request ref, or ErrNotFound.
	Get(ctx context.Context, ref RequestRef) (Request, error)
	// Revision returns revision n of the request id, or ErrNotFound.
	Revision(ctx context.Context, id RequestID, n int) (Revision, error)
	// ListRequests returns the requests that f selects, by ID.
	ListRequests(ctx context.Context, f ListFilter) ([]Request, error)
	// Change applies c to the revision c.Number of the request c.Ref, with
	// CheckTransition, at c.Version of the request, and appends e, in one
	// transaction. A change to StateApplied also sets Request.Applied and
	// Request.Ensure. A change to a terminal state of a create starts the
	// durable operation of KindQuota, with the start key of QuotaKey. It
	// returns ErrNotFound, ErrConflict, and the errors of CheckTransition.
	Change(ctx context.Context, c RevisionChange, fence namespace.Fence, e []audit.Entry) (Revision, error)
	// Extend applies d to a revision in flight, at or after ExtendLead before
	// its deadline, whose job has not ended, at d.Version of the request, in
	// one transaction: it
	// moves the deadline of the revision to d.Deadline and, for a revision
	// with a reservation, the end time of the reservation to the same time,
	// through the quota.Tx of the transaction (SetEnd). It changes no state,
	// deletes no unit and starts no durable operation. The expire schedule
	// calls it after ExpiryOf. It returns ErrNotFound, ErrConflict for a
	// stale version, ErrFinished for a terminal revision, ErrInvalid for a
	// deadline that is not after the old one, and the errors of quota.Tx.
	Extend(ctx context.Context, d DeadlineChange, fence namespace.Fence) (Revision, error)
	// ObserveStateVersion applies a final state version to the revision from
	// git that waits for it, in the transaction of the state write: the
	// revision of the unit of v in StateAwaitingState with no job, whose
	// commit is exactly the commit of v, or, when v has no commit, whose
	// render was at or before v. That revision becomes StateApplied. A state
	// version at another commit applies no revision, also at a commit that
	// descends from the commit of the revision: the team's CI renders each
	// commit, so the render of a newer commit supersedes the older revision.
	// It returns the revision that changed, and false when none did.
	ObserveStateVersion(ctx context.Context, v job.StateVersion, fence namespace.Fence) (Revision, bool, error)
}

// BlueprintStore is the persistence port for blueprints: one draft and the
// published versions of each. It runs the suite in package catalogtest.
// Each write checks fence and appends its audit entries in its transaction.
type BlueprintStore interface {
	// PutDraft writes the draft of b in the namespace of fence, at version
	// of the draft that the caller read, 0 for a new blueprint. It returns
	// the new version of the draft, ErrConflict for a stale version, and
	// ErrLimit above the blueprints of a namespace.
	PutDraft(ctx context.Context, b Blueprint, version int64, fence namespace.Fence, e []audit.Entry) (int64, error)
	// Draft returns the draft of ref and its version, or ErrNotFound.
	Draft(ctx context.Context, ref BlueprintRef) (Blueprint, int64, error)
	// Publish stores the draft of p.Ref at p.DraftVersion as the next
	// published version, from 1. p.Blueprint must be that stored draft, with
	// only ExprVersion set: the store compares the two by a hash of their
	// documents, and refuses a document that differs with ErrInvalid. One
	// draft version gives one version: a second publish of a draft version
	// that has a version returns ErrAlreadyPublished. It also returns
	// ErrConflict when the draft is not at p.DraftVersion or does not exist,
	// ErrInvalid for a blueprint that fails Blueprint.Validate or whose name
	// is not the name of p.Ref, an error that wraps namespace.ErrFenced for a
	// p.Ref in another namespace than the fence, and ErrLimit above the
	// published versions of a blueprint.
	Publish(ctx context.Context, p Publication, fence namespace.Fence, e []audit.Entry) (Version, error)
	// Version returns the published version n of ref, or ErrNotFound.
	Version(ctx context.Context, ref BlueprintRef, n int) (Version, error)
	// ListBlueprints returns the newest published version of each blueprint
	// of ns, by name. A blueprint with no published version is not in it.
	ListBlueprints(ctx context.Context, ns namespace.Name) ([]Version, error)
	// Retire marks the version n of ref retired. A retire is for one version:
	// the other versions stay as they are, and a publish after a retire is
	// allowed, as a package registry deprecates one version and still takes
	// the next. A retire of a retired version changes nothing, and still
	// appends e. It returns ErrNotFound for a version that does not exist,
	// also for n below 1, and an error that wraps namespace.ErrFenced for a
	// ref in another namespace than the fence.
	Retire(ctx context.Context, ref BlueprintRef, n int, fence namespace.Fence, e []audit.Entry) error
	// SetExprVersion records that the version c.Number of c.Ref compiles with
	// the Expr version c.Expr, and appends e in the same transaction. It returns
	// ErrNotFound as Retire does, ErrInvalid for an Expr version that is
	// empty or not a version, and an error that wraps namespace.ErrFenced for
	// a ref in another namespace than the fence.
	SetExprVersion(ctx context.Context, c ExprVersionChange, fence namespace.Fence, e []audit.Entry) error
}

// ExprVersionChange is one change of the Expr version of a published version:
// the version Number of Ref compiles with the Expr version Expr.
type ExprVersionChange struct {
	Ref    BlueprintRef
	Expr   string
	Number int
}

// Publication is one publish: the draft of Ref at DraftVersion, as the
// caller checked and compiled it, with the Expr version that compiled it.
// Blueprint is the stored draft and nothing else.
type Publication struct {
	At           time.Time
	Ref          BlueprintRef
	By           string
	Blueprint    Blueprint
	DraftVersion int64
}

// Provision is what UnitProvisioner.Provision makes: the unit of a request,
// of the tool of the blueprint, with the name that the blueprint computed.
type Provision struct {
	Request   RequestID
	Tool      string
	Unit      string
	Ref       RequestRef
	Blueprint BlueprintRef
	Version   int
}

// UnitTx makes the empty unit of a create inside the open transaction of
// the store that holds it. Its writes commit or roll back with that
// transaction, so a submit never keeps a unit without its request, or a
// request without its unit. A UnitTx is valid only inside the Store.Submit
// call that the wiring gives it to.
//
// The internal wiring implements it with the code of each tool on the
// transaction of the SQL store. It passes the SQL store adapter a
// constructor that takes the transaction of the adapter and returns a
// UnitTx, and the adapter calls the constructor once in each Submit that
// has a Provision. The suite RunUnitTx in package catalogtest states the
// rules.
type UnitTx interface {
	// Provision is UnitProvisioner.Provision on the transaction: it is
	// idempotent by p.Request, and returns ErrUnitName for a name that the
	// tool refuses or a unit that another request or no request owns, and
	// ErrInvalid for an empty field of p.
	Provision(ctx context.Context, p Provision) (identity.Object, error)
}

// UnitProvisioner makes and deletes the unit of a request. The internal
// wiring implements it with each tool package, so no tool package imports
// this package.
type UnitProvisioner interface {
	// Provision makes an empty unit for p: a Pulumi stack or an OpenTofu
	// workspace with no state, and records the blueprint of the unit for
	// the inventory. It is idempotent by p.Request: a second call returns
	// the unit of the first. It returns the unit reference with its stable
	// unit ID set, and ErrUnitName for a name that the tool refuses or a
	// unit that another request or no request owns.
	Provision(ctx context.Context, p Provision) (identity.Object, error)
	// Remove deletes the unit of request id when it has no state version.
	// A remove of a unit that does not exist changes nothing. It returns
	// ErrHasState for a unit with a state version, which stays.
	Remove(ctx context.Context, unit identity.Object, id RequestID) error
}

// File is one file of a render, with a path below the render directory.
type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

// RenderInput is what a Renderer renders: a document of a request, the
// blueprint version that it pins, and the unit.
type RenderInput struct {
	Version  *Version
	Document *Document
	Unit     identity.Object
}

// Rendered is the result of a render: the operation that the tool runs,
// the files, and the config keys that the files set, for the one-home check
// against the other homes of the unit.
type Rendered struct {
	Operation Operation `json:"operation"`
	Files     []File    `json:"files"`
	Keys      []string  `json:"keys"`
}

// Renderer turns a revision into the files of one tool: an OpenTofu root
// module of one module call and its variables file, or a Pulumi YAML
// program of one component resource and its stack settings. Each tool has
// one adapter in the internal wiring. Two renders of one input give the
// same bytes. A render never holds a secret value: secrets come from the
// profile of the blueprint at run time.
//
// An answer and a platform value are data only. The render writes them
// with a JSON encoder into the variables file, .tfvars.json, or into the
// stack settings of Pulumi, and never into the text of the root module or
// of the Pulumi YAML program. The program text names only the keys of the
// blueprint, which ValidKey checked at the publish. So no answer can change
// the program that the tool runs.
type Renderer interface {
	// Tool returns the tool name that the renderer renders for.
	Tool() string
	// Render returns the files of in. It returns ErrOneHome, naming the key
	// and both homes, for a key that another home of the unit also sets,
	// for example a stack settings file that the team wrote. It compares
	// the keys in the form of NormalizeKey.
	Render(ctx context.Context, in RenderInput) (Rendered, error)
}

// RenderRequest is the body of a render for the team's CI: the request file
// at Path in Repository of the forge connection Connection, at Commit. The
// server reads the file from the forge itself, through RequestFiles, and
// never takes a document from the caller, so a render renders only what is
// in git at that commit. An empty Connection takes the forge connection of
// the namespace.
type RenderRequest struct {
	Namespace  namespace.Name
	Connection string
	Repository string
	Commit     string
	Path       string
}

// RequestFiles reads request files from the forge. The internal wiring
// implements it with the forge connections of the namespace.
type RequestFiles interface {
	// Read returns the bytes of the file at path in repository of the forge
	// connection, at commit, which must be a full commit ID. It returns
	// ErrNotFound for a missing file, repository or commit, ErrLimit for a
	// file above MaxDocumentBytes, and an error that wraps
	// ErrForgeUnavailable when it cannot read.
	Read(ctx context.Context, ns namespace.Name, connection, repository, commit, path string) ([]byte, error)
}

// SubmitRequest is the body of a submit: the document, the reason of the
// change, and an optional break-glass through an active freeze.
// IdempotencyKey makes a retry safe.
type SubmitRequest struct {
	BreakGlass     *job.BreakGlass
	Document       Document
	Reason         string
	IdempotencyKey string
	Channel        audit.Channel
}

// SubmitResult is the answer of a submit. Unchanged is true when the
// document equals the last applied revision, which then is Revision and
// has no new job.
type SubmitResult struct {
	Request   Request
	Revision  Revision
	Unchanged bool
}

// OptionsQuery asks for the options of the choices of a blueprint version
// with the answers so far.
type OptionsQuery struct {
	Answers   map[string]json.RawMessage
	Blueprint BlueprintRef
	Version   int
}

// Service is what the REST API and every feature of the server that sends
// Catalog requests call. It checks the actions of Requirements, runs the
// checks at each recheck point, writes the audit entries, and starts jobs
// through job.Service. Each method that takes a request returns ErrNotFound
// when the principal lacks catalog:read on the unit of the request.
type Service interface {
	// Submit checks req at PointSubmit and stores a revision. It returns a
	// *CheckError that matches ErrCheckFailed with every failure, ErrInFlight,
	// job.ErrFrozen for a submit during a freeze with no break-glass, an
	// error that wraps identity.ErrDenied for a missing action, and an error
	// that wraps policy.ErrUnavailable or ErrSourceUnavailable for a check
	// that cannot run.
	Submit(ctx context.Context, a job.Actor, req SubmitRequest) (SubmitResult, error)
	// Check runs the checks of PointSubmit on d and stores nothing. It
	// returns the failures, and the errors of Submit for a check that
	// cannot run.
	Check(ctx context.Context, a job.Actor, d Document) ([]Failure, error)
	// Options returns the options of each choice input of q, in the order
	// of Order, each cut with OptionSet.ForRoute. It reads the sources as
	// the source principal of the blueprint, after a check of
	// SourceRequirements for that principal.
	Options(ctx context.Context, a job.Actor, q OptionsQuery) ([]OptionSet, error)
	// Render reads the request file of r from the forge, checks it with
	// CheckPath and at PointApply, and returns its files, for the team's
	// CI. A document that differs from the last revision stores a revision
	// from git at r.Commit, in one store write that also makes the empty
	// unit of a create and supersedes a revision in flight. It needs the
	// actions of Requirements for OpRender. It returns a *CheckError as
	// Submit does, ErrPathMismatch, ErrConflict for a supersede that
	// CheckSupersede refuses, and the errors of RequestFiles.
	Render(ctx context.Context, a job.Actor, r RenderRequest) (Rendered, error)
	// Get returns the request ref.
	Get(ctx context.Context, a job.Actor, ref RequestRef) (Request, error)
	// Cancel cancels the revision in flight of ref through job.Service, and
	// returns the error of the job model, for example job.ErrDelivered.
	Cancel(ctx context.Context, a job.Actor, ref RequestRef, reason string) (Revision, error)
}

// Rechecker is what the job model calls, through its precondition port,
// before it records an approval and before the dispatch of a phase that
// changes, for a job with a Catalog origin. It checks the revision key of
// the job at point. It returns a *CheckError for a failure, which rejects
// the job, ErrPolicyChanged for a policy that asks for more approvals than
// the job has, and another error for a check that cannot run: a policy or a
// quota rule that fails to run (policy.ErrRuleFailed,
// quota.ErrRuleFailed), an engine that does not answer
// (policy.ErrUnavailable), or a source that cannot read
// (ErrSourceUnavailable). With such an error, the job model records no
// approval and dispatches nothing, and the gate timeout of the job still
// ends a job whose check keeps failing.
type Rechecker interface {
	Recheck(ctx context.Context, revision string, point RecheckPoint) error
}
