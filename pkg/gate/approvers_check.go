package gate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// checkApprovers returns a with each group name resolved to the ID of the
// group, or an error that wraps ErrApprovers when no one could meet an
// approval that asks for count approvals from a. It checks that each group
// name is valid and names a group that exists, that each user name is a name,
// and, when the unit owners are not an approver, that the people who can
// approve are at least count. An approver list that names no one means any
// holder of the approve action, so it has no upper bound to check.
//
// The result holds the current name of each group in Groups and its ID in
// GroupIDs, each group once. It drops the GroupIDs that the caller gave: only
// the directory says which group a name is.
//
// The people who can approve are the users that a names, each once, and the
// enabled members of the groups that a names. A disabled principal cannot
// approve, so it does not count. When fresh asks for a fresh authentication, a
// window of any length, only a person can give it, so only the persons among
// the members count; otherwise a service principal or a workload with the
// approve action can sign too, and counts. A person who is in two of the groups
// counts twice, so the check refuses a count only when even this upper bound is
// below it. A group can get members later, but a count above its members now
// cannot be met now, and the error says how many people can approve.
//
// Defense in depth: ApprovalDefault.Validate and ApprovalRule.Validate check
// the shape of the lists and the count; this check does not rely on them, and
// they do not rely on it.
func (s *ConfigService) checkApprovers(
	ctx context.Context, a Approvers, count int, fresh FreshAuth,
) (Approvers, error) {
	users, err := countUsers(a.Users)
	if err != nil {
		return a, err
	}

	groups, members, err := s.resolveGroups(ctx, a.Groups, fresh)
	if err != nil {
		return a, err
	}

	a.Groups, a.GroupIDs = make([]string, len(groups)), make([]string, len(groups))

	for i, g := range groups {
		a.Groups[i], a.GroupIDs[i] = g.Name, g.ID
	}

	people := users + members

	if a.Empty() || a.UnitOwners || int64(count) <= people {
		return a, nil
	}

	return a, &ApproversError{
		Reason: ApproversCountAbove, Count: int64(count), People: people,
		detail: fmt.Sprintf(
			"count %d is more than the number of people who can approve (%d): lower the count or add approvers",
			count, people,
		),
	}
}

// currentNames returns a with the current name of each group that GroupIDs
// names, so a rename of a group shows in the rule. A group that no longer
// exists keeps its stored name, and it counts no one. A group with no ID, from
// a rule saved before the IDs existed, keeps its stored name too.
func (s *ConfigService) currentNames(ctx context.Context, a Approvers) (Approvers, error) {
	if len(a.GroupIDs) != len(a.Groups) {
		return a, nil
	}

	names := slices.Clone(a.Groups)

	for i, id := range a.GroupIDs {
		ref, _, err := s.groups.GroupByID(ctx, id)

		switch {
		case errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			return a, fmt.Errorf("read the group %q: %w", id, err)
		default:
		}

		names[i] = ref.Name
	}

	a.Groups = names

	return a, nil
}

// countUsers checks the user names and returns how many different users they
// name.
func countUsers(users []string) (int64, error) {
	seen := make(map[string]struct{}, len(users))

	for _, user := range users {
		if err := checkUserName(user); err != nil {
			return 0, err
		}

		seen[user] = struct{}{}
	}

	return int64(len(seen)), nil
}

// ResolveGroups resolves each group name to the group with that name now, and
// returns the groups with their IDs, each group once (a name in another case
// is the same group). It returns an error that wraps ErrApprovers, with the
// reason ApproversGroupInvalid for a name that is not valid and
// ApproversGroupMissing for a group that does not exist. Every code that
// turns the group names of a rule or a policy into an enforced approver list
// calls it, so the enforcement matches a member by ID and never by name.
func ResolveGroups(ctx context.Context, dir GroupResolver, names []string) ([]GroupRef, error) {
	seen := make(map[string]struct{}, len(names))
	groups := make([]GroupRef, 0, len(names))

	for _, name := range names {
		ref, err := dir.ResolveGroup(ctx, name)

		switch {
		case errors.Is(err, ErrInvalid):
			return nil, newApproversError(ApproversGroupInvalid,
				fmt.Sprintf("the group name %q is not valid: use letters, digits, '-', '_' and '.'", name))
		case errors.Is(err, ErrNotFound):
			return nil, newApproversError(ApproversGroupMissing,
				fmt.Sprintf("the group %q does not exist: make the group first", name))
		case err != nil:
			return nil, fmt.Errorf("read the group %q: %w", name, err)
		default:
		}

		if _, again := seen[ref.ID]; again {
			continue
		}

		seen[ref.ID] = struct{}{}

		groups = append(groups, ref)
	}

	return groups, nil
}

// resolveGroups resolves the group names and returns the groups and the
// current members of them. It counts the members by the ID of the group, so a
// group that someone deleted and made again between the two reads is not
// counted.
func (s *ConfigService) resolveGroups(ctx context.Context, names []string, fresh FreshAuth) ([]GroupRef, int64, error) {
	resolved, err := ResolveGroups(ctx, s.groups, names)
	if err != nil {
		return nil, 0, err
	}

	groups := make([]GroupRef, 0, len(resolved))
	people := int64(0)

	for _, ref := range resolved {
		current, members, err := s.groups.GroupByID(ctx, ref.ID)

		switch {
		case errors.Is(err, ErrNotFound):
			return nil, 0, newApproversError(ApproversGroupMissing,
				fmt.Sprintf("the group %q does not exist: make the group first", ref.Name))
		case err != nil:
			return nil, 0, fmt.Errorf("read the group %q: %w", ref.ID, err)
		default:
		}

		groups = append(groups, current)

		if fresh.Set {
			people += members.Persons
		} else {
			people += members.Enabled
		}
	}

	return groups, people, nil
}

// checkUserName returns an error that wraps ErrApprovers for a user name that is
// not a name: one that is not UTF-8, one with a control or format character, or
// with white space at its start or its end. The name of a user from an identity
// provider can hold characters that an API subject cannot, so the check is not
// stricter.
func checkUserName(name string) error {
	if !utf8.ValidString(name) {
		// Defense in depth: Approvers.Validate refuses a name that is not
		// UTF-8 first. This check stays so that a new caller cannot skip it.
		return newApproversError(ApproversUserInvalid, "the user name is not UTF-8")
	}

	if strings.TrimSpace(name) != name {
		return newApproversError(ApproversUserInvalid,
			fmt.Sprintf("the user name %q starts or ends with white space", name))
	}

	for _, c := range name {
		if unicode.IsControl(c) || unicode.Is(unicode.Cf, c) {
			return newApproversError(ApproversUserInvalid,
				fmt.Sprintf("the user name %q holds a control or format character", name))
		}
	}

	return nil
}
