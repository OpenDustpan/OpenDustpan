// Package selfservicesvc checks access and validates blueprint changes before
// it sends them to the persistence port.
package selfservicesvc

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/selfservice"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ErrMissingDependency reports a Config without a required dependency.
var ErrMissingDependency = errors.New("missing dependency")

// Authorizer checks the objects of self-service operations. Blueprint objects
// have no identity.Object representation.
type Authorizer interface {
	// Authorize returns an error wrapping identity.ErrDenied for a denial.
	// Every other error also stops the operation.
	Authorize(ctx context.Context, p identity.Principal, action identity.Action, object selfservice.Object) error
}

// Clock supplies the time of a publication or an audit entry.
type Clock interface {
	Now() time.Time
}

// Fences reads the current fence for a namespace.
type Fences interface {
	Fence(ctx context.Context, ns namespace.Name) (namespace.Fence, error)
}

// Audit writes entries for operations that store no blueprint change.
type Audit interface {
	Append(ctx context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error)
}

// Config holds the required dependencies of Blueprints.
type Config struct {
	Store      selfservice.BlueprintStore
	Authorizer Authorizer
	Evaluator  selfservice.Evaluator
	Clock      Clock
	Audit      Audit
	Fences     Fences
}

// Blueprints reads drafts and versions, and checks each blueprint change.
type Blueprints struct {
	cfg Config
}

// New refuses a missing dependency, including an interface with a nil value.
func New(cfg Config) (*Blueprints, error) {
	dependencies := []struct {
		name  string
		value any
	}{
		{"store", cfg.Store},
		{"authorizer", cfg.Authorizer},
		{"evaluator", cfg.Evaluator},
		{"clock", cfg.Clock},
		{"audit", cfg.Audit},
		{"fences", cfg.Fences},
	}
	for _, dependency := range dependencies {
		if missing(dependency.value) {
			return nil, fmt.Errorf("%w: %s", ErrMissingDependency, dependency.name)
		}
	}

	return &Blueprints{cfg: cfg}, nil
}

func missing(value any) bool {
	if value == nil {
		return true
	}

	v := reflect.ValueOf(value)
	nilKinds := []reflect.Kind{
		reflect.Chan,
		reflect.Func,
		reflect.Interface,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice,
	}
	return slices.Contains(nilKinds, v.Kind()) && v.IsNil()
}

// ListBlueprints returns the newest versions after a namespace read check.
func (s *Blueprints) ListBlueprints(
	ctx context.Context, p identity.Principal, ns namespace.Name,
) ([]selfservice.Version, error) {
	ref := selfservice.BlueprintRef{Namespace: ns, Name: ""}
	call := s.operation(p, selfservice.OpListBlueprints, ref, "")

	if err := call.authorize(ctx); err != nil {
		return nil, call.fail(ctx, err)
	}

	out, err := s.cfg.Store.ListBlueprints(ctx, ns)
	if err != nil {
		return nil, call.fail(ctx, fmt.Errorf("list blueprints: %w", err))
	}

	return out, nil
}

// ReadDraft returns the stored draft after a blueprint read check.
func (s *Blueprints) ReadDraft(
	ctx context.Context, p identity.Principal, ref selfservice.BlueprintRef,
) (selfservice.Blueprint, int64, error) {
	call := s.operation(p, selfservice.OpReadBlueprint, ref, "")

	if err := call.authorize(ctx); err != nil {
		return selfservice.Blueprint{}, 0, call.fail(ctx, err)
	}

	out, version, err := s.cfg.Store.Draft(ctx, ref)
	if err != nil {
		return selfservice.Blueprint{}, 0, call.fail(ctx, fmt.Errorf("read blueprint draft: %w", err))
	}

	return out, version, nil
}

