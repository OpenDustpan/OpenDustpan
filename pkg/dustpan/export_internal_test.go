package dustpan

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
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
	ctx context.Context, unit manuallock.Unit, jobID string, opts ...Option,
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

// DescribeOperations returns the operations of the mounted adapters after the
// checks that the assembly runs, or the error that stops a server.
func (s *Server) DescribeOperations() ([]apidesc.Operation, error) {
	if s.mounted == nil {
		return nil, nil
	}

	return s.mounted.describeOperations()
}

// WrappedRoute returns a copy of s with the adapter name wrapped so that it
// serves pattern too. The wrapper lists pattern among the patterns of the
// adapter, and op among its operations when op is not nil. A server that does
// not mount name, or whose adapter lists no routes, is returned unchanged.
func (s *Server) WrappedRoute(name, pattern string, op *apidesc.Operation) *Server {
	if s.mounted == nil {
		return s
	}

	mount := *s.mounted

	for _, h := range mount.allHandlers() {
		if h.name != name {
			continue
		}

		wrapped, ok := newExtraRoute(h.handler, pattern, op)
		if !ok {
			return s
		}

		mount, ok = withAdapterHandler(mount, name, wrapped)
		if !ok {
			return s
		}

		copied := *s
		copied.mounted = &mount

		return &copied
	}

	return s
}

// extraRoute is one mounted adapter with one more route.
type extraRoute struct {
	http.Handler

	patterns []string
	ops      []apidesc.Operation
}

// RegisteredPatterns returns the patterns of the adapter and the extra route.
func (h extraRoute) RegisteredPatterns() []string { return h.patterns }

// Operations returns the operations of the adapter and the extra route.
func (h extraRoute) Operations() []apidesc.Operation { return h.ops }

// newExtraRoute wraps base with one more route. It returns false when base
// does not list both its patterns and its operations.
func newExtraRoute(base http.Handler, pattern string, op *apidesc.Operation) (extraRoute, bool) {
	patterns, ok := base.(patternLister)
	if !ok {
		var none extraRoute

		return none, false
	}

	operations, ok := base.(operationLister)
	if !ok {
		var none extraRoute

		return none, false
	}

	mux := http.NewServeMux()
	mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	mux.Handle("/", base)

	ops := append([]apidesc.Operation(nil), operations.Operations()...)
	if op != nil {
		ops = append(ops, *op)
	}

	return extraRoute{
		Handler:  mux,
		patterns: append(append([]string(nil), patterns.RegisteredPatterns()...), pattern),
		ops:      ops,
	}, true
}

// withAdapterHandler returns a copy of a with the handler of the adapter
// called name replaced.
func withAdapterHandler(a adapters, name string, h http.Handler) (adapters, bool) {
	set := adapterHandlerSetter(&a, name)
	if set == nil {
		return a, false
	}

	set(h)

	return a, true
}

// adapterHandlerSetter returns a function that sets the handler field of a
// that has the name, or nil.
func adapterHandlerSetter(a *adapters, name string) func(http.Handler) {
	fields := map[string]func(http.Handler){
		"server":       func(h http.Handler) { a.server = h },
		"inventory":    func(h http.Handler) { a.inventory = h },
		"audit":        func(h http.Handler) { a.audit = h },
		"context":      func(h http.Handler) { a.context = h },
		"runners":      func(h http.Handler) { a.runners = h },
		"profiles":     func(h http.Handler) { a.profiles = h },
		"issuer":       func(h http.Handler) { a.issuer = h },
		"sinks":        func(h http.Handler) { a.sinks = h },
		"jobs":         func(h http.Handler) { a.jobs = h },
		"triggers":     func(h http.Handler) { a.triggers = h },
		"unitsettings": func(h http.Handler) { a.unitSettings = h },
		"drift":        func(h http.Handler) { a.drift = h },
		"registry":     func(h http.Handler) { a.registry = h },
		"sourcefilter": func(h http.Handler) { a.sourceFilter = h },
		"trustrules":   func(h http.Handler) { a.trustRules = h },
		"selfservice":  func(h http.Handler) { a.selfService = h },
		"pulumi":       func(h http.Handler) { a.pulumi = h },
		"tfe":          func(h http.Handler) { a.tfe = h },
		"tofuhttp":     func(h http.Handler) { a.tofuHTTP = h },
	}

	return fields[name]
}

// ErrStoreDatabaseForm is the refusal of a database value that names no
// built-in store.
var ErrStoreDatabaseForm = errStoreDatabaseForm

// OpenPostgresStoreForTest runs openPostgresStore, so that a test reaches the
// form check of the inner layer.
func OpenPostgresStoreForTest(ctx context.Context, dsn string) (*BuiltinStore, error) {
	return openPostgresStore(ctx, dsn, defaultStoreConfig())
}
