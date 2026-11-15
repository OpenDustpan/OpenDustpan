package trustrulesvc

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// UpdateInput names the rule and the fields of a rule update. Empty fields
// keep the stored value; the service sorts the conditions by their claim names
// when they change. ExpectedRevision is the revision of the If-Match header.
type UpdateInput struct {
	ID               trustrule.ID
	Name             string
	Issuer           string
	Audience         string
	Keys             trustrule.KeySource
	KeysSet          bool
	StaticKeys       *string
	PrincipalID      string
	Conditions       []trustrule.Condition
	ConditionsSet    bool
	SpiffeIDPattern  *string
	Bundle           *string
	ExpectedRevision int64
}

// Update stores the changed fields of the rule in.ID of ns when its revision
// is one past the stored revision, with the audit entry template e. It needs
// the same checks as a create: the fresh authentication, the issuer check, the
// action on the principal, and the reach of the principal. It returns
// trustrule.ErrStale for a stale revision and keeps the stored rule.
func (s *Service) Update(
	ctx context.Context, caller identity.Principal, e audit.Entry, ns namespace.Name, in UpdateInput,
) (trustrule.Rule, error) {
	if err := s.requireWriter(ctx, caller); err != nil {
		return trustrule.Rule{}, err
	}

	n, err := s.namespaces.WritableHere(ctx, ns)
	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("read namespace %s: %w", ns, err)
	}

	stored, err := s.store.Get(ctx, ns, in.ID)
	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("get trust rule: %w", err)
	}

	r := mergeUpdate(&stored, &in)

	if checkErr := s.checkUpdate(ctx, caller, ns, &r, &in); checkErr != nil {
		return trustrule.Rule{}, checkErr
	}

	updated, err := s.store.Update(ctx, ns, r, n.Fence(), s.updateEntry(e, &stored, &r))
	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("update trust rule: %w", err)
	}

	return updated, nil
}

// mergeUpdate returns the stored rule with the fields that in sets, at the
// revision one past the expected one.
func mergeUpdate(stored *trustrule.Rule, in *UpdateInput) trustrule.Rule {
	r := *stored

	if in.Name != "" {
		r.Name = in.Name
	}

	if in.Issuer != "" {
		r.Issuer = in.Issuer
	}

	if in.Audience != "" {
		r.Audience = in.Audience
	}

	if in.KeysSet {
		r.Keys = in.Keys
	}

	if in.StaticKeys != nil {
		r.StaticKeys = *in.StaticKeys
	}

	if in.PrincipalID != "" {
		r.PrincipalID = in.PrincipalID
	}

	if in.ConditionsSet {
		r.Conditions = trustrule.SortConditions(in.Conditions)
	}

	if in.SpiffeIDPattern != nil {
		r.SpiffeIDPattern = *in.SpiffeIDPattern
	}

	if in.Bundle != nil {
		r.Bundle = *in.Bundle
	}

	r.Revision = in.ExpectedRevision + 1

	return r
}

// checkUpdate runs the checks of a create on the updated rule r: its fields,
// its issuer, the action of the caller on its principal, the reach of the
// principal, and the principal itself.
func (s *Service) checkUpdate(
	ctx context.Context, caller identity.Principal, ns namespace.Name, r *trustrule.Rule, in *UpdateInput,
) error {
	if err := r.Validate(); err != nil {
		return err
	}

	if err := s.checkUpdatedIssuer(ctx, r, in); err != nil {
		return err
	}

	if err := s.authorizeCreate(ctx, caller, r.PrincipalID); err != nil {
		return err
	}

	return s.checkPrincipal(ctx, r.PrincipalID, ns)
}

// checkUpdatedIssuer runs the issuer checks of a create when the update changes
// the issuer or the key source.
func (s *Service) checkUpdatedIssuer(ctx context.Context, r *trustrule.Rule, in *UpdateInput) error {
	if in.Issuer == "" && !in.KeysSet {
		return nil
	}

	if err := s.CheckOwnIssuer(ctx, r.Issuer); err != nil {
		return err
	}

	if r.Keys != trustrule.KeySourceDiscovery {
		return nil
	}

	if err := s.checkURL(r.Issuer); err != nil {
		return fmt.Errorf("%w: the issuer %q: %w", trustrule.ErrInvalid, r.Issuer, err)
	}

	return nil
}

// updateEntry is the entry of an update of a rule, with a change for each
// field that changed: name, issuer, audience, key source, principal, SPIFFE
// ID pattern, bundle, conditions and static keys. The conditions are
// recorded as their old and new text; the static keys are recorded as a
// SHA-256 hex digest or the key IDs of the old and new keys, never as the
// key text.
func (s *Service) updateEntry(e audit.Entry, old, r *trustrule.Rule) audit.Entry {
	out := s.entry(e, r.Namespace, r.ID, ActionUpdate)

	change := func(key, before, after string) {
		if before != after {
			out.Changes = append(out.Changes,
				audit.Change{Key: key, Old: &before, New: after, Layer: "", TakesEffect: ""})
		}
	}

	change("name", old.Name, r.Name)
	change("issuer", old.Issuer, r.Issuer)
	change("audience", old.Audience, r.Audience)
	change("keySource", string(old.Keys), string(r.Keys))
	change("principal", old.PrincipalID, r.PrincipalID)
	change("spiffeIDPattern", old.SpiffeIDPattern, r.SpiffeIDPattern)
	change("bundle", old.Bundle, r.Bundle)
	change("conditions", formatConditions(old.Conditions), formatConditions(r.Conditions))
	change("staticKeys", staticKeysDigest(old.StaticKeys), staticKeysDigest(r.StaticKeys))

	return out
}
