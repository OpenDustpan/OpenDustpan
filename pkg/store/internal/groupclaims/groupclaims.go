// Package groupclaims holds the logic of the explicit group mappings that the
// SQL stores share: the principal.GroupMapper and the
// principal.GroupClaimApplier. Each store gives the package its reads and
// writes through small interfaces, so the rules exist once and each dialect
// only supplies its queries and its transaction.
package groupclaims

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Target is the part of a principal record that decides whether an identity
// provider may manage its groups.
type Target struct {
	Kind       principal.Kind
	Namespace  string
	BreakGlass bool
	Disabled   bool
}

// Reads is what the rules read about a principal.
type Reads interface {
	// Principal returns the target for the principal id, or
	// principal.ErrNotFound for a principal that does not exist or was
	// deleted.
	Principal(ctx context.Context, id string) (Target, error)
	// IsTempAdmin reports whether a temporary administrator record names the
	// principal.
	IsTempAdmin(ctx context.Context, id string) (bool, error)
}

// Pair is one stored mapping: a group value and the group it maps to.
type Pair struct {
	Value   string
	GroupID string
}

// MappingReads is what the mapper reads.
type MappingReads interface {
	Reads
	// Mappings returns the count of all stored mappings of the source and
	// the source ID, and the ones whose group exists and was not deleted.
	Mappings(ctx context.Context, source principal.MembershipSource, sourceID string) (int, []Pair, error)
}

// Membership is one current membership of a principal.
type Membership struct {
	GroupID  string
	Source   principal.MembershipSource
	SourceID string
	State    principal.MembershipState
	Pinned   bool
}

// Tx is the reads and the writes of one transaction.
type Tx interface {
	Reads
	// GroupSource returns the source of the group, and false when the group
	// does not exist or was deleted.
	GroupSource(ctx context.Context, groupID string) (string, bool, error)
	// Memberships returns each current membership of the principal.
	Memberships(ctx context.Context, principalID string) ([]Membership, error)
	// AddMember inserts m, and reports false when a current membership of
	// the principal in the group exists already.
	AddMember(ctx context.Context, m principal.Membership) (bool, error)
	// EndMember ends the current membership at at. When the end would leave
	// the installation with no site administrator, it keeps the membership
	// and returns access.ErrLastSiteAdministrator. The transaction stays
	// usable.
	EndMember(ctx context.Context, groupID, principalID string, at time.Time) (bool, error)
	// Append validates and appends one audit entry in the scope @server.
	Append(ctx context.Context, e audit.Entry) error
}

// Runner runs the changes of one call in one transaction.
type Runner interface {
	Reads
	// InTx validates e, then runs fn in one transaction. An error from fn
	// rolls everything back, the audit entries too.
	InTx(ctx context.Context, e audit.Entry, fn func(Tx) error) error
}

// Mapper implements principal.GroupMapper over the stored mappings.
type Mapper struct {
	reads MappingReads
}

var _ principal.GroupMapper = (*Mapper)(nil)

// NewMapper returns a Mapper that reads with r.
func NewMapper(r MappingReads) *Mapper {
	return &Mapper{reads: r}
}

// checkMapInput is layer 1 of the mapper: the boundary. The applier checks
// the same limits, and the mapper does not rely on it, so a second caller
// cannot send a hostile value to the store.
func checkMapInput(source principal.MembershipSource, sourceID string, values []string) error {
	if err := principal.ValidateGroupSource(source, sourceID); err != nil {
		return err
	}

	if len(values) > principal.MaxGroupClaimValues {
		return fmt.Errorf("%w: %d group values, the limit is %d",
			principal.ErrInvalid, len(values), principal.MaxGroupClaimValues)
	}

	for _, v := range values {
		if err := principal.ValidateGroupValue(v); err != nil {
			return err
		}
	}

	return nil
}

