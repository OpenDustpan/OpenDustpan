package agent

import (
	"context"
	"errors"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// The read ports of the Toolset. Each package that owns the data implements
// its port: the inventory, each tool package, the job model, the audit log,
// the authorizer and the docs. A Toolset checks the actions before it calls
// a port, so a port does not check them again, except where it says so.
// Every port answers masked values, except UnitReader.Reveal. Each
// implementation runs the conformance suite of its own package for the data
// that it reads: these ports only shape that data for the tools.

// ErrUnitNotFound reports a unit, an update or a job that a read port does
// not find.
var ErrUnitNotFound = errors.New("unit, update or job not found")

// ErrObserveUnavailable reports a unit with no profile or workload identity
// that grants a read-only role in its cloud.
var ErrObserveUnavailable = errors.New("the unit has no read-only cloud identity")

// UnitQuery is the filter of search_units. Limit is between 1 and
// MaxSearchLimit.
type UnitQuery struct {
	Query        string
	Namespace    namespace.Name
	Tool         string
	ResourceType string
	Limit        int
}

// UnitFinder finds units across tools. The inventory implements it.
type UnitFinder interface {
	// Find returns the units in scope that q selects, in the order of the
	// inventory, at most q.Limit. scope is the intersection of the scopes
	// of ActionAgentUse and of identity.ActionUnitRead for the person: the
	// finder filters in its query, never with one check for each unit.
	Find(ctx context.Context, scope identity.Scope, q UnitQuery) (SearchUnitsResult, error)
}

// StateQuery selects what read_state reads. Version and CompareTo are empty
// for the latest version and for no comparison.
type StateQuery struct {
	Version   string
	CompareTo string
	Filter    string
}

// UnitReader reads one unit of one tool. The tool package of the unit
// implements it: one for Pulumi stacks, one for OpenTofu workspaces. Each
// keeps the words of its tool. Every method returns ErrUnitNotFound for a
// unit that does not exist.
type UnitReader interface {
	// Tool returns the tool name of the units that it reads.
	Tool() string
	// Describe returns the summary of unit. Executors stay empty: the
	// Toolset fills them from Changes.Support.
	Describe(ctx context.Context, unit identity.Object) (UnitResult, error)
	// State returns the resources of a version, or the changes from
	// q.CompareTo to q.Version, with secret values masked.
	State(ctx context.Context, unit identity.Object, q StateQuery) (StateResult, error)
	// Reveal returns the resources of a version with secret values in plain
	// text, and each of those values. The Toolset calls it only after the
	// check of identity.ActionUnitReadSecrets and an open secret gate. It
	// returns ErrInvalid for a query with CompareTo.
	Reveal(ctx context.Context, unit identity.Object, q StateQuery) (StateResult, []string, error)
	// History returns the changes of the tool on unit, newest first: its
	// updates, previews, state versions, plans and locks. limit is between 1
	// and MaxHistoryLimit.
	History(ctx context.Context, unit identity.Object, limit int) (HistoryResult, error)
	// RunOutput returns the events of the update run of the tool, or
	// ErrUnitNotFound when run is not one of its updates.
	RunOutput(ctx context.Context, unit identity.Object, run string, errorsOnly bool) (RunOutputResult, error)
}

// JobReader reads the jobs of a unit. The job model implements it with the
// person as the principal, so its own checks apply.
type JobReader interface {
	// History returns the jobs of unit as history entries of the kind
	// "job", newest first, at most limit.
	History(ctx context.Context, p identity.Principal, unit identity.Object, limit int) ([]HistoryEntry, error)
	// Output returns the log of the job, or ErrUnitNotFound when job is
	// not a job of unit.
	Output(ctx context.Context, p identity.Principal, unit identity.Object, job string, errorsOnly bool) (
		RunOutputResult, error)
}

// AuditQuery is the filter of read_audit_log. Limit is between 1 and
// MaxAuditLimit.
type AuditQuery struct {
	Unit    string
	Subject string
	Action  string
	From    time.Time
	Limit   int
}

// AuditReader reads the audit log of a namespace, newest first.
type AuditReader interface {
	List(ctx context.Context, ns namespace.Name, q AuditQuery) (AuditResult, error)
}

// AccessExplainer answers who holds which action on a unit, and through
// which grant, group or role. The authorizer implements it. subject is empty
// for every subject.
type AccessExplainer interface {
	Explain(ctx context.Context, unit identity.Object, subject string) (AccessResult, error)
}

// DocsIndex finds pages in the docs of the server version, which the binary
// holds.
type DocsIndex interface {
	Search(ctx context.Context, query string, limit int) (DocsResult, error)
}

// ObserveQuery selects what observe_unit reads. What is "events", "health"
// or "all". Limit is between 1 and MaxObserveLimit.
type ObserveQuery struct {
	Resource string
	What     string
	Limit    int
}

// LiveObserver reads the live status of the resources of a unit from its
// cloud. It gets short-lived, federated, read-only credentials for each
// call, through the profile of the unit or the workload identity of the
// server, and drops them after the call. It never holds a long-lived cloud
// key and never asks the model for one. It masks values that the cloud
// marks as secret. It returns ErrObserveUnavailable for a unit with no such
// identity.
type LiveObserver interface {
	Observe(ctx context.Context, unit identity.Object, q ObserveQuery) (ObserveResult, error)
}
