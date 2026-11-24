package job

import (
	"slices"

	"opendustpan.org/opendustpan/pkg/gate"
)

// GatesProgress returns the result of approvals for all gates of one phase,
// as GatesStatus does, and the number of slots that each approval gate has
// filled, by gate name.
//
// The approval gates share one pool of approvers: one subject fills at most
// one slot of one gate, and a maximum matching assigns the subjects to the
// slots, so that the order of the approvals never changes the result. A
// confirmation gate keeps its own rule (Gate.Status) and fills no slot of the
// matching. The map has no entry for a confirmation gate.
func GatesProgress(
	gates []Gate, approvals []Approval, startedBy string, policy gate.ApproverPolicy,
) (GateStatus, map[string]int) {
	filled := make(map[string]int, len(gates))

	if len(gates) > 0 && slices.ContainsFunc(approvals, func(a Approval) bool { return a.Verdict == VerdictReject }) {
		return GateRejected, filled
	}

	status := GateOpen

	var approvalGates []Gate

	for i := range gates {
		g := &gates[i]
		if typeOf(g.Type) != GateConfirmation {
			approvalGates = append(approvalGates, *g)

			continue
		}

		if g.Status(approvals, startedBy, policy) != GateOpen {
			status = GateWaiting
		}
	}

	if len(approvalGates) == 0 {
		return status, filled
	}

	byGate, open := matchGates(approvalGates, approvals, startedBy, policy)
	for name, n := range byGate {
		filled[name] += n
	}

	if !open {
		status = GateWaiting
	}

	return status, filled
}

// matchGates assigns the approvers to the slots of gates with a maximum
// matching (augmenting paths), and returns the slots that each gate has
// filled by gate name, and whether every slot is filled. gates holds approval
// gates only.
//
// An approval is a candidate for a gate when Gate.counts accepts it and the
// name of the gate is in its For. A subject with several approvals is one
// candidate, for the gates that every one of its approvals names. An approval
// with an empty For is no candidate. The size is at most MaxApprovalGates
// gates, so the cost is bounded. A set of more gates is never open.
func matchGates(
	gates []Gate, approvals []Approval, startedBy string, policy gate.ApproverPolicy,
) (map[string]int, bool) {
	filled := make(map[string]int, len(gates))

	// Defense in depth: the start refuses a phase with more gates (checkGates)
	// and the decider never gives them. This check stays so that a stored job
	// of a hostile or damaged shape cannot open, and does not depend on both.
	if len(gates) > MaxApprovalGates {
		return filled, false
	}

	// Defense in depth: an approval names its gates by name, so two gates with
	// one name cannot be told apart, and a person who holds one of them would
	// hold both. checkGates refuses such a set at the start, and the decider
	// gives unique names. This check stays so that a stored job of another
	// shape cannot open, and does not rely on either.
	if repeatedName(gates) {
		return filled, false
	}

	subjects, eligible := candidates(gates, approvals, startedBy, policy)
	assigned := make([][]int, len(gates)) // for each gate, the subjects in its slots

	for s := range subjects {
		visited := make([]bool, len(gates))
		augment(s, eligible, gates, assigned, visited)
	}

	open := true

	for i := range gates {
		filled[gates[i].Name] += len(assigned[i])

		if len(assigned[i]) < gates[i].Approvals {
			open = false
		}
	}

	return filled, open
}

// repeatedName reports whether two of gates have the same name.
func repeatedName(gates []Gate) bool {
	for i := range gates {
		for j := i + 1; j < len(gates); j++ {
			if gates[i].Name == gates[j].Name {
				return true
			}
		}
	}

	return false
}

// candidates returns the distinct subjects that may fill a slot, in order of
// first approval, and for each subject the indexes of the gates it may fill.
func candidates(
	gates []Gate, approvals []Approval, startedBy string, policy gate.ApproverPolicy,
) ([]string, [][]int) {
	subjects := make([]string, 0, len(approvals))
	eligible := make([][]int, 0, len(approvals))

	index := make(map[string]int, len(approvals))

	for i := range approvals {
		a := &approvals[i]

		if a.Agent != "" || a.Verdict != VerdictApprove || typeOf(a.Gate) != GateApproval {
			continue
		}

		mine := make([]int, 0, len(gates))

		for gi := range gates {
			if gates[gi].counts(a, startedBy, policy) && slices.Contains(a.For, gates[gi].Name) {
				mine = append(mine, gi)
			}
		}

		at, seen := index[a.Subject]
		if !seen {
			index[a.Subject] = len(subjects)
			subjects = append(subjects, a.Subject)
			eligible = append(eligible, mine)

			continue
		}

		// Two approvals of one subject make one candidate. The candidate holds
		// only the gates that every approval allows, which does not depend on
		// the order of the approvals.
		eligible[at] = slices.DeleteFunc(eligible[at], func(gi int) bool { return !slices.Contains(mine, gi) })
	}

	return subjects, eligible
}

// augment tries to give subject s a slot, and moves earlier subjects to other
// gates when that is needed. It reports whether s has a slot.
func augment(s int, eligible [][]int, gates []Gate, assigned [][]int, visited []bool) bool {
	for _, gi := range eligible[s] {
		if visited[gi] {
			continue
		}

		visited[gi] = true

		if len(assigned[gi]) < gates[gi].Approvals {
			assigned[gi] = append(assigned[gi], s)

			return true
		}

		for k, other := range assigned[gi] {
			if augment(other, eligible, gates, assigned, visited) {
				assigned[gi][k] = s

				return true
			}
		}
	}

	return false
}