// MapGroups implements principal.GroupMapper. It reads only the mappings of
// the source and the source ID, compares each value byte for byte, and
// returns only groups that exist.
func (m *Mapper) MapGroups(
	ctx context.Context, principalID string, source principal.MembershipSource, sourceID string, values []string,
) (map[string][]string, error) {
	if err := checkMapInput(source, sourceID, values); err != nil {
		return nil, err
	}

	target, err := m.reads.Principal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("read the principal: %w", err)
	}

	total, live, err := m.reads.Mappings(ctx, source, sourceID)
	if err != nil {
		return nil, fmt.Errorf("read the group mappings: %w", err)
	}

	if total == 0 {
		return nil, principal.ErrNoGroupMapping
	}

	out := make(map[string][]string)

	// The groups of the installation are not in the home of a principal of a
	// namespace.
	if target.Namespace != "" {
		return out, nil
	}

	wanted := make(map[string]bool, len(values))
	for _, v := range values {
		wanted[v] = true
	}

	for _, p := range live {
		// Defense in depth: the query keeps the claim value of the source and
		// the source ID only. This check compares the bytes again, so a store
		// that compares values with a collation never maps a value that
		// differs in case.
		if wanted[p.Value] && !slices.Contains(out[p.Value], p.GroupID) {
			out[p.Value] = append(out[p.Value], p.GroupID)
		}
	}

	return out, nil
}

// Applier implements principal.GroupClaimApplier.
type Applier struct {
	runner Runner
	mapper principal.GroupMapper
}

var _ principal.GroupClaimApplier = (*Applier)(nil)

// NewApplier returns an Applier that changes memberships with r and reads
// the mapping with m.
func NewApplier(r Runner, m principal.GroupMapper) *Applier {
	return &Applier{runner: r, mapper: m}
}

// ApplyGroupClaims implements principal.GroupClaimApplier. The rules for
// protected groups are not applied: no stored data for them exists yet.
func (a *Applier) ApplyGroupClaims(
	ctx context.Context, e audit.Entry, c principal.GroupClaims,
) (principal.GroupClaimResult, error) {
	var none principal.GroupClaimResult

	// Layer 1: the boundary.
	if err := c.Validate(); err != nil {
		return none, err
	}

	if !c.Present {
		return none, nil
	}

	values := unique(c.Groups)

	// Layer 3 and layer 4: the principal must be a person that the identity
	// provider can manage. The check runs again in the transaction.
	if err := checkTarget(ctx, a.runner, c.PrincipalID); err != nil {
		return none, err
	}

	mapped, err := a.mapper.MapGroups(ctx, c.PrincipalID, c.Source, c.SourceID, values)
	if errors.Is(err, principal.ErrNoGroupMapping) {
		return a.noMapping(ctx, e, c, values)
	}

	if err != nil {
		return none, fmt.Errorf("map the group values: %w", err)
	}

	var res principal.GroupClaimResult

	err = a.runner.InTx(ctx, e, func(tx Tx) error {
		r := &run{tx: tx, e: e, c: c, res: &principal.GroupClaimResult{
			Added: nil, Removed: nil, Suspended: nil, Refused: nil, Unmapped: nil, NoMapping: false,
		}}

		if applyErr := r.apply(ctx, values, mapped); applyErr != nil {
			return applyErr
		}

		res = *r.res

		return nil
	})
	if err != nil {
		return none, err
	}

	return res, nil
}

// noMapping fails closed for a source with no mapping: it joins no group,
// changes no membership and writes one audit entry.
func (a *Applier) noMapping(
	ctx context.Context, e audit.Entry, c principal.GroupClaims, values []string,
) (principal.GroupClaimResult, error) {
	err := a.runner.InTx(ctx, e, func(tx Tx) error {
		if err := checkTarget(ctx, tx, c.PrincipalID); err != nil {
			return err
		}

		r := &run{tx: tx, e: e, c: c, res: nil}

		return tx.Append(ctx, r.entry(principal.AuditActionClaimNoMapping, audit.OutcomeDenied,
			"principal:"+c.PrincipalID, change("valueCount", strconv.Itoa(len(values)))))
	})
	if err != nil {
		return principal.GroupClaimResult{}, err
	}

	return principal.GroupClaimResult{
		Added: nil, Removed: nil, Suspended: nil, Refused: nil, Unmapped: values, NoMapping: true,
	}, nil
}