// WriteDraft checks access, the parsed blueprint and its name before writing.
func (s *Blueprints) WriteDraft(
	ctx context.Context, p identity.Principal, ref selfservice.BlueprintRef, b selfservice.Blueprint, version int64,
) (int64, error) {
	call := s.operation(p, selfservice.OpWriteDraft, ref, "")

	if err := call.authorize(ctx); err != nil {
		return 0, call.fail(ctx, err)
	}

	if err := validateDraft(ref, &b); err != nil {
		return 0, call.fail(ctx, err)
	}

	// Defense in depth: Validate checks the keys too; this check stays so that
	// a new caller cannot skip the one-home rule.
	if err := selfservice.CheckOneHome(&b); err != nil {
		return 0, call.fail(ctx, fmt.Errorf("check blueprint homes: %w", err))
	}

	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return 0, call.fail(ctx, err)
	}

	entry := call.entry(audit.OutcomeSuccess)
	next, err := s.cfg.Store.PutDraft(ctx, b, version, fence, []audit.Entry{entry})
	if err != nil {
		return 0, call.fail(ctx, fmt.Errorf("write blueprint draft: %w", err))
	}

	return next, nil
}

// Publish checks the stored draft and compiles every expression before writing.
func (s *Blueprints) Publish(
	ctx context.Context, p identity.Principal, ref selfservice.BlueprintRef, draftVersion int64,
) (selfservice.Version, error) {
	call := s.operation(p, selfservice.OpPublish, ref, "")

	if err := call.authorize(ctx); err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	stored, version, err := s.cfg.Store.Draft(ctx, ref)
	if err != nil {
		return selfservice.Version{}, call.fail(ctx, fmt.Errorf("read blueprint draft for publish: %w", err))
	}

	if version != draftVersion {
		return selfservice.Version{}, call.fail(ctx, fmt.Errorf("publish blueprint: %w", selfservice.ErrConflict))
	}

	// Defense in depth: the write path validates drafts; this check stays so
	// that a new caller cannot skip validation of an older or restored row.
	if err = validateDraft(ref, &stored); err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	if err = s.authorizeSources(ctx, p, ref.Namespace, &stored); err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	if err = s.compile(&stored); err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	stored.ExprVersion = selfservice.ExprVersionName(s.cfg.Evaluator.Version())
	publication := selfservice.Publication{
		At: s.cfg.Clock.Now(), Ref: ref, By: p.Subject, Blueprint: stored, DraftVersion: draftVersion,
	}
	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	entry := call.entry(audit.OutcomeSuccess)
	out, err := s.cfg.Store.Publish(ctx, publication, fence, []audit.Entry{entry})
	if err != nil {
		return selfservice.Version{}, call.fail(ctx, fmt.Errorf("publish blueprint: %w", err))
	}

	return out, nil
}

// ReadVersion returns one version after a blueprint read check.
func (s *Blueprints) ReadVersion(
	ctx context.Context, p identity.Principal, ref selfservice.BlueprintRef, n int,
) (selfservice.Version, error) {
	call := s.operation(p, selfservice.OpReadBlueprint, ref, strconv.Itoa(n))

	if err := call.authorize(ctx); err != nil {
		return selfservice.Version{}, call.fail(ctx, err)
	}

	out, err := s.cfg.Store.Version(ctx, ref, n)
	if err != nil {
		return selfservice.Version{}, call.fail(ctx, fmt.Errorf("read blueprint version: %w", err))
	}

	return out, nil
}

// Retire checks access and retires one version with an audit entry.
func (s *Blueprints) Retire(
	ctx context.Context, p identity.Principal, ref selfservice.BlueprintRef, n int,
) error {
	call := s.operation(p, selfservice.OpRetire, ref, strconv.Itoa(n))

	if err := call.authorize(ctx); err != nil {
		return call.fail(ctx, err)
	}

	fence, err := s.fence(ctx, ref.Namespace)
	if err != nil {
		return call.fail(ctx, err)
	}

	entry := call.entry(audit.OutcomeSuccess)
	if err := s.cfg.Store.Retire(ctx, ref, n, fence, []audit.Entry{entry}); err != nil {
		return call.fail(ctx, fmt.Errorf("retire blueprint version: %w", err))
	}

	return nil
}
