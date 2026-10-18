package gate

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Approver is a person who approves a change.
type Approver struct {
	Principal

	// FreshAuth says that the person authenticated again for this approval.
	FreshAuth bool
}

// Approve records an approval on the record of id and returns the record.
// The author may not approve unless the rule says so. A person counts in
// one requirement only, so the people across rules are distinct. It returns
// ErrNotApprover with the names of the open rules when the person is in none
// of their groups.
func (e *Engine) Approve(_ context.Context, id string, who Approver) (Record, error) {
	e.Records.mu.Lock()
	defer e.Records.mu.Unlock()

	rec, ok := e.Records.byID[id]
	if !ok {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}

	if rec.Result != ResultRequireApproval {
		return Record{}, fmt.Errorf("%w: record %s result is %s", ErrNotCleared, id, rec.Result)
	}

	if slices.ContainsFunc(rec.Approvals, func(a Approval) bool { return slices.Contains(a.By, who.ID) }) {
		return Record{}, fmt.Errorf("%w: %s already approved", ErrNotApprover, who.ID)
	}

	approvals := slices.Clone(rec.Approvals)

	var open []string

	for i, a := range approvals {
		if a.Have >= a.Need {
			continue
		}

		open = append(open, a.Rule)

		if !eligibleFor(a, who.Principal) {
			continue
		}

		if who.ID == rec.Subject.Principal && !e.authorMay(a.Rule) {
			continue
		}

		if a.FreshAuth && !who.FreshAuth {
			return Record{}, fmt.Errorf("%w: rule %s", ErrFreshAuth, a.Rule)
		}

		approvals[i].Have++
		approvals[i].By = append(slices.Clone(a.By), who.ID)
		rec.Approvals = approvals
		e.Records.byID[id] = rec

		return rec, nil
	}

	switch {
	case len(open) == 0:
		return Record{}, fmt.Errorf("%w: no open requirement on %s", ErrNotApprover, id)
	case who.ID == rec.Subject.Principal:
		return Record{}, fmt.Errorf("%w: %s authored %s", ErrAuthorApproval, who.ID, id)
	default:
		return Record{}, fmt.Errorf("%w: %s is not an approver of %s", ErrNotApprover, who.ID, strings.Join(open, ", "))
	}
}

func (e *Engine) authorMay(rule string) bool {
	r, ok := e.Registry.Rule(rule)

	return ok && r.AuthorMayApprove
}

func eligibleFor(a Approval, p Principal) bool {
	for _, from := range a.From {
		kind, name, _ := strings.Cut(from, ":")

		switch kind {
		case "group":
			if slices.Contains(p.Groups, name) {
				return true
			}
		case "user":
			if p.ID == name {
				return true
			}
		default:
		}
	}

	return false
}

// Clear returns nil when the record of id lets the change go on. It
// returns ErrNotCleared with the blockers or the open rules otherwise.
func (e *Engine) Clear(ctx context.Context, id string) (Record, error) {
	rec, err := e.Records.Get(ctx, id)
	if err != nil {
		return Record{}, err
	}

	if rec.Cleared() {
		return rec, nil
	}

	return rec, fmt.Errorf("%w: record %s is %s", ErrNotCleared, id, rec.Result)
}