// run is one call of ApplyGroupClaims inside its transaction.
type run struct {
	tx  Tx
	e   audit.Entry
	c   principal.GroupClaims
	res *principal.GroupClaimResult
}

// apply computes the adds and the removals and writes them with their audit
// entries.
func (r *run) apply(ctx context.Context, values []string, mapped map[string][]string) error {
	// Defense in depth: the check in ApplyGroupClaims ran before the mapper.
	// It runs again in the transaction so that a change between the two reads
	// never lets a disabled or special principal get a membership.
	if err := checkTarget(ctx, r.tx, r.c.PrincipalID); err != nil {
		return err
	}

	current, err := r.tx.Memberships(ctx, r.c.PrincipalID)
	if err != nil {
		return fmt.Errorf("read the memberships: %w", err)
	}

	plan := planGroups(values, mapped)
	desired, unmapped := plan.groups, plan.unmapped
	r.res.Unmapped = unmapped

	held := make(map[string]bool, len(current))
	for i := range current {
		held[current[i].GroupID] = true
	}

	for _, gid := range desired {
		if err := r.add(ctx, gid, held); err != nil {
			return err
		}
	}

	if err := r.remove(ctx, current, desired); err != nil {
		return err
	}

	if len(unmapped) == 0 {
		return nil
	}

	return r.tx.Append(ctx, r.entry(principal.AuditActionClaimUnmapped, audit.OutcomeSuccess,
		"principal:"+r.c.PrincipalID, unmappedChanges(unmapped)...))
}

// add adds the membership in gid, or refuses the group.
func (r *run) add(ctx context.Context, gid string, held map[string]bool) error {
	source, ok, err := r.tx.GroupSource(ctx, gid)
	if err != nil {
		return fmt.Errorf("read the group %s: %w", gid, err)
	}

	// Layer 4: authorization at the point of use, for the object. The mapper
	// returns only live groups, and this check does not rely on it. A group
	// that does not exist, was deleted or is kept by a directory never gets
	// a member from a claim, even when the mapper returned it.
	if !ok || principal.Source(source) != principal.SourceLocal {
		r.res.Refused = append(r.res.Refused, gid)

		return r.tx.Append(ctx, r.entry(principal.AuditActionClaimRefuse, audit.OutcomeDenied,
			"group:"+gid, change("reason", "the group cannot take a member from a claim")))
	}

	// A principal that is a member of the group already, from any source,
	// keeps that membership and gets no second one.
	if held[gid] {
		return nil
	}

	added, err := r.tx.AddMember(ctx, principal.Membership{
		AddedAt: r.e.At, SuspendedAt: time.Time{}, GroupID: gid, PrincipalID: r.c.PrincipalID,
		Source: r.c.Source, SourceID: r.c.SourceID, State: principal.MembershipStateActive, Pinned: false,
	})
	if err != nil {
		return fmt.Errorf("add the membership in %s: %w", gid, err)
	}

	if !added {
		return nil
	}

	r.res.Added = append(r.res.Added, gid)

	return r.tx.Append(ctx, r.entry(principal.AuditActionClaimAdd, audit.OutcomeSuccess, "group:"+gid))
}

