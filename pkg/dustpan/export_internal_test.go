package dustpan

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/manuallock"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// RouteSource is the list of the route patterns of one mounted adapter.
type RouteSource struct {
	Name     string
	Patterns []string
}

// Operations returns the joined operations of the adapters that the server
// mounts.
func (s *Server) Operations() []apidesc.Operation {
	if s.mounted == nil {
		return nil
	}

	return s.mounted.operations()
}

// RouteSources returns the route patterns of each mounted adapter.
func (s *Server) RouteSources() []RouteSource {
	if s.mounted == nil {
		return nil
	}

	sources := s.mounted.routeSources()
	out := make([]RouteSource, 0, len(sources))

	for _, source := range sources {
		out = append(out, RouteSource{Name: source.name, Patterns: source.patterns})
	}

	return out
}

// HandlerNames returns the names of the adapters that the server mounts.
func (s *Server) HandlerNames() []string {
	if s.mounted == nil {
		return nil
	}

	var out []string

	for _, h := range s.mounted.handlers() {
		out = append(out, h.name)
	}

	return out
}

// RootPatterns is what the root mux recorded.
type RootPatterns struct {
	// Direct holds the patterns that code registered directly.
	Direct []string
	// Mounted holds the paths that mountV1 added from the patterns of an
	// adapter.
	Mounted []string
}

// RootPatterns returns the patterns that the root mux recorded.
func (s *Server) RootPatterns() RootPatterns {
	if s.root == nil {
		return RootPatterns{Direct: nil, Mounted: nil}
	}

	return RootPatterns{
		Direct:  append([]string(nil), s.root.patterns...),
		Mounted: append([]string(nil), s.root.mounted...),
	}
}

// AllHandlerNames returns the names of every adapter slot, mounted or not.
func (s *Server) AllHandlerNames() []string {
	if s.mounted == nil {
		return nil
	}

	var out []string

	for _, h := range s.mounted.allHandlers() {
		out = append(out, h.name)
	}

	return out
}

// HandlerPackages returns the Go package of each adapter that the server
// mounts, by the name of the adapter.
func (s *Server) HandlerPackages() map[string]string {
	out := map[string]string{}

	if s.mounted == nil {
		return out
	}

	for _, h := range s.mounted.handlers() {
		t := reflect.TypeOf(h.handler)
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}

		out[h.name] = t.PkgPath()
	}

	return out
}

// ToolServices holds the domain services of the tools that newServices builds.
// The conformance suite of output sharing uses them to make units and states
// through the same code as a server.
type ToolServices struct {
	Namespaces *namespace.Service
	Stacks     *stack.Service
	Updates    *stack.Updates
	Workspaces *workspace.Service
}

// NewToolServices builds the domain services from opts as a server does, with
// an authorizer that only an administrator passes, and returns the services
// of the tools.
func NewToolServices(opts ...Option) (ToolServices, error) {
	cfg := defaultSettings()
	for _, opt := range opts {
		opt(cfg)
	}

	cfg.authorizer = identity.AdminOnly{}

	svc, err := newServices(cfg, nil, cfg.authorizer)
	if err != nil {
		return ToolServices{}, err
	}

	return ToolServices{
		Namespaces: svc.namespaces, Stacks: svc.stacks, Updates: svc.updates, Workspaces: svc.workspaces,
	}, nil
}

// PlanJob runs the job planner of the server over the pools, as the job
// service does at a start.
func PlanJob(
	ctx context.Context, pools runner.PoolStore, unit identity.Object, req *job.StartRequest,
) (job.Spec, error) {
	return jobPlanner{pools: pools, connections: nil}.Plan(ctx, unit, req)
}

// ManualLockPhaseStart builds the manual lock service from opts as a server
// does, with an authorizer that only an administrator passes, and returns when
// the phase of job jobID on unit started. It returns the zero time when the
// service has no phase clock or the clock has no answer.
func ManualLockPhaseStart(
	ctx context.Context, unit collection.UnitRef, jobID string, opts ...Option,
) (time.Time, error) {
	cfg := defaultSettings()
	for _, opt := range opts {
		opt(cfg)
	}

	cfg.authorizer = identity.AdminOnly{}

	svc, err := newManualLockService(cfg.namespaceStore, cfg.jobStore, cfg.now, cfg.authorizer)
	if err != nil {
		return time.Time{}, err
	}

	if svc == nil {
		return time.Time{}, errNoManualLockService
	}

	return svc.PhaseStart(ctx, unit, jobID), nil
}

var errNoManualLockService = errors.New("no manual lock service")

// ManualLocks returns the manual lock service that the server wired, or nil.
// A test calls it to reach the service layer with no route in front.
func (s *Server) ManualLocks() *manuallock.Service {
	return s.manualLocks
}

// UngatedHandler returns the root handler before the start gate and the
// component gate, so that a test can send a request to a server that did not
// start.
func (s *Server) UngatedHandler() http.Handler {
	return s.ungated
}
