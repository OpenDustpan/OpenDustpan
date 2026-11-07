package issuersvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
)

// RotateRequest is the body of a manual rotation. An emergency rotation
// needs a reason.
type RotateRequest struct {
	Reason    string
	Emergency bool
}

// RotationResult is the answer of a rotation: the keys after it, and the keys
// that it deleted.
type RotationResult struct {
	Keys     []profile.KeyInfo
	Deleted  []profile.KeyID
	Promoted bool
}

// Rotate rotates the keys of a namespace now. A normal rotation makes the
// next key active. For a namespace with an issuerURL the copy check runs
// first, and a failed check changes nothing.
func (s *Service) Rotate(ctx context.Context, a *Actor, ns namespace.Name, req RotateRequest) (RotationResult, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return RotationResult{}, err
	}

	r := rotation{actor: a, reason: "", kind: profile.RotateNormal}

	if req.Emergency {
		if err := checkReason(req.Reason); err != nil {
			return RotationResult{}, err
		}

		r.kind, r.reason = profile.RotateEmergency, req.Reason
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	return s.runRotation(ctx, &n, &r)
}

func checkReason(reason string) error {
	switch {
	case strings.TrimSpace(reason) == "":
		return fmt.Errorf("%w: an emergency rotation needs a reason", profile.ErrInvalid)
	case utf8.RuneCountInString(reason) > profile.MaxEmergencyReason:
		return fmt.Errorf("%w: a reason has at most %d characters", profile.ErrInvalid, profile.MaxEmergencyReason)
	case ident.HasHidden(reason):
		return fmt.Errorf("%w: a reason holds a control, format or separator character", profile.ErrInvalid)
	default:
		return nil
	}
}

// rotation says who rotates and why. actor is nil for the schedule.
type rotation struct {
	actor  *Actor
	reason string
	kind   profile.RotationKind
}

func (s *Service) runRotation(ctx context.Context, n *namespace.Namespace, r *rotation) (RotationResult, error) {
	set, err := s.store.GetIssuerSettings(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read issuer settings: %w", err)
	}

	keys, err := s.store.Live(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return RotationResult{}, fmt.Errorf("%w: the namespace has no key: call POST %s to make the keys",
			profile.ErrIssuerKeyMissing, keysRoute(n.Name))
	}

	promote, err := s.copyBefore(ctx, n, &set, keys, r)
	if err != nil {
		return RotationResult{}, err
	}

	now := s.now().UTC()

	plan, err := profile.PlanRotation(keys, now, profile.RotationOptions{Kind: r.kind, Promote: promote})
	if errors.Is(err, profile.ErrIssuerKeyMissing) {
		return RotationResult{}, fmt.Errorf("%w: the rotation changes nothing: export the files with GET %s, "+
			"copy them, then call POST %s", err, exportRoute(n.Name), checkCopyRoute(n.Name))
	}

	if err != nil {
		return RotationResult{}, fmt.Errorf("plan the rotation: %w", err)
	}

	if plan.Empty() {
		return RotationResult{Keys: infos(keys), Deleted: nil, Promoted: false}, nil
	}

	add, err := s.planKeys(ctx, n.Name, &plan, now)
	if err != nil {
		return RotationResult{}, err
	}

	err = s.store.Apply(ctx, n.Name, profile.KeyChange{Add: add, Move: plan.Moves, Initial: false}, n.Fence(),
		[]audit.Entry{s.rotationEntry(n.Name, r, keyChanges(add, plan.Moves))})
	if err != nil {
		return RotationResult{}, fmt.Errorf("store rotation: %w", err)
	}

	// The private keys that the signer opened may be keys that this rotation
	// deleted or retired.
	s.forget(n.Name)
	s.notifyRotation(ctx, n, &rotationChange{before: keys, added: add, plan: plan, kind: r.kind, now: now})

	after, err := s.store.Live(ctx, n.Name)
	if err != nil {
		return RotationResult{}, fmt.Errorf("read signing keys: %w", err)
	}

	return RotationResult{Keys: infos(after), Deleted: plan.Deleted(), Promoted: plan.Promoted()}, nil
}

// emergencyNote is the change that an emergency rotation adds to its audit
// entry: the new active key is not in any JWKS that a verifier cached, and the
// entry says that the rotation skipped the wait of profile.MinNextKeyAge.
const emergencyNote = "skipped: an emergency rotation replaces every key at once, so the new active key " +
	"was in no cached JWKS before it signed"

func (s *Service) rotationEntry(ns namespace.Name, r *rotation, changes []audit.Change) audit.Entry {
	if r.kind == profile.RotateEmergency {
		changes = append(changes, change("minNextKeyAge", emergencyNote))
	}

	if r.actor == nil {
		return s.serverEntry(ns, string(r.kind)+" rotation", changes)
	}

	return s.entry(r.actor, ns, r.reason, changes)
}

// planKeys makes the keys that the plan adds.
func (s *Service) planKeys(
	ctx context.Context, ns namespace.Name, plan *profile.RotationPlan, now time.Time,
) ([]profile.Key, error) {
	var add []profile.Key

	for _, want := range []struct {
		make  bool
		state profile.KeyState
	}{{plan.NewActive, profile.KeyActive}, {plan.NewNext, profile.KeyNext}} {
		if !want.make {
			continue
		}

		k, err := s.newKey(ctx, ns, want.state, now)
		if err != nil {
			return nil, err
		}

		add = append(add, k)
	}

	return add, nil
}

