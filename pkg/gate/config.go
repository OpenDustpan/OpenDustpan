package gate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// NamespaceSource gives a namespace with its fence.
type NamespaceSource interface {
	// Get returns the namespace name. It returns namespace.ErrNotFound for
	// a name that no namespace holds.
	Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error)
}

// MemberCounts says how many members of a group could approve.
type MemberCounts struct {
	// Enabled is the number of current members that are enabled: not disabled
	// and not ended. A disabled principal cannot approve.
	Enabled int64
	// Persons is the number of those members that are persons. Only a person
	// signs in to the console, so only a person gives the fresh authentication
	// that an approval window asks for.
	Persons int64
}

// GroupRef names one group by its immutable ID. The name is the current name
// of the group, and a rename changes it. Only the ID is the identity.
type GroupRef struct {
	ID   string
	Name string
}

// GroupResolver finds a group by its name.
type GroupResolver interface {
	// ResolveGroup returns the group that is called name now, without regard
	// to case. It returns ErrInvalid when name is not a valid group name, and
	// ErrNotFound when no group has that name.
	ResolveGroup(ctx context.Context, name string) (GroupRef, error)
}

// ApproverDirectory reads the groups that approvers name. The config service
// uses it to resolve a group name to the ID of the group at save, and to
// refuse an approver list that no one can meet. Package approverdirectorytest
// holds the conformance suite of an adapter.
type ApproverDirectory interface {
	GroupResolver
	// GroupByID returns the group with the ID id, with its current name and
	// the counts of its current members. A member counts when its membership
	// is active and its principal is not disabled. It returns ErrNotFound
	// when no group has that ID, also for a group that someone deleted and
	// made again with the same name: the new group has another ID. A group
	// with no member answers zero counts and no error.
	GroupByID(ctx context.Context, id string) (GroupRef, MemberCounts, error)
}

// ConfigService reads and writes the approval defaults and the approval
// rules of the scopes that hold a namespace. It reads the fence of the
// namespace, completes the audit entry of the caller, and calls the Store,
// which checks the fence and appends the entry in the transaction of the
// write.
//
// The service does not check who may call. The adapter of the transport
// checks the action of the route before it calls.
type ConfigService struct {
	store  Store
	ns     NamespaceSource
	groups ApproverDirectory
	now    func() time.Time
}

// ConfigServiceConfig holds the ports of a ConfigService.
type ConfigServiceConfig struct {
	Store      Store
	Namespaces NamespaceSource
	// Approvers reads the groups that approvers name.
	Approvers ApproverDirectory
	// Now is the clock, and time.Now when nil.
	Now func() time.Time
}

// NewConfigService returns a ConfigService. It returns ErrInvalid when a
// port is nil.
func NewConfigService(cfg ConfigServiceConfig) (*ConfigService, error) {
	if cfg.Store == nil || cfg.Namespaces == nil || cfg.Approvers == nil {
		return nil, fmt.Errorf(
			"%w: the gate config service needs a store, a namespace source and an approver directory", ErrInvalid,
		)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &ConfigService{store: cfg.Store, ns: cfg.Namespaces, groups: cfg.Approvers, now: now}, nil
}

// Audit actions of the writes of this service. They name the entry only. The
// authorization action of the route is a different value.
const (
	AuditSetApprovalDefault    = "namespace:set-approval-default"
	AuditDeleteApprovalDefault = "namespace:delete-approval-default"
	AuditSetApprovalRule       = "namespace:set-approval-rule"
	AuditDeleteApprovalRule    = "namespace:delete-approval-rule"
)

// ApprovalDefault returns the approval default of scope. It returns
// ErrNotFound when scope sets none.
func (s *ConfigService) ApprovalDefault(ctx context.Context, scope Scope) (ApprovalDefault, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return ApprovalDefault{}, err
	}

	d, err := s.store.Settings().ApprovalDefault(ctx, scope)
	if err != nil {
		return ApprovalDefault{}, fmt.Errorf("read the approval default: %w", err)
	}

	if d.Approvers, err = s.currentNames(ctx, d.Approvers); err != nil {
		return ApprovalDefault{}, err
	}

	return d, nil
}

