package gatestore

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Objects is gate.Objects[T] over a Runner.
type Objects[T gate.Object] struct {
	run       Runner
	codec     codec[T]
	approvers approverChecker
}

var (
	_ gate.Objects[gate.Gate]          = (*Objects[gate.Gate])(nil)
	_ gate.Objects[gate.AccessLimit]   = (*Objects[gate.AccessLimit])(nil)
	_ gate.Objects[gate.ApprovalRule]  = (*Objects[gate.ApprovalRule])(nil)
	_ gate.Objects[gate.OwnershipRule] = (*Objects[gate.OwnershipRule])(nil)
	_ gate.Objects[gate.PathSet]       = (*Objects[gate.PathSet])(nil)
)

func newObjects[T gate.Object](run Runner, approvers approverChecker) *Objects[T] {
	return &Objects[T]{run: run, codec: codecOf[T](), approvers: approvers}
}

// checkFence returns gate.ErrInvalid when fence is not the fence of scope: the
// zero fence for the installation, and the fence of the namespace of scope
// for any other scope. It returns gate.ErrInvalid for a scope that takes no
// write.
func checkFence(scope gate.Scope, fence namespace.Fence) error {
	if !scope.HoldsConfig() {
		return fmt.Errorf("%w: a %s scope holds no configuration of its own", gate.ErrInvalid, scope.Level())
	}

	return checkFenceOf(scope.Namespace(), fence)
}

// checkFenceOf returns gate.ErrInvalid when fence is not the fence of ns,
// where ns is empty or audit.ServerScope for the installation.
func checkFenceOf(ns namespace.Name, fence namespace.Fence) error {
	want := namespace.Fence{Namespace: "", FailoverVersion: 0}
	if ns != "" && ns != audit.ServerScope {
		want.Namespace = ns
	}

	if fence.Namespace != want.Namespace || (want.Namespace == "" && fence.FailoverVersion != 0) {
		return fmt.Errorf("%w: the fence names %q, the scope needs the fence of %q", gate.ErrInvalid,
			fence.Namespace, want.Namespace)
	}

	return nil
}

func notFound(scope gate.Scope, id string) error {
	return fmt.Errorf("%w: %s/%s", gate.ErrNotFound, scope, id)
}

// Get implements gate.Objects.
func (o *Objects[T]) Get(ctx context.Context, scope gate.Scope, id gate.ID) (T, error) {
	var out T

	if scope.IsZero() {
		return out, fmt.Errorf("%w: the scope is empty", gate.ErrInvalid)
	}

	err := o.run.Read(ctx, func(tx Tx) error {
		row, ok, err := tx.Get(ctx, scope, o.codec.kind, string(id))
		if err != nil {
			return err
		}

		if !ok {
			return notFound(scope, string(id))
		}

		out, err = o.codec.decode(scope, row)

		return err
	})
	if err != nil {
		var zero T

		return zero, err
	}

	return out, nil
}

