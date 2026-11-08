package gate

import (
	"context"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Audit actions of the writes of gates and access limits. They name the
// entry only. The authorization action of the route is a different value.
const (
	AuditSetGate           = "namespace:set-gate"
	AuditDeleteGate        = "namespace:delete-gate"
	AuditSetAccessLimit    = "namespace:set-access-limit"
	AuditDeleteAccessLimit = "namespace:delete-access-limit"
)

// MaxMessageBytes bounds the message of a gate.
const MaxMessageBytes = 1024

// MaxReasonBytes bounds the reason of a write that makes a final gate weaker.
const MaxReasonBytes = 1024

// Gates returns the gates stored at scope, ordered by ID. It returns none of
// a wider scope. The State of each gate is empty: the state is computed when
// the server evaluates the gate.
func (s *ConfigService) Gates(ctx context.Context, scope Scope) ([]Gate, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return nil, err
	}

	gates, err := s.store.Gates().List(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list the gates: %w", err)
	}

	return gates, nil
}

// Gate returns the gate id of scope, or ErrNotFound.
func (s *ConfigService) Gate(ctx context.Context, scope Scope, id ID) (Gate, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return Gate{}, err
	}

	g, err := s.store.Gates().Get(ctx, scope, id)
	if err != nil {
		return Gate{}, fmt.Errorf("read the gate: %w", err)
	}

	return g, nil
}

// PutGate checks g with the engine, stores it at g.Scope and returns it with
// its new version. With prev 0, g must be new, else prev is the stored
// version. reason is the reason of a write that makes a final gate weaker. e
// holds the caller: the service sets its namespace, time, object, kind,
// outcome and changes. A change of the mode adds the old and the new mode to
// the changes of the entry.
//
// It returns ErrInvalid for a gate that is not valid or that the engine
// refuses (a *CheckError), ErrLimit above MaxGatesPerScope or MaxConditionBytes,
// ErrFinal for a write that a final gate forbids, ErrConflict for a stale
// version, ErrUnavailable when the service has no engine or the engine does
// not answer, and the fence errors of Objects.
func (s *ConfigService) PutGate(ctx context.Context, g Gate, prev int64, reason string, e audit.Entry) (Gate, error) {
	if err := requireNamespaceScope(g.Scope); err != nil {
		return Gate{}, err
	}

	if err := validateGate(g); err != nil {
		return Gate{}, err
	}

	if err := validateReason(reason); err != nil {
		return Gate{}, err
	}

	checked, err := s.checkGate(ctx, g)
	if err != nil {
		return Gate{}, err
	}

	g = checked

	object := g.Scope.Key() + "/gates/" + string(g.ID)

	fence, entry, err := s.prepare(ctx, g.Scope, e, object, AuditSetGate)
	if err != nil {
		return Gate{}, err
	}

	entry.Changes = putChanges("gate/"+string(g.ID), prev)

	if prev > 0 {
		entry.Changes = append(entry.Changes, s.modeChange(ctx, g)...)
	}

	entry.Changes = withReason(entry.Changes, reason)

	version, err := s.store.Gates().Put(ctx, g, prev, fence, entry)
	if err != nil {
		return Gate{}, fmt.Errorf("store the gate: %w", err)
	}

	g.Version = version

	return g, nil
}

// modeChange returns the audit change of a switch of the mode of g, or none
// when the stored gate has the mode of g or cannot be read. The read only
// labels the entry: the store checks the version of the write itself.
func (s *ConfigService) modeChange(ctx context.Context, g Gate) []audit.Change {
	old, err := s.store.Gates().Get(ctx, g.Scope, g.ID)
	if err != nil || old.Mode == g.Mode {
		return nil
	}

	from := string(old.Mode)

	return []audit.Change{{
		Key: "gate/" + string(g.ID) + ".mode", Old: &from, New: string(g.Mode), Layer: "", TakesEffect: "",
	}}
}

// DeleteGate removes ref.ID of ref.Scope when its version is ref.Version.
// reason is the reason of the delete of a final gate. It returns ErrNotFound
// for an unknown gate, ErrConflict for another version and ErrFinal for a
// final gate with no reason.
func (s *ConfigService) DeleteGate(ctx context.Context, ref ObjectRef, reason string, e audit.Entry) error {
	if err := validateReason(reason); err != nil {
		return err
	}

	changes := withReason(deleteChanges("gate/"+string(ref.ID), ref.Version), reason)

	return s.deleteObject(ctx, ref, e, deleteSpec{
		segment: "gates", action: AuditDeleteGate, noun: "gate", changes: changes,
		remove: func(fence namespace.Fence, entry audit.Entry) error {
			return s.store.Gates().Delete(ctx, ref, fence, entry)
		},
	})
}

