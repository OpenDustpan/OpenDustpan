package selfservicesvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/internal/quota"
	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ParseDraft checks the document size and strictly parses a blueprint.
func ParseDraft(data []byte) (selfservice.Blueprint, error) {
	// Defense in depth: ParseBlueprint limits the document too; this check
	// stays so that a new caller cannot skip the service document limit.
	if len(data) > selfservice.MaxBlueprintBytes {
		return selfservice.Blueprint{}, fmt.Errorf("blueprint exceeds %d bytes: %w",
			selfservice.MaxBlueprintBytes, selfservice.ErrLimit)
	}

	b, err := selfservice.ParseBlueprint(data)
	if err != nil {
		return selfservice.Blueprint{}, fmt.Errorf("parse blueprint draft: %w: %w", selfservice.ErrInvalid, err)
	}

	// Defense in depth: a publish sets the Expr version from the server, and a
	// draft leaves it empty; this check stays so that a caller of ParseDraft
	// cannot store a version that the server did not compile.
	if b.ExprVersion != "" {
		return selfservice.Blueprint{}, fmt.Errorf("draft expression version is set by the server: %w",
			selfservice.ErrInvalid)
	}

	return b, nil
}

func validateDraft(ref selfservice.BlueprintRef, b *selfservice.Blueprint) error {
	if err := b.Validate(); err != nil {
		return fmt.Errorf("validate blueprint draft: %w", err)
	}

	// Defense in depth: the store checks publication names too; this check
	// stays so that a new caller cannot skip the authorized blueprint name.
	if b.Name != ref.Name {
		return fmt.Errorf("blueprint name differs from its reference: %w", selfservice.ErrInvalid)
	}

	return nil
}

type operation struct {
	service   *Blueprints
	principal identity.Principal
	op        selfservice.Op
	ref       selfservice.BlueprintRef
	version   string
}

func (s *Blueprints) operation(
	p identity.Principal, op selfservice.Op, ref selfservice.BlueprintRef, version string,
) operation {
	return operation{service: s, principal: p, op: op, ref: ref, version: version}
}

func (c *operation) authorize(ctx context.Context) error {
	// Defense in depth: the boundary validates references too; this check
	// stays so that a new caller cannot skip reference validation.
	if _, err := namespace.ParseName(c.ref.Namespace.String()); err != nil {
		return fmt.Errorf("blueprint namespace: %w: %w", selfservice.ErrInvalid, err)
	}

	if c.op != selfservice.OpListBlueprints && !selfservice.ValidName(c.ref.Name) {
		return fmt.Errorf("blueprint name: %w", selfservice.ErrInvalid)
	}

	target := selfservice.Target{
		Unit: identity.Object{}, Namespace: c.ref.Namespace, Blueprint: c.ref.Name, Operation: "", BreakGlass: false,
	}
	requirements, err := selfservice.Requirements(c.op, target)
	if err != nil {
		return fmt.Errorf("blueprint requirements: %w", err)
	}

	for i := range requirements {
		requirement := &requirements[i]
		authorizer := c.service.cfg.Authorizer
		if err := authorizer.Authorize(ctx, c.principal, requirement.Action, requirement.Object); err != nil {
			return fmt.Errorf("authorize blueprint operation: %w", err)
		}
	}

	return nil
}

func (s *Blueprints) authorizeSources(
	ctx context.Context, p identity.Principal, ns namespace.Name, b *selfservice.Blueprint,
) error {
	// The operation already checked the first requirement on the blueprint.
	requirements := selfservice.PublishRequirements(ns, b)[1:]
	for i := range requirements {
		requirement := &requirements[i]
		if err := s.cfg.Authorizer.Authorize(ctx, p, requirement.Action, requirement.Object); err != nil {
			return fmt.Errorf("authorize blueprint source principal: %w", err)
		}
	}

	return nil
}

