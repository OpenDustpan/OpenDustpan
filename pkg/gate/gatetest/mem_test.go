package gatetest_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errNotInFake reports a port method that the store in memory does not
// hold. The executable suite does not call one.
var errNotInFake = errors.New("not part of the store in memory")

// memDB is the shared state of the store in memory: the namespaces with
// their failover versions, the objects of each kind and the audit entries.
type memDB struct {
	mu      sync.Mutex
	fences  map[namespace.Name]int64
	entries []audit.Entry
	// dev is true for the store of a dev installation.
	dev bool
}

// memStore is a gate.Store in memory. It holds the rules of gate.Objects
// for one scope, and nothing else.
type memStore struct {
	db             *memDB
	gates          *memObjects[gate.Gate]
	accessLimits   *memObjects[gate.AccessLimit]
	approvalRules  *memObjects[gate.ApprovalRule]
	ownershipRules *memObjects[gate.OwnershipRule]
	pathSets       *memObjects[gate.PathSet]
}

func newMemStore() *memStore {
	return newMemStoreOfKind(false)
}

func newMemStoreOfKind(dev bool) *memStore {
	db := &memDB{mu: sync.Mutex{}, fences: map[namespace.Name]int64{}, entries: nil, dev: dev}

	return &memStore{
		db:             db,
		gates:          newMemObjects[gate.Gate](db, gate.MaxGatesPerScope),
		accessLimits:   newMemObjects[gate.AccessLimit](db, gate.MaxAccessLimitsPerScope),
		approvalRules:  newMemObjects[gate.ApprovalRule](db, gate.MaxApprovalRulesPerScope),
		ownershipRules: newMemObjects[gate.OwnershipRule](db, gate.MaxOwnershipRulesPerScope),
		pathSets:       newMemObjects[gate.PathSet](db, gate.MaxPathSetsPerScope),
	}
}

func (s *memStore) Gates() gate.Objects[gate.Gate]                   { return s.gates }
func (s *memStore) AccessLimits() gate.Objects[gate.AccessLimit]     { return s.accessLimits }
func (s *memStore) ApprovalRules() gate.Objects[gate.ApprovalRule]   { return s.approvalRules }
func (s *memStore) OwnershipRules() gate.Objects[gate.OwnershipRule] { return s.ownershipRules }
func (s *memStore) PathSets() gate.Objects[gate.PathSet]             { return s.pathSets }

// Settings returns no port: the executable suite does not call it.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *memStore) Settings() gate.Settings { return nil }

// Records returns no port: the executable suite does not call it.
//
//nolint:ireturn // The method implements gate.Store, which returns the port.
func (s *memStore) Records() gate.Records { return nil }

func (s *memStore) Snapshot(context.Context, []gate.Scope) (gate.Config, error) {
	return gate.Config{}, errNotInFake
}

// CreateNamespace makes the namespace name with failover version 1.
func (s *memStore) CreateNamespace(_ context.Context, name namespace.Name) (namespace.Fence, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	s.db.fences[name] = 1

	return namespace.Fence{Namespace: name, FailoverVersion: 1}, nil
}

// BumpFence adds 1 to the failover version of the namespace of fence.
func (s *memStore) BumpFence(_ context.Context, fence namespace.Fence) (namespace.Fence, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()

	s.db.fences[fence.Namespace]++

	return namespace.Fence{Namespace: fence.Namespace, FailoverVersion: s.db.fences[fence.Namespace]}, nil
}

// memObjects is gate.Objects[T] in memory.
type memObjects[T gate.Object] struct {
	db    *memDB
	limit int
	byKey map[string]map[gate.ID]T
}

func newMemObjects[T gate.Object](db *memDB, limit int) *memObjects[T] {
	return &memObjects[T]{db: db, limit: limit, byKey: map[string]map[gate.ID]T{}}
}

func (m *memObjects[T]) Get(_ context.Context, scope gate.Scope, id gate.ID) (T, error) {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	obj, ok := m.byKey[scope.Key()][id]
	if !ok {
		return obj, fmt.Errorf("%w: %s/%s", gate.ErrNotFound, scope, id)
	}

	return obj, nil
}

func (m *memObjects[T]) List(_ context.Context, scope gate.Scope) ([]T, error) {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	out := make([]T, 0, len(m.byKey[scope.Key()]))
	for _, obj := range m.byKey[scope.Key()] {
		out = append(out, obj)
	}

	slices.SortFunc(out, func(a, b T) int { return compareIDs(a.Ref().ID, b.Ref().ID) })

	return out, nil
}

func (m *memObjects[T]) ListEveryScope(_ context.Context) ([]T, error) {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	var out []T

	for _, objs := range m.byKey {
		for _, obj := range objs {
			out = append(out, obj)
		}
	}

	slices.SortFunc(out, func(a, b T) int {
		ar, br := a.Ref(), b.Ref()
		if c := strings.Compare(ar.Scope.Key(), br.Scope.Key()); c != 0 {
			return c
		}

		return compareIDs(ar.ID, br.ID)
	})

	return out, nil
}

