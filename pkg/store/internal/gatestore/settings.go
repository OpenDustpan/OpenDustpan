package gatestore

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Settings is gate.Settings over a Runner. A scope holds each setting once,
// as a row of the kind with an empty ID.
type Settings struct {
	run       Runner
	approvers approverChecker
}

var _ gate.Settings = (*Settings)(nil)

// settingName names the setting of a scope in an error.
func settingName(scope gate.Scope, kind string) string {
	return "the " + kind + " of " + scope.Key()
}

func requireDefaultScope(scope gate.Scope) error {
	if !scope.HoldsApprovalDefault() {
		return fmt.Errorf("%w: a %s scope holds no approval default", gate.ErrInvalid, scope.Level())
	}

	return nil
}

// ApprovalDefault implements gate.Settings.
func (s *Settings) ApprovalDefault(ctx context.Context, scope gate.Scope) (gate.ApprovalDefault, error) {
	var out gate.ApprovalDefault

	if scope.IsZero() {
		return out, fmt.Errorf("%w: the scope is empty", gate.ErrInvalid)
	}

	err := s.run.Read(ctx, func(tx Tx) error {
		row, ok, err := tx.Get(ctx, scope, KindApprovalDefault, "")
		if err != nil {
			return err
		}

		if !ok {
			return fmt.Errorf("%w: %s sets no approval default", gate.ErrNotFound, scope)
		}

		out, err = decodeApprovalDefault(scope, row)

		return err
	})
	if err != nil {
		return gate.ApprovalDefault{}, err
	}

	return out, nil
}

// PutApprovalDefault implements gate.Settings.
func (s *Settings) PutApprovalDefault(
	ctx context.Context, d gate.ApprovalDefault, prev int64, fence namespace.Fence, e audit.Entry,
) (int64, error) {
	if err := requireDefaultScope(d.Scope); err != nil {
		return 0, err
	}

	if err := checkFence(d.Scope, fence); err != nil {
		return 0, err
	}

	// Defense in depth: the service validates the default too; this check
	// stays so that a new caller of the port cannot store a default with a
	// count of 0.
	if err := d.Validate(); err != nil {
		return 0, err
	}

	if err := s.approvers.check(ctx, d.Approvers); err != nil {
		return 0, err
	}

	var version int64

	err := s.run.Write(ctx, d.Scope, fence, e, func(tx Tx) error {
		if err := checkSettingVersion(ctx, tx, d.Scope, KindApprovalDefault, prev); err != nil {
			return err
		}

		if err := requireReads(ctx, tx, d.Scope, d.Reads); err != nil {
			return err
		}

		body, refs, err := encodeApprovalDefault(d)
		if err != nil {
			return err
		}

		w := rowWrite{
			scope: d.Scope, row: Row{Kind: KindApprovalDefault, ID: "", Version: prev + 1, Body: body},
			prev: prev, refs: refs,
		}
		version, err = w.store(ctx, tx)

		return err
	})
	if err != nil {
		return 0, err
	}

	return version, nil
}

// checkSettingVersion applies the version rules of Put to the setting of the
// kind in scope.
func checkSettingVersion(ctx context.Context, tx Tx, scope gate.Scope, kind string, prev int64) error {
	row, found, err := tx.Get(ctx, scope, kind, "")
	if err != nil {
		return err
	}

	return checkVersion(settingName(scope, kind), prev, row.Version, found)
}

// DeleteApprovalDefault implements gate.Settings.
func (s *Settings) DeleteApprovalDefault(
	ctx context.Context, scope gate.Scope, prev int64, fence namespace.Fence, e audit.Entry,
) error {
	if err := requireDefaultScope(scope); err != nil {
		return err
	}

	if err := checkFence(scope, fence); err != nil {
		return err
	}

	return s.run.Write(ctx, scope, fence, e, func(tx Tx) error {
		return removeSetting(ctx, tx, scope, KindApprovalDefault, prev)
	})
}

// removeSetting deletes the setting of scope when its version is prev.
func removeSetting(ctx context.Context, tx Tx, scope gate.Scope, kind string, prev int64) error {
	row, found, err := tx.Get(ctx, scope, kind, "")
	if err != nil {
		return err
	}

	if !found {
		return fmt.Errorf("%w: %s", gate.ErrNotFound, settingName(scope, kind))
	}

	if row.Version != prev {
		return fmt.Errorf("%w: %s is at version %d, not %d", gate.ErrConflict, settingName(scope, kind), row.Version,
			prev)
	}

	ok, err := tx.Delete(ctx, scope, kind, "", prev)
	if err != nil {
		return err
	}

	if !ok {
		return fmt.Errorf("%w: %s changed in the transaction", gate.ErrConflict, settingName(scope, kind))
	}

	return nil
}

// CarryOver implements gate.Settings.
func (s *Settings) CarryOver(ctx context.Context, scope gate.Scope) (gate.CarryOverSetting, error) {
	if scope.IsZero() {
		return gate.CarryOverSetting{}, fmt.Errorf("%w: the scope is empty", gate.ErrInvalid)
	}

	out := gate.CarryOverSetting{Scope: scope, Version: 0, Value: gate.CarryOverOff}

	err := s.run.Read(ctx, func(tx Tx) error {
		row, ok, err := tx.Get(ctx, scope, KindCarryOver, "")
		if err != nil || !ok {
			return err
		}

		out, err = decodeCarryOver(scope, row)

		return err
	})
	if err != nil {
		return gate.CarryOverSetting{}, err
	}

	return out, nil
}

// PutCarryOver implements gate.Settings.
func (s *Settings) PutCarryOver(
	ctx context.Context, c gate.CarryOverSetting, prev int64, fence namespace.Fence, e audit.Entry,
) (int64, error) {
	if err := checkFence(c.Scope, fence); err != nil {
		return 0, err
	}

	if _, err := gate.ParseCarryOver(c.Scope, string(c.Value)); err != nil {
		return 0, err
	}

	var version int64

	err := s.run.Write(ctx, c.Scope, fence, e, func(tx Tx) error {
		var err error

		version, err = putCarryOver(ctx, tx, c, prev)

		return err
	})
	if err != nil {
		return 0, err
	}

	return version, nil
}

// putCarryOver stores c in the transaction tx. A setting of off deletes the
// stored setting, if any, and has version 0.
func putCarryOver(ctx context.Context, tx Tx, c gate.CarryOverSetting, prev int64) (int64, error) {
	row, found, err := tx.Get(ctx, c.Scope, KindCarryOver, "")
	if err != nil {
		return 0, err
	}

	if versionErr := checkVersion(settingName(c.Scope, KindCarryOver), prev, row.Version, found); versionErr != nil {
		return 0, versionErr
	}

	if c.Value == gate.CarryOverOff {
		if found {
			err = removeSetting(ctx, tx, c.Scope, KindCarryOver, prev)
		}

		return 0, err
	}

	body, err := encodeCarryOver(c.Value)
	if err != nil {
		return 0, err
	}

	w := rowWrite{
		scope: c.Scope, row: Row{Kind: KindCarryOver, ID: "", Version: prev + 1, Body: body}, prev: prev, refs: nil,
	}

	return w.store(ctx, tx)
}