// PutApprovalDefault stores d at d.Scope and returns it with its new version.
// With prev 0, d must be new, else prev is the stored version. e holds the
// caller. The service sets its namespace, time, object, kind, outcome and
// changes. It returns ErrInvalid for a scope with no approval default or a
// default that is not valid, ErrConflict for a stale version, and the fence
// errors of Objects.
func (s *ConfigService) PutApprovalDefault(
	ctx context.Context, d ApprovalDefault, prev int64, e audit.Entry,
) (ApprovalDefault, error) {
	if err := requireNamespaceScope(d.Scope); err != nil {
		return ApprovalDefault{}, err
	}

	if err := d.Validate(); err != nil {
		return ApprovalDefault{}, err
	}

	approvers, err := s.checkApprovers(ctx, d.Approvers, d.Count, d.FreshAuth)
	if err != nil {
		return ApprovalDefault{}, err
	}

	d.Approvers = approvers

	fence, entry, err := s.prepare(ctx, d.Scope, e, d.Scope.Key()+"/approval-default", AuditSetApprovalDefault)
	if err != nil {
		return ApprovalDefault{}, err
	}

	entry.Changes = putChanges("approval-default", prev)

	version, err := s.store.Settings().PutApprovalDefault(ctx, d, prev, fence, entry)
	if err != nil {
		return ApprovalDefault{}, fmt.Errorf("store the approval default: %w", err)
	}

	d.Version = version

	return d, nil
}

// DeleteApprovalDefault removes the approval default of scope when its
// version is prev. It returns ErrNotFound when scope sets none and
// ErrConflict for another version.
func (s *ConfigService) DeleteApprovalDefault(
	ctx context.Context, scope Scope, prev int64, e audit.Entry,
) error {
	if err := requireNamespaceScope(scope); err != nil {
		return err
	}

	fence, entry, err := s.prepare(ctx, scope, e, scope.Key()+"/approval-default", AuditDeleteApprovalDefault)
	if err != nil {
		return err
	}

	entry.Changes = deleteChanges("approval-default", prev)

	if err := s.store.Settings().DeleteApprovalDefault(ctx, scope, prev, fence, entry); err != nil {
		return fmt.Errorf("delete the approval default: %w", err)
	}

	return nil
}

// ApprovalRules returns the approval rules stored at scope, ordered by ID.
// It returns none of a wider scope.
func (s *ConfigService) ApprovalRules(ctx context.Context, scope Scope) ([]ApprovalRule, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return nil, err
	}

	rules, err := s.store.ApprovalRules().List(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list the approval rules: %w", err)
	}

	for i := range rules {
		if rules[i].Approvers, err = s.currentNames(ctx, rules[i].Approvers); err != nil {
			return nil, err
		}
	}

	return rules, nil
}

// ApprovalRule returns the approval rule id of scope, or ErrNotFound.
func (s *ConfigService) ApprovalRule(ctx context.Context, scope Scope, id ID) (ApprovalRule, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return ApprovalRule{}, err
	}

	r, err := s.store.ApprovalRules().Get(ctx, scope, id)
	if err != nil {
		return ApprovalRule{}, fmt.Errorf("read the approval rule: %w", err)
	}

	if r.Approvers, err = s.currentNames(ctx, r.Approvers); err != nil {
		return ApprovalRule{}, err
	}

	return r, nil
}