// copyBefore runs the copy check that comes before a promotion, for a
// namespace with an issuerURL. It returns whether a promotion may run. An
// emergency rotation needs no check: it promotes nothing.
func (s *Service) copyBefore(
	ctx context.Context, n *namespace.Namespace, set *profile.IssuerSettings, keys []profile.Key, r *rotation,
) (bool, error) {
	if set.IssuerURL == "" || r.kind == profile.RotateEmergency {
		return true, nil
	}

	next, hasNext := findState(keys, profile.KeyNext)
	if !hasNext && r.kind == profile.RotateNormal {
		// A normal rotation without a next key makes one and promotes nothing.
		return true, nil
	}

	check := s.runCheck(ctx, set, keys, checkWant{Next: hasNext})

	if err := s.recordCheck(ctx, n, &check, r.actor); err != nil {
		return false, err
	}

	if r.kind == profile.RotateScheduled && !check.OK {
		s.checkFailed(ctx, n, set, keys, &check)
	}

	if !hasNext {
		return true, nil
	}

	// Only this check counts: the list of an earlier check says that a key was
	// once in a copy, and the promotion needs the key in the copy now.
	return check.OK && check.Holds(set.IssuerURL, next.ID), nil
}

// recordCheck stores the outcome of a copy check with an audit entry. actor is
// nil for the schedule.
func (s *Service) recordCheck(ctx context.Context, n *namespace.Namespace, c *profile.CopyCheck, actor *Actor) error {
	changes := []audit.Change{change("copyCheck", strconv.FormatBool(c.OK))}

	var e audit.Entry
	if actor == nil {
		e = s.serverEntry(n.Name, "copy check", changes)
	} else {
		e = s.entry(actor, n.Name, "", changes)
	}

	if err := s.store.RecordCopyCheck(ctx, n.Name, *c, n.Fence(), []audit.Entry{e}); err != nil {
		return fmt.Errorf("store copy check: %w", err)
	}

	return nil
}

// RotateAll runs the daily step of the schedule issuer.rotate for every
// namespace that has keys. A namespace that fails does not stop the others.
func (s *Service) RotateAll(ctx context.Context) error {
	all, err := s.namespaces.ListNamespaces(ctx)
	if err != nil {
		return fmt.Errorf("list namespaces: %w", err)
	}

	var errs []error

	for i := range all {
		if rotErr := s.rotateOne(ctx, &all[i]); rotErr != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", all[i].Name, rotErr))
		}
	}

	return errors.Join(errs...)
}

// rotateOne runs the daily step for one namespace. A namespace with no key, and
// a namespace that another instance changed first, are no error.
func (s *Service) rotateOne(ctx context.Context, n *namespace.Namespace) error {
	keys, err := s.store.Live(ctx, n.Name)
	if err != nil {
		return fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return nil
	}

	_, err = s.runRotation(ctx, n, &rotation{actor: nil, reason: "", kind: profile.RotateScheduled})

	if errors.Is(err, profile.ErrConflict) || errors.Is(err, namespace.ErrFenced) {
		// Another instance changed the keys first. The next tick looks again.
		s.log.LogAttrs(ctx, slog.LevelDebug, "issuer.rotate: skipped a namespace that changed",
			slog.String(attrNamespace, n.Name.String()))

		return nil
	}

	return err
}

// CheckCopy runs the copy check at once and stores and answers its outcome.
func (s *Service) CheckCopy(ctx context.Context, a *Actor, ns namespace.Name) (profile.CopyCheck, error) {
	if err := s.require(ctx, a, ns, identity.ActionNamespaceManageIssuer); err != nil {
		return profile.CopyCheck{}, err
	}

	n, err := s.namespaces.GetNamespace(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	set, err := s.store.GetIssuerSettings(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read issuer settings: %w", err)
	}

	if set.IssuerURL == "" {
		return profile.CopyCheck{}, errNoIssuerURL(ns)
	}

	keys, err := s.store.Live(ctx, ns)
	if err != nil {
		return profile.CopyCheck{}, fmt.Errorf("read signing keys: %w", err)
	}

	if len(keys) == 0 {
		return profile.CopyCheck{}, errNoKeys(ns)
	}

	_, hasNext := findState(keys, profile.KeyNext)
	check := s.runCheck(ctx, &set, keys, checkWant{Next: hasNext})

	if recErr := s.recordCheck(ctx, &n, &check, a); recErr != nil {
		return profile.CopyCheck{}, recErr
	}

	return check, nil
}

// errNoIssuerURL is the refusal of a route that needs an issuerURL.
func errNoIssuerURL(ns namespace.Name) error {
	return fmt.Errorf("%w: the namespace has no issuerURL: set it with PATCH %s",
		profile.ErrInvalid, "/api/dustpan/v1/namespaces/"+ns.String()+"/issuer")
}

// errNoKeys is the refusal of a route that needs keys.
func errNoKeys(ns namespace.Name) error {
	return fmt.Errorf("%w: the namespace has no key: call POST %s to make the keys",
		profile.ErrIssuerKeyMissing, keysRoute(ns))
}

// isNotConfigured reports whether err says that the server has no issuer host.
func isNotConfigured(err error) bool {
	return errors.Is(err, profile.ErrIssuerNotConfigured)
}