func compareIDs(a, b gate.ID) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func (m *memObjects[T]) Put(
	_ context.Context,
	obj T,
	prev int64,
	fence namespace.Fence,
	e audit.Entry,
) (int64, error) {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	ref := obj.Ref()

	if err := checkWrite(m.db, obj, fence, e); err != nil {
		return 0, err
	}

	objs := m.byKey[ref.Scope.Key()]
	old, ok := objs[ref.ID]

	switch {
	case prev == 0 && ok:
		return 0, fmt.Errorf("%w: %s/%s exists", gate.ErrConflict, ref.Scope, ref.ID)
	case prev > 0 && !ok:
		return 0, fmt.Errorf("%w: %s/%s", gate.ErrNotFound, ref.Scope, ref.ID)
	case prev > 0 && old.Ref().Version != prev:
		return 0, fmt.Errorf("%w: %s/%s", gate.ErrConflict, ref.Scope, ref.ID)
	case !ok && len(objs) >= m.limit:
		return 0, fmt.Errorf("%w: %d objects at %s", gate.ErrLimit, m.limit, ref.Scope)
	default:
	}

	if objs == nil {
		objs = map[gate.ID]T{}
		m.byKey[ref.Scope.Key()] = objs
	}

	objs[ref.ID] = withVersion(obj, prev+1)
	m.db.entries = append(m.db.entries, e)

	return prev + 1, nil
}

func (m *memObjects[T]) Delete(_ context.Context, ref gate.ObjectRef, fence namespace.Fence, e audit.Entry) error {
	m.db.mu.Lock()
	defer m.db.mu.Unlock()

	if err := m.db.check(ref.Scope, fence, e); err != nil {
		return err
	}

	old, ok := m.byKey[ref.Scope.Key()][ref.ID]

	switch {
	case !ok:
		return fmt.Errorf("%w: %s/%s", gate.ErrNotFound, ref.Scope, ref.ID)
	case old.Ref().Version != ref.Version:
		return fmt.Errorf("%w: %s/%s", gate.ErrConflict, ref.Scope, ref.ID)
	default:
	}

	delete(m.byKey[ref.Scope.Key()], ref.ID)
	m.db.entries = append(m.db.entries, e)

	return nil
}

// check holds the scope, fence and entry rules of gate.Objects.
func (db *memDB) check(scope gate.Scope, fence namespace.Fence, e audit.Entry) error {
	if !scope.HoldsConfig() {
		return fmt.Errorf("%w: %s holds no configuration routes", gate.ErrInvalid, scope)
	}

	entryNS := scope.Namespace()
	if scope.Level() == gate.LevelInstallation {
		entryNS = audit.ServerScope
	}

	if err := db.checkFence(scope, fence); err != nil {
		return err
	}

	if err := e.Validate(); err != nil {
		return fmt.Errorf("gate entry: %w", err)
	}

	if e.Namespace != entryNS {
		return fmt.Errorf("%w: entry of %q for %s", audit.ErrInvalidEntry, e.Namespace, scope)
	}

	return nil
}

// checkFence holds the fence rules of gate.Objects.
func (db *memDB) checkFence(scope gate.Scope, fence namespace.Fence) error {
	ns := scope.Namespace()

	switch current, ok := db.fences[ns]; {
	case scope.Level() == gate.LevelInstallation:
		if fence != (namespace.Fence{Namespace: "", FailoverVersion: 0}) {
			return fmt.Errorf("%w: the installation takes the zero fence", gate.ErrInvalid)
		}

		return nil
	case fence.Namespace != ns:
		return fmt.Errorf("%w: fence of %q for %s", gate.ErrInvalid, fence.Namespace, scope)
	case !ok:
		return fmt.Errorf("%w: %s", namespace.ErrNotFound, ns)
	case current != fence.FailoverVersion:
		return fmt.Errorf("%w: %s", namespace.ErrFenced, ns)
	default:
		return nil
	}
}

// withVersion returns obj with version v.
func withVersion[T gate.Object](obj T, v int64) T {
	var out any

	switch o := any(obj).(type) {
	case gate.Gate:
		o.Version, o.State = v, ""
		out = o
	case gate.AccessLimit:
		o.Version = v
		out = o
	case gate.ApprovalRule:
		o.Version = v
		out = o
	case gate.OwnershipRule:
		o.Version = v
		out = o
	case gate.PathSet:
		o.Version = v
		out = o
	default:
	}

	typed, ok := out.(T)
	if !ok {
		return obj
	}

	return typed
}

// checkApprovers applies the rule of the users of the approvers to obj.
func (db *memDB) checkApprovers(obj any) error {
	var approvers gate.Approvers

	switch v := obj.(type) {
	case gate.ApprovalRule:
		approvers = v.Approvers
	case gate.OwnershipRule:
		approvers = v.Approvers
	default:
		return nil
	}

	return gate.ApproverPolicy{Production: !db.dev, IsDev: principal.IsDevSubject}.Validate(approvers)
}

// checkWrite checks the approvers, the fence and the entry of a write of obj.
func checkWrite[T gate.Object](db *memDB, obj T, fence namespace.Fence, e audit.Entry) error {
	if err := db.checkApprovers(obj); err != nil {
		return err
	}

	return db.check(obj.Ref().Scope, fence, e)
}
