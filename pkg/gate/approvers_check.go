package gate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// checkApprovers returns an error that wraps ErrApprovers when no one could meet
// an approval that asks for count approvals from a. It checks that each group
// name is valid and names a group that exists, that each user name is a name,
// and, when the unit owners are not an approver, that the people who can
// approve are at least count. An approver list that names no one means any
// holder of the approve action, so it has no upper bound to check.
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
func (s *ConfigService) checkApprovers(ctx context.Context, a Approvers, count int, fresh FreshAuth) error {
	users, err := countUsers(a.Users)
	if err != nil {
		return err
	}

	members, err := s.countGroupMembers(ctx, a.Groups, fresh)
	if err != nil {
		return err
	}

	people := users + members

	if a.Empty() || a.UnitOwners || int64(count) <= people {
		return nil
	}

	return &ApproversError{
		Reason: ApproversCountAbove, Count: int64(count), People: people,
		detail: fmt.Sprintf(
			"count %d is more than the number of people who can approve (%d): lower the count or add approvers",
			count, people,
		),
	}
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

// countGroupMembers checks that each group name is valid and names a group
// that exists, and returns the current members of the groups, each group once
// (a name in another case is the same group).
func (s *ConfigService) countGroupMembers(ctx context.Context, groups []string, fresh FreshAuth) (int64, error) {
	seen := make(map[string]struct{}, len(groups))
	people := int64(0)

	for _, group := range groups {
		key := strings.ToLower(group)
		if _, again := seen[key]; again {
			continue
		}

		seen[key] = struct{}{}

		members, err := s.groups.GroupMembers(ctx, group)

		switch {
		case errors.Is(err, ErrInvalid):
			return 0, newApproversError(ApproversGroupInvalid,
				fmt.Sprintf("the group name %q is not valid: use letters, digits, '-', '_' and '.'", group))
		case errors.Is(err, ErrNotFound):
			return 0, newApproversError(ApproversGroupMissing,
				fmt.Sprintf("the group %q does not exist: make the group first", group))
		case err != nil:
			return 0, fmt.Errorf("read the group %q: %w", group, err)
		default:
		}

		if fresh.Set {
			people += members.Persons
		} else {
			people += members.Enabled
		}
	}

	return people, nil
}

// checkUserName returns an error that wraps ErrApprovers for a user name that is
// not a name: one with a control or format character, or with white space at
// its start or its end. The name of a user from an identity provider can hold
// characters that an API subject cannot, so the check is not stricter.
func checkUserName(name string) error {
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