// List implements gate.Objects.
func (o *Objects[T]) List(ctx context.Context, scope gate.Scope) ([]T, error) {
	if scope.IsZero() {
		return nil, fmt.Errorf("%w: the scope is empty", gate.ErrInvalid)
	}

	var out []T

	err := o.run.Read(ctx, func(tx Tx) error {
		rows, err := tx.List(ctx, scope, o.codec.kind)
		if err != nil {
			return err
		}

		out = make([]T, 0, len(rows))

		for _, row := range rows {
			obj, derr := o.codec.decode(scope, row)
			if derr != nil {
				return derr
			}

			out = append(out, obj)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// ListEveryScope implements gate.Objects.
func (o *Objects[T]) ListEveryScope(ctx context.Context) ([]T, error) {
	var out []T

	err := o.run.Read(ctx, func(tx Tx) error {
		rows, err := tx.ListEveryScope(ctx, o.codec.kind)
		if err != nil {
			return err
		}

		out = make([]T, 0, len(rows))

		for _, row := range rows {
			scope, perr := ParseScope(row.ScopeKey)
			if perr != nil {
				return perr
			}

			obj, derr := o.codec.decode(scope, row.Row)
			if derr != nil {
				return derr
			}

			out = append(out, obj)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Put implements gate.Objects.
func (o *Objects[T]) Put(
	ctx context.Context, obj T, prev int64, fence namespace.Fence, e audit.Entry,
) (int64, error) {
	scope := obj.Ref().Scope
	if err := checkFence(scope, fence); err != nil {
		return 0, err
	}

	if err := validateObject(obj); err != nil {
		return 0, err
	}

	if err := o.approvers.checkObject(ctx, obj); err != nil {
		return 0, err
	}

	var version int64

	err := o.run.Write(ctx, scope, fence, e, func(tx Tx) error {
		v, err := o.write(ctx, tx, obj, prev, gate.Reason(e))
		version = v

		return err
	})
	if err != nil {
		return 0, err
	}

	return version, nil
}

// write stores obj in the transaction tx.
func (o *Objects[T]) write(ctx context.Context, tx Tx, obj T, prev int64, reason string) (int64, error) {
	ref := obj.Ref()

	current, found, err := o.current(ctx, tx, ref, prev)
	if err != nil {
		return 0, err
	}

	if !found {
		if roomErr := o.requireRoom(ctx, tx, ref.Scope); roomErr != nil {
			return 0, roomErr
		}
	}

	var previous *gate.Gate
	if found {
		previous = previousGate(current)
	}

	if checkErr := checkPut(ctx, tx, obj, previous, reason); checkErr != nil {
		return 0, checkErr
	}

	body, refs, err := o.codec.encode(obj)
	if err != nil {
		return 0, err
	}

	w := rowWrite{
		scope: ref.Scope, row: Row{Kind: o.codec.kind, ID: string(ref.ID), Version: prev + 1, Body: body},
		prev: prev, refs: refs,
	}

	return w.store(ctx, tx)
}

// current returns the stored object of ref, and false when the scope holds
// none. It applies the version rules of Put for prev.
func (o *Objects[T]) current(ctx context.Context, tx Tx, ref gate.ObjectRef, prev int64) (T, bool, error) {
	var none T

	id := string(ref.ID)

	row, found, err := tx.Get(ctx, ref.Scope, o.codec.kind, id)
	if err != nil {
		return none, false, err
	}

	if versionErr := checkVersion(ref.Scope.Key()+"/"+id, prev, row.Version, found); versionErr != nil {
		return none, false, versionErr
	}

	if !found {
		return none, false, nil
	}

	cur, err := o.codec.decode(ref.Scope, row)
	if err != nil {
		return none, false, err
	}

	return cur, true, nil
}

// requireRoom returns gate.ErrLimit when scope holds the most objects of the
// kind.
func (o *Objects[T]) requireRoom(ctx context.Context, tx Tx, scope gate.Scope) error {
	count, err := tx.Count(ctx, scope, o.codec.kind)
	if err != nil {
		return err
	}

	if count >= o.codec.limit {
		return fmt.Errorf("%w: %s holds %d %s objects, the most is %d", gate.ErrLimit, scope, count, o.codec.kind,
			o.codec.limit)
	}

	return nil
}

// checkVersion applies the version rules of Put to the object called what,
// which is stored at version stored or, when found is false, not stored.
func checkVersion(what string, prev, stored int64, found bool) error {
	switch {
	case prev == 0 && found:
		return fmt.Errorf("%w: %s exists at version %d", gate.ErrConflict, what, stored)
	case prev > 0 && !found:
		return fmt.Errorf("%w: %s", gate.ErrNotFound, what)
	case prev > 0 && stored != prev:
		return fmt.Errorf("%w: %s is at version %d, not %d", gate.ErrConflict, what, stored, prev)
	default:
		return nil
	}
}

// previousGate returns the stored object current as a gate, or nil when it is
// no gate.
func previousGate(current any) *gate.Gate {
	g, ok := current.(gate.Gate)
	if !ok {
		return nil
	}

	return &g
}

// rowWrite is one write of a row and of its references.
type rowWrite struct {
	scope gate.Scope
	row   Row
	// prev is the version that the row replaces, or 0 for a new row.
	prev int64
	refs []Ref
}

// store writes the row, as a new row when prev is 0, and replaces the
// references of the row. It returns the version of the row.
func (w rowWrite) store(ctx context.Context, tx Tx) (int64, error) {
	if err := w.insertOrUpdate(ctx, tx); err != nil {
		return 0, err
	}

	if err := tx.SetReferences(ctx, w.scope, w.row.Kind, w.row.ID, w.refs); err != nil {
		return 0, err
	}

	return w.row.Version, nil
}

func (w rowWrite) insertOrUpdate(ctx context.Context, tx Tx) error {
	at := time.Now()

	if w.prev == 0 {
		return tx.Insert(ctx, w.scope, w.row, at)
	}

	ok, err := tx.Update(ctx, w.scope, w.row, w.prev, at)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("%w: %s/%s changed in the transaction", gate.ErrConflict, w.scope, w.row.ID)
	}

	return nil
}

// Delete implements gate.Objects.
func (o *Objects[T]) Delete(ctx context.Context, ref gate.ObjectRef, fence namespace.Fence, e audit.Entry) error {
	if err := checkFence(ref.Scope, fence); err != nil {
		return err
	}

	return o.run.Write(ctx, ref.Scope, fence, e, func(tx Tx) error {
		return o.remove(ctx, tx, ref, gate.Reason(e))
	})
}

func (o *Objects[T]) remove(ctx context.Context, tx Tx, ref gate.ObjectRef, reason string) error {
	current, found, err := o.current(ctx, tx, ref, ref.Version)
	if err != nil {
		return err
	}

	if !found {
		return notFound(ref.Scope, string(ref.ID))
	}

	if checkErr := checkDelete(ctx, tx, current, reason); checkErr != nil {
		return checkErr
	}

	ok, err := tx.Delete(ctx, ref.Scope, o.codec.kind, string(ref.ID), ref.Version)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("%w: %s/%s changed in the transaction", gate.ErrConflict, ref.Scope, ref.ID)
	}

	return nil
}

// validateObject returns gate.ErrInvalid for an object whose ID or whose
// enumerated fields the grammar of package gate refuses.
func validateObject(obj any) error {
	switch v := obj.(type) {
	case gate.Gate:
		return validateGate(v)
	case gate.AccessLimit:
		_, err := gate.ParseID(string(v.ID))

		return err
	case gate.ApprovalRule:
		// Defense in depth: the service validates the rule too; this check
		// stays so that a new caller of the port cannot store a rule that
		// nobody can meet or that needs no approval.
		return v.Validate()
	case gate.OwnershipRule:
		if _, err := gate.ParseID(string(v.ID)); err != nil {
			return err
		}

		_, err := gate.ParseMode(string(v.Mode))

		return err
	case gate.PathSet:
		_, err := gate.ParseID(string(v.ID))

		return err
	default:
		return nil
	}
}

func validateGate(g gate.Gate) error {
	if _, err := gate.ParseGateID(string(g.ID)); err != nil {
		return err
	}

	if _, err := gate.ParseDecisionPoint(string(g.DecisionPoint)); err != nil {
		return err
	}

	if _, err := gate.ParseOutcome(string(g.Outcome)); err != nil {
		return err
	}

	if _, err := gate.ParseMode(string(g.Mode)); err != nil {
		return err
	}

	_, err := gate.ParseOnError(string(g.OnError))

	return err
}
