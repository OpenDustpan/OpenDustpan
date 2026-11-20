package gatestore

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/gate"
)

// errUnknownKind reports a stored row of a kind that this version does not
// know.
var errUnknownKind = errors.New("a stored row has an unknown kind")

// Snapshot implements gate.Store. One read transaction serves every scope, so
// the answer is one snapshot of the configuration.
func (s *Store) Snapshot(ctx context.Context, scopes []gate.Scope) (gate.Config, error) {
	var distinct []gate.Scope

	for _, scope := range scopes {
		if scope.IsZero() {
			return gate.Config{}, fmt.Errorf("%w: a snapshot needs a scope", gate.ErrInvalid)
		}

		if !slices.Contains(distinct, scope) {
			distinct = append(distinct, scope)
		}
	}

	var cfg gate.Config

	err := s.run.Read(ctx, func(tx Tx) error {
		for _, scope := range distinct {
			if err := readScope(ctx, tx, scope, &cfg); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return gate.Config{}, err
	}

	return cfg, nil
}

// readScope adds every object and setting of scope to cfg.
func readScope(ctx context.Context, tx Tx, scope gate.Scope, cfg *gate.Config) error {
	rows, err := tx.ListScope(ctx, scope)
	if err != nil {
		return err
	}

	for _, row := range rows {
		if err := addToConfig(cfg, scope, row); err != nil {
			return err
		}
	}

	return nil
}

// addToConfig decodes row, held by scope, into the list of its kind.
func addToConfig(cfg *gate.Config, scope gate.Scope, row Row) error {
	switch row.Kind {
	case KindGate:
		return appendDecoded(&cfg.Gates, scope, row, decodeGate)
	case KindAccessLimit:
		return appendDecoded(&cfg.AccessLimits, scope, row, decodeAccessLimit)
	case KindApprovalRule:
		return appendDecoded(&cfg.ApprovalRules, scope, row, decodeApprovalRule)
	case KindOwnershipRule:
		return appendDecoded(&cfg.OwnershipRules, scope, row, decodeOwnershipRule)
	case KindPathSet:
		return appendDecoded(&cfg.PathSets, scope, row, decodePathSet)
	case KindApprovalDefault:
		return appendDecoded(&cfg.ApprovalDefaults, scope, row, decodeApprovalDefault)
	case KindCarryOver:
		return appendDecoded(&cfg.CarryOver, scope, row, decodeCarryOver)
	default:
		return fmt.Errorf("%w: %s/%s has the kind %q", errUnknownKind, scope, row.ID, row.Kind)
	}
}

func appendDecoded[T any](
	list *[]T, scope gate.Scope, row Row, decode func(gate.Scope, Row) (T, error),
) error {
	v, err := decode(scope, row)
	if err != nil {
		return err
	}

	*list = append(*list, v)

	return nil
}