func (s *Blueprints) compile(b *selfservice.Blueprint) error {
	if err := s.cfg.Evaluator.Compile(b.UnitName, selfservice.ResultString); err != nil {
		return fmt.Errorf("compile blueprint unit name: %w", err)
	}

	if err := s.compileInputs(b.Inputs); err != nil {
		return err
	}

	for i, rule := range b.Policy {
		if err := s.cfg.Evaluator.Compile(rule.When, selfservice.ResultBool); err != nil {
			return fmt.Errorf("compile blueprint policy rule %d: %w", i, err)
		}
	}

	for i, rule := range b.Quotas {
		if rule.Kind == quota.KindExpr {
			if err := s.cfg.Evaluator.Compile(rule.Rule, selfservice.ResultBool); err != nil {
				return fmt.Errorf("compile blueprint quota rule %d: %w", i, err)
			}
		}
	}

	return nil
}

func (s *Blueprints) compileInputs(inputs []selfservice.Input) error {
	for i := range inputs {
		input := &inputs[i]
		for j, rule := range input.Validate {
			if err := s.cfg.Evaluator.Compile(rule.Rule, selfservice.ResultBool); err != nil {
				return fmt.Errorf("compile input %s rule %d: %w", input.Name, j, err)
			}
		}
	}

	return nil
}

func (s *Blueprints) fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error) {
	fence, err := s.cfg.Fences.Fence(ctx, ns)
	if err != nil {
		return namespace.Fence{}, fmt.Errorf("read blueprint namespace fence: %w", err)
	}

	// Defense in depth: the store checks the fence too; this check stays so
	// that a new caller cannot skip the namespace of the authorized object.
	if fence.Namespace != ns {
		return namespace.Fence{}, fmt.Errorf("blueprint namespace fence: %w", namespace.ErrFenced)
	}

	return fence, nil
}

const failureAuditTimeout = 5 * time.Second

// fail records only the operation metadata, never the blueprint or error text.
func (c *operation) fail(ctx context.Context, err error) error {
	outcome := audit.OutcomeFailure
	if errors.Is(err, identity.ErrDenied) {
		outcome = audit.OutcomeDenied
		if c.op == selfservice.OpListBlueprints || c.op == selfservice.OpReadBlueprint {
			err = fmt.Errorf("read blueprint: %w", selfservice.ErrNotFound)
		}
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failureAuditTimeout)
	defer cancel()

	fence, auditErr := c.service.fence(ctx, c.ref.Namespace)
	if auditErr == nil {
		_, auditErr = c.service.cfg.Audit.Append(ctx, fence, c.entry(outcome))
	}

	if auditErr != nil {
		return errors.Join(err, fmt.Errorf("audit blueprint failure: %w", auditErr))
	}

	return err
}

func (c *operation) entry(outcome audit.Outcome) audit.Entry {
	action := selfservice.ActionBlueprintPublish
	kind := audit.KindChange
	if c.op == selfservice.OpListBlueprints || c.op == selfservice.OpReadBlueprint {
		action, kind = selfservice.ActionBlueprintRead, audit.KindReadAccess
	}
	if c.op == selfservice.OpRetire {
		action = selfservice.ActionBlueprintRetire
	}

	object := c.ref.String()
	if c.ref.Name == "" {
		object = c.ref.Namespace.String()
	}

	p := c.principal
	return audit.Entry{
		At:            c.service.cfg.Clock.Now().UTC(),
		Namespace:     c.ref.Namespace,
		Subject:       p.Subject,
		IDPSubject:    "",
		TokenID:       p.TokenID,
		Action:        string(action),
		Object:        object,
		Version:       c.version,
		ValuePath:     "",
		ValueCount:    nil,
		Changes:       nil,
		SourceIP:      "",
		UserAgent:     "",
		Channel:       audit.ChannelAPI,
		Kind:          kind,
		Outcome:       outcome,
		RequestID:     "",
		ObjectID:      "",
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		PrincipalKind: string(p.Kind),
		JobID:         p.JobID,
		Reason:        string(c.op),
	}
}