// AccessLimits returns the access limits stored at scope, ordered by ID. It
// returns none of a wider scope.
func (s *ConfigService) AccessLimits(ctx context.Context, scope Scope) ([]AccessLimit, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return nil, err
	}

	limits, err := s.store.AccessLimits().List(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list the access limits: %w", err)
	}

	return limits, nil
}

// AccessLimit returns the access limit id of scope, or ErrNotFound.
func (s *ConfigService) AccessLimit(ctx context.Context, scope Scope, id ID) (AccessLimit, error) {
	if err := requireNamespaceScope(scope); err != nil {
		return AccessLimit{}, err
	}

	l, err := s.store.AccessLimits().Get(ctx, scope, id)
	if err != nil {
		return AccessLimit{}, fmt.Errorf("read the access limit: %w", err)
	}

	return l, nil
}

// PutAccessLimit checks l with the engine, stores it at l.Scope and returns
// it with its new version. The version rules and the entry follow PutGate.
// It returns ErrInvalid for a limit that is not valid or that the engine
// refuses, which includes a policy with the effect permit.
func (s *ConfigService) PutAccessLimit(
	ctx context.Context, l AccessLimit, prev int64, e audit.Entry,
) (AccessLimit, error) {
	if err := requireNamespaceScope(l.Scope); err != nil {
		return AccessLimit{}, err
	}

	if err := validateAccessLimit(l); err != nil {
		return AccessLimit{}, err
	}

	if err := s.checkPolicy(ctx, Schema{Version: accessSchema(), PathSets: nil}, accessLimitPolicy(l)); err != nil {
		return AccessLimit{}, err
	}

	object := l.Scope.Key() + "/access-limits/" + string(l.ID)

	fence, entry, err := s.prepare(ctx, l.Scope, e, object, AuditSetAccessLimit)
	if err != nil {
		return AccessLimit{}, err
	}

	entry.Changes = putChanges("access-limit/"+string(l.ID), prev)

	version, err := s.store.AccessLimits().Put(ctx, l, prev, fence, entry)
	if err != nil {
		return AccessLimit{}, fmt.Errorf("store the access limit: %w", err)
	}

	l.Version = version

	return l, nil
}

// DeleteAccessLimit removes ref.ID of ref.Scope when its version is
// ref.Version. It returns ErrNotFound for an unknown access limit and
// ErrConflict for another version.
func (s *ConfigService) DeleteAccessLimit(ctx context.Context, ref ObjectRef, e audit.Entry) error {
	return s.deleteObject(ctx, ref, e, deleteSpec{
		segment: "access-limits", action: AuditDeleteAccessLimit, noun: "access limit",
		changes: deleteChanges("access-limit/"+string(ref.ID), ref.Version),
		remove: func(fence namespace.Fence, entry audit.Entry) error {
			return s.store.AccessLimits().Delete(ctx, ref, fence, entry)
		},
	})
}

// deleteSpec says how deleteObject removes one kind of object.
type deleteSpec struct {
	// segment is the path segment of the kind in the object of the entry.
	segment string
	action  string
	noun    string
	changes []audit.Change
	remove  func(namespace.Fence, audit.Entry) error
}

// deleteObject completes the audit entry of the delete of ref and removes the
// object with spec.remove, in the namespace of the scope of ref.
func (s *ConfigService) deleteObject(ctx context.Context, ref ObjectRef, e audit.Entry, spec deleteSpec) error {
	if err := requireNamespaceScope(ref.Scope); err != nil {
		return err
	}

	object := ref.Scope.Key() + "/" + spec.segment + "/" + string(ref.ID)

	fence, entry, err := s.prepare(ctx, ref.Scope, e, object, spec.action)
	if err != nil {
		return err
	}

	entry.Changes = spec.changes

	if err := spec.remove(fence, entry); err != nil {
		return fmt.Errorf("delete the %s: %w", spec.noun, err)
	}

	return nil
}

