package gatestore

import (
	"context"
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
)

// KindReader reads the installation kind of the database without a claim.
// The store adapter implements it over its own database.
type KindReader interface {
	// ReadKind returns the kind that the database holds, or
	// installation.ErrUnavailable.
	ReadKind(ctx context.Context) (installation.Kind, error)
}

// approverChecker validates the users of the approvers of a write. A write
// of a rule or a default calls it before the transaction opens.
type approverChecker struct {
	kinds KindReader
}

// checkObject validates the approvers of obj when obj is an approval rule or
// an ownership rule. Each other object holds none.
func (c approverChecker) checkObject(ctx context.Context, obj any) error {
	switch v := obj.(type) {
	case gate.ApprovalRule:
		return c.check(ctx, v.Approvers)
	case gate.OwnershipRule:
		return c.check(ctx, v.Approvers)
	default:
		return nil
	}
}

// check returns an error that wraps gate.ErrInvalid for an approver that
// gate.ValidateApprovers refuses. It reads the installation kind only for a
// dev subject. A kind that is missing or cannot be read counts as production,
// and a read that fails refuses the write: fail closed.
func (c approverChecker) check(ctx context.Context, a gate.Approvers) error {
	production, err := c.production(ctx, a)
	if err != nil {
		return err
	}

	return gate.ApproverPolicy{Production: production, IsDev: principal.IsDevSubject}.Validate(a)
}

func (c approverChecker) production(ctx context.Context, a gate.Approvers) (bool, error) {
	hasDev := slices.ContainsFunc(a.Users, principal.IsDevSubject)

	if !hasDev || c.kinds == nil {
		return true, nil
	}

	kind, err := c.kinds.ReadKind(ctx)
	if err != nil {
		return true, fmt.Errorf("%w: read the installation kind to check the approvers: %w", gate.ErrUnavailable, err)
	}

	return kind != installation.KindDev, nil
}
