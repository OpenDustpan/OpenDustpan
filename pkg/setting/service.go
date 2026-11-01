package setting

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Namespaces reads the namespace that a write fences. namespace.Store
// implements it.
type Namespaces interface {
	GetNamespace(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// Service reads and changes settings. It adds the fence, the time and the
// scope of the audit entry to the calls of the Store.
type Service struct {
	store      Store
	registry   *Registry
	static     Static
	namespaces Namespaces
	now        func() time.Time
}

// NewService returns a Service. static is the static layer of the
// installation; nil means none. now gives the time of a change; nil means
// time.Now.
func NewService(store Store, registry *Registry, static Static, namespaces Namespaces, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}

	if static == nil {
		static = NoStatic{}
	}

	return &Service{store: store, registry: registry, static: static, namespaces: namespaces, now: now}
}

// Registry returns the declarations of the service.
func (s *Service) Registry() *Registry { return s.registry }

// Get returns the resource of scope: each declared setting with its
// effective value and its source.
func (s *Service) Get(ctx context.Context, scope Scope) (Resource, error) {
	if err := checkScope(scope); err != nil {
		return Resource{Items: nil, Revision: 0}, err
	}

	stored, err := s.store.Load(ctx, scope)
	if err != nil {
		return Resource{Items: nil, Revision: 0}, fmt.Errorf("read settings: %w", err)
	}

	return Read(s.registry, scope, s.static, stored), nil
}

// Effective returns the setting key of scope as consumers read it: the
// effective value and its source.
func (s *Service) Effective(ctx context.Context, scope Scope, key string) (Item, error) {
	res, err := s.Get(ctx, scope)
	if err != nil {
		return Item{}, err
	}

	for i := range res.Items {
		if res.Items[i].Key == key {
			return res.Items[i], nil
		}
	}

	return Item{}, fmt.Errorf("%w: %s", ErrUnknownKey, shown(key))
}

// PatchRequest is what a caller gives to Patch besides the edits.
type PatchRequest struct {
	// Entry is the audit entry of the request: the caller, the channel, the
	// object and the request ID. The service sets the scope, the action, the
	// time, the kind and the outcome.
	Entry audit.Entry
	// IfMatch is the revision that the caller read.
	IfMatch int64
	// Fresh says that the caller has a fresh authentication.
	Fresh bool
}

// Patch applies patch to scope. A change that reduces security and has no
// fresh authentication returns ErrFreshStepRequired and changes nothing.
func (s *Service) Patch(ctx context.Context, scope Scope, patch Patch, req PatchRequest) (Result, error) {
	none := Result{Resource: Resource{Items: nil, Revision: 0}, Changed: nil}

	if err := checkScope(scope); err != nil {
		return none, err
	}

	var fence namespace.Fence

	entry := req.Entry
	entry.Namespace = audit.ServerScope
	entry.Action = string(identity.ActionServerManageSettings)

	if !scope.IsInstallation() {
		ns, err := s.namespaces.GetNamespace(ctx, scope.Namespace)
		if err != nil {
			return none, fmt.Errorf("read namespace: %w", err)
		}

		fence = ns.Fence()
		entry.Namespace = scope.Namespace
		entry.Action = string(identity.ActionNamespaceManageSettings)
	}

	at := s.now().UTC().Truncate(time.Millisecond)
	entry.At = at
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess

	res, err := s.store.Update(ctx, scope, fence, ChangeInput{
		Registry: s.registry, Static: s.static, Patch: patch, Entry: entry,
		IfMatch: req.IfMatch, Fresh: req.Fresh, AtMs: at.UnixMilli(),
	})
	if err != nil {
		return none, fmt.Errorf("change settings: %w", err)
	}

	return res, nil
}

// SyncStatic records the static layer of this instance. When it changed an
// effective value or a source, the revision of the installation settings
// changes. It returns the revision.
func (s *Service) SyncStatic(ctx context.Context) (int64, bool, error) {
	revision, changed, err := s.store.Sync(ctx, s.registry.Digest(s.static))
	if err != nil {
		return 0, false, fmt.Errorf("sync the static layer: %w", err)
	}

	return revision, changed, nil
}

// checkScope refuses a scope that cannot hold settings.
func checkScope(scope Scope) error {
	if !scope.IsInstallation() && scope.Namespace == audit.ServerScope {
		return fmt.Errorf("%w: the scope %q is not a namespace", ErrScope, scope.Namespace)
	}

	return nil
}