// checkGate type-checks g against the fact schema of its decision point, and
// returns g with the engine and the schema that it passed.
func (s *ConfigService) checkGate(ctx context.Context, g Gate) (Gate, error) {
	version := SchemaVersion{Subject: string(g.DecisionPoint), Major: 1, Minor: 0}

	if err := s.checkPolicy(ctx, Schema{Version: version, PathSets: nil}, gatePolicy(&g)); err != nil {
		return Gate{}, err
	}

	g.CheckedWith = CheckedWith{Engine: s.engine.Version(), Schema: version}
	// The caller never sets these two. The state is computed at load, and no
	// path set can be read until the path sets have a route.
	g.State = ""
	g.Reads = nil

	return g, nil
}

// checkPolicy asks the engine to check p, and returns a *CheckError for a
// diagnostic. It fails closed: no engine, or an engine that does not answer,
// is ErrUnavailable.
func (s *ConfigService) checkPolicy(ctx context.Context, schema Schema, p Policy) error {
	if s.engine == nil {
		return fmt.Errorf("%w: this server has no policy engine to check %s", ErrUnavailable, p.ID)
	}

	// The membership test of an access limit is checked before the policy
	// check, so that the text of a limit whose group guard cannot read it
	// gets the code of that rule and not the diagnostic of the policy check.
	if p.Kind == PolicyLimit {
		if err := s.engine.CheckLimitMembership(ctx, p.Text); err != nil {
			return fmt.Errorf("check %s: %w", p.ID, err)
		}
	}

	diags, err := s.engine.Check(ctx, schema, []Policy{p})
	if err != nil {
		return fmt.Errorf("check %s: %w", p.ID, err)
	}

	if len(diags) > 0 {
		for i := range diags {
			diags[i].Message = cleanText(diags[i].Message)
		}

		return &CheckError{Diagnostics: diags}
	}

	return nil
}

// accessLimitPolicy returns the policy that the engine takes for l.
func accessLimitPolicy(l AccessLimit) Policy {
	return Policy{
		ID: l.PolicyID(), Version: l.Version, Kind: PolicyLimit, Point: "", Condition: "", Unless: "",
		Text: l.Policy,
	}
}

// validateGate checks the fields of g that the engine does not check.
func validateGate(g Gate) error {
	if _, err := ParseID(string(g.ID)); err != nil {
		return err
	}

	if _, err := ParseDecisionPoint(string(g.DecisionPoint)); err != nil {
		return err
	}

	if _, err := ParseOutcome(string(g.Outcome)); err != nil {
		return err
	}

	if _, err := ParseMode(string(g.Mode)); err != nil {
		return err
	}

	if _, err := ParseOnError(string(g.OnError)); err != nil {
		return err
	}

	switch {
	case len(g.Condition) > MaxConditionBytes:
		return fmt.Errorf("%w: the condition is %d bytes, above %d", ErrLimit, len(g.Condition), MaxConditionBytes)
	case len(g.Unless) > MaxConditionBytes:
		return fmt.Errorf("%w: unless is %d bytes, above %d", ErrLimit, len(g.Unless), MaxConditionBytes)
	case len(g.Message) > MaxMessageBytes:
		return fmt.Errorf("%w: the message is %d bytes, above %d", ErrInvalid, len(g.Message), MaxMessageBytes)
	case strings.TrimSpace(g.Message) == "":
		return fmt.Errorf("%w: the gate needs a message", ErrInvalid)
	default:
		return nil
	}
}

// validateAccessLimit checks the fields of l that the engine does not check.
func validateAccessLimit(l AccessLimit) error {
	if _, err := ParseID(string(l.ID)); err != nil {
		return err
	}

	switch {
	case strings.TrimSpace(l.Policy) == "":
		return fmt.Errorf("%w: the access limit needs a policy", ErrInvalid)
	case len(l.Policy) > MaxConditionBytes:
		return fmt.Errorf("%w: the policy is %d bytes, above %d", ErrLimit, len(l.Policy), MaxConditionBytes)
	default:
		return nil
	}
}

// validateReason checks the reason of a write that makes a final gate weaker.
func validateReason(reason string) error {
	if len(reason) > MaxReasonBytes {
		return fmt.Errorf("%w: the reason is %d bytes, above %d", ErrInvalid, len(reason), MaxReasonBytes)
	}

	if strings.ContainsAny(reason, "\x00\r\n") {
		return fmt.Errorf("%w: the reason holds a control character", ErrInvalid)
	}

	return nil
}

// withReason adds the reason to the changes of an entry. The store reads it
// with Reason.
func withReason(changes []audit.Change, reason string) []audit.Change {
	if reason == "" {
		return changes
	}

	return append(changes, audit.Change{Key: ReasonKey, Old: nil, New: reason, Layer: "", TakesEffect: ""})
}