// remove ends each membership of the source and the source ID that the
// claims no longer name. It never ends a membership of another source or
// source ID, or a pinned membership.
func (r *run) remove(ctx context.Context, current []Membership, desired []string) error {
	for i := range current {
		m := &current[i]

		// Defense in depth: the removal is for the source and the source ID of
		// the claims only. The loop checks the pair again for each row, so a
		// store that returns the memberships of another source never loses
		// one.
		if m.Source != r.c.Source || m.SourceID != r.c.SourceID || m.Pinned || slices.Contains(desired, m.GroupID) {
			continue
		}

		ended, err := r.tx.EndMember(ctx, m.GroupID, r.c.PrincipalID, r.e.At)
		if errors.Is(err, access.ErrLastSiteAdministrator) {
			// The membership stays, because the end would leave the
			// installation with no site administrator. The call carries on
			// with the other groups.
			r.res.Refused = append(r.res.Refused, m.GroupID)

			err = r.tx.Append(ctx, r.entry(principal.AuditActionClaimRefuse, audit.OutcomeDenied,
				"group:"+m.GroupID, change("reason", "the removal would leave no site administrator")))
			if err != nil {
				return err
			}

			continue
		}

		if err != nil {
			return fmt.Errorf("end the membership in %s: %w", m.GroupID, err)
		}

		if !ended {
			continue
		}

		r.res.Removed = append(r.res.Removed, m.GroupID)

		err = r.tx.Append(ctx, r.entry(principal.AuditActionClaimRemove, audit.OutcomeSuccess, "group:"+m.GroupID))
		if err != nil {
			return err
		}
	}

	return nil
}

// entry returns the audit entry of one effect of the claims, from the entry
// of the call.
func (r *run) entry(action string, outcome audit.Outcome, object string, extra ...audit.Change) audit.Entry {
	out := r.e
	out.Action = action
	out.Object = object
	out.Outcome = outcome
	out.ObjectID = ""
	out.Changes = append([]audit.Change{
		change("principal", r.c.PrincipalID),
		change("source", string(r.c.Source)),
		change("sourceId", r.c.SourceID),
	}, extra...)

	return out
}

// change returns the audit change of the key with the new text.
func change(key, text string) audit.Change {
	return audit.Change{Key: key, Old: nil, New: text, Layer: "", TakesEffect: ""}
}

// checkTarget returns why an identity provider cannot manage the groups of
// the principal id, or nil.
func checkTarget(ctx context.Context, r Reads, id string) error {
	t, err := r.Principal(ctx, id)
	if err != nil {
		return fmt.Errorf("read the principal: %w", err)
	}

	switch {
	case t.Kind != principal.KindPerson || t.Namespace != "" || t.BreakGlass:
		return fmt.Errorf("%w: %s", principal.ErrNotSignInTarget, principal.Ref(id))
	case t.Disabled:
		return fmt.Errorf("%w: %s", principal.ErrDisabled, principal.Ref(id))
	}

	temp, err := r.IsTempAdmin(ctx, id)
	if err != nil {
		return fmt.Errorf("read the temporary administrator record of %s: %w", principal.Ref(id), err)
	}

	if temp {
		return fmt.Errorf("%w: %s is a temporary administrator", principal.ErrNotSignInTarget, principal.Ref(id))
	}

	return nil
}

// groupPlan is what the values of a call map to: the sorted group IDs, and
// the values that map to no group in the order of the values.
type groupPlan struct {
	groups   []string
	unmapped []string
}

// planGroups maps the values with the answer of the mapper.
func planGroups(values []string, mapped map[string][]string) groupPlan {
	var plan groupPlan

	for _, v := range values {
		ids := mapped[v]
		if len(ids) == 0 {
			plan.unmapped = append(plan.unmapped, v)

			continue
		}

		for _, id := range ids {
			if !slices.Contains(plan.groups, id) {
				plan.groups = append(plan.groups, id)
			}
		}
	}

	slices.Sort(plan.groups)

	return plan
}

// unique returns the values of in, each once, in order.
func unique(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, v := range in {
		if seen[v] {
			continue
		}

		seen[v] = true

		out = append(out, v)
	}

	return out
}

// unmappedChanges returns the count of the unmapped values and at most
// principal.MaxUnmappedAudited of them.
func unmappedChanges(unmapped []string) []audit.Change {
	changes := []audit.Change{change("unmappedCount", strconv.Itoa(len(unmapped)))}

	for i, v := range unmapped {
		if i == principal.MaxUnmappedAudited {
			break
		}

		changes = append(changes, change(fmt.Sprintf("unmapped.%02d", i+1), v))
	}

	return changes
}