// PutApprovalRule stores r at r.Scope and returns it with its new version.
// The version rules and the entry follow PutApprovalDefault. It returns
// ErrFinal when a wider scope holds a rule with the same ID, ErrInvalid when
// a narrower scope holds one, and ErrLimit above MaxApprovalRulesPerScope.
func (s *ConfigService) PutApprovalRule(
	ctx context.Context, r ApprovalRule, prev int64, e audit.Entry,
) (ApprovalRule, error) {
	if err := requireNamespaceScope(r.Scope); err != nil {
		return ApprovalRule{}, err
	}

	if err := r.Validate(); err != nil {
		return ApprovalRule{}, err
	}

	approvers, err := s.checkApprovers(ctx, r.Approvers, r.Count, r.FreshAuth)
	if err != nil {
		return ApprovalRule{}, err
	}

	r.Approvers = approvers

	object := r.Scope.Key() + "/approval-rules/" + string(r.ID)

	fence, entry, err := s.prepare(ctx, r.Scope, e, object, AuditSetApprovalRule)
	if err != nil {
		return ApprovalRule{}, err
	}

	entry.Changes = putChanges("approval-rule/"+string(r.ID), prev)

	version, err := s.store.ApprovalRules().Put(ctx, r, prev, fence, entry)
	if err != nil {
		return ApprovalRule{}, fmt.Errorf("store the approval rule: %w", err)
	}

	r.Version = version

	return r, nil
}

// DeleteApprovalRule removes ref.ID of ref.Scope when its version is
// ref.Version. It returns ErrNotFound for an unknown rule, ErrConflict for
// another version, and ErrInvalid when a stored gate names the rule.
func (s *ConfigService) DeleteApprovalRule(ctx context.Context, ref ObjectRef, e audit.Entry) error {
	if err := requireNamespaceScope(ref.Scope); err != nil {
		return err
	}

	object := ref.Scope.Key() + "/approval-rules/" + string(ref.ID)

	fence, entry, err := s.prepare(ctx, ref.Scope, e, object, AuditDeleteApprovalRule)
	if err != nil {
		return err
	}

	entry.Changes = deleteChanges("approval-rule/"+string(ref.ID), ref.Version)

	if err := s.store.ApprovalRules().Delete(ctx, ref, fence, entry); err != nil {
		return fmt.Errorf("delete the approval rule: %w", err)
	}

	return nil
}

// requireNamespaceScope refuses a scope that has no namespace, or that no
// configuration route serves in this version. Defense in depth: the store
// refuses a scope that holds no configuration too, and the route builds the
// scope from the namespace of its path only.
func requireNamespaceScope(scope Scope) error {
	switch scope.Level() {
	case LevelNamespace, LevelCollection, LevelUnit:
		return nil
	case LevelInstallation, LevelBlueprint, LevelProfile:
		return fmt.Errorf("%w: a %s scope holds no approval rule or approval default through this service",
			ErrInvalid, scope.Level())
	default:
		return fmt.Errorf("%w: the scope is empty", ErrInvalid)
	}
}

// prepare reads the fence of the namespace of scope and completes e.
func (s *ConfigService) prepare(
	ctx context.Context, scope Scope, e audit.Entry, object, action string,
) (namespace.Fence, audit.Entry, error) {
	ns, err := s.ns.Get(ctx, scope.Namespace())
	if err != nil {
		return namespace.Fence{}, audit.Entry{}, fmt.Errorf("read the namespace %q: %w", scope.Namespace(), err)
	}

	e.At = s.now()
	e.Namespace = ns.Name
	e.Object = object
	e.Action = action
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess
	e.Changes = nil

	return ns.Fence(), e, nil
}

// putChanges returns the audit changes of a write from version prev to the
// next one. A prev of 0 means a new object, so the change has no old value.
func putChanges(key string, prev int64) []audit.Change {
	var old *string

	if prev > 0 {
		v := strconv.FormatInt(prev, 10)
		old = &v
	}

	return []audit.Change{{
		Key: key + ".version", Old: old, New: strconv.FormatInt(prev+1, 10), Layer: "", TakesEffect: "",
	}}
}

// deleteChanges returns the audit changes of a delete of version prev.
func deleteChanges(key string, prev int64) []audit.Change {
	old := strconv.FormatInt(prev, 10)

	return []audit.Change{{Key: key + ".version", Old: &old, New: "deleted", Layer: "", TakesEffect: ""}}
}
