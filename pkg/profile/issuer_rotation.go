package profile

import (
	"fmt"
	"slices"
	"time"
)

// RotationKind says why a rotation runs.
type RotationKind string

// The kinds of rotation.
const (
	// RotateScheduled is the daily step of the schedule.
	RotateScheduled RotationKind = "scheduled"
	// RotateNormal is a manual rotation: the next key becomes active.
	RotateNormal RotationKind = "normal"
	// RotateEmergency replaces every key.
	RotateEmergency RotationKind = "emergency"
)

// RotationPlan is the result of PlanRotation. The caller makes a key for
// each of NewActive and NewNext, adds it, and applies Moves.
type RotationPlan struct {
	Moves     []KeyMove
	NewActive bool
	NewNext   bool
}

// Empty reports whether the plan changes nothing.
func (p RotationPlan) Empty() bool {
	return len(p.Moves) == 0 && !p.NewActive && !p.NewNext
}

// Promoted reports whether the plan makes a next key active.
func (p RotationPlan) Promoted() bool {
	return slices.ContainsFunc(p.Moves, func(m KeyMove) bool { return m.To == KeyActive })
}

// Deleted returns the IDs that the plan deletes.
func (p RotationPlan) Deleted() []KeyID {
	var out []KeyID

	for _, m := range p.Moves {
		if m.To == KeyDeleted {
			out = append(out, m.ID)
		}
	}

	return out
}

// PlanRotation plans one rotation step at now for the live keys of a
// namespace (the states next, active and retired).
//
// promote says that a promotion may run. A namespace with an issuerURL sets
// it only when the copy check found the next key. A scheduled step with no
// promotion still deletes old retired keys and makes the next key. A normal
// rotation with promote false returns ErrIssuerKeyMissing.
//
// A normal rotation with no next key makes one and promotes nothing: a
// relying party must see a key in the JWKS before the key signs.
func PlanRotation(keys []Key, now time.Time, kind RotationKind, promote bool) (RotationPlan, error) {
	switch kind {
	case RotateEmergency:
		return planEmergency(keys, now), nil
	case RotateNormal, RotateScheduled:
		return planStep(keys, now, kind, promote)
	default:
		return RotationPlan{}, fmt.Errorf("%w: rotation kind %q", ErrInvalid, kind)
	}
}

func planEmergency(keys []Key, now time.Time) RotationPlan {
	plan := RotationPlan{Moves: nil, NewActive: true, NewNext: true}

	for _, k := range keys {
		if k.State != KeyDeleted {
			plan.Moves = append(plan.Moves, KeyMove{At: now, ID: k.ID, From: k.State, To: KeyDeleted})
		}
	}

	return plan
}

func byState(keys []Key, s KeyState) []Key {
	var out []Key

	for _, k := range keys {
		if k.State == s {
			out = append(out, k)
		}
	}

	slices.SortFunc(out, func(a, b Key) int { return a.CreatedAt.Compare(b.CreatedAt) })

	return out
}

func planStep(keys []Key, now time.Time, kind RotationKind, promote bool) (RotationPlan, error) {
	var plan RotationPlan

	active := byState(keys, KeyActive)
	next := byState(keys, KeyNext)
	retired := byState(keys, KeyRetired)
	gone := map[KeyID]bool{}

	del := func(k Key) {
		plan.Moves = append(plan.Moves, KeyMove{At: now, ID: k.ID, From: k.State, To: KeyDeleted})
		gone[k.ID] = true
	}

	for _, k := range retired {
		if !k.RetiredAt.Add(RetiredKeyKeep).After(now) {
			del(k)
		}
	}

	if len(active) == 0 {
		// No key: the first make of keys is not a rotation.
		return plan, nil
	}

	cur := active[0]
	due := !cur.ActivatedAt.Add(ActiveKeyLifetime).After(now)

	if kind == RotateNormal && !promote && len(next) > 0 {
		return RotationPlan{}, fmt.Errorf("%w: the copy check did not find the next key", ErrIssuerKeyMissing)
	}

	// A scheduled step promotes at the end of the lifetime. A normal rotation
	// promotes now. A next key that this step makes never signs in this step.
	if len(next) > 0 && promote && (kind == RotateNormal || due) {
		plan.Moves = append(plan.Moves,
			KeyMove{At: now, ID: cur.ID, From: KeyActive, To: KeyRetired},
			KeyMove{At: now, ID: next[0].ID, From: KeyNext, To: KeyActive},
		)

		live := 0

		for _, k := range retired {
			if !gone[k.ID] {
				live++
			}
		}

		// The JWKS holds at most MaxRetiredKeys retired keys: the key that
		// retires now needs a place, so the oldest retired keys go first.
		for _, k := range retired {
			if live < MaxRetiredKeys {
				break
			}

			if !gone[k.ID] {
				del(k)

				live--
			}
		}

		plan.NewNext = kind == RotateNormal

		return plan, nil
	}

	if len(next) == 0 && (kind == RotateNormal || !cur.ActivatedAt.Add(ActiveKeyLifetime-NextKeyLead).After(now)) {
		plan.NewNext = true
	}

	return plan, nil
}

// CheckKey checks the shape of a key before a store adds it.
func CheckKey(k Key) error {
	switch {
	case k.ID == "" || !k.State.Valid() || k.Namespace == "" || k.CreatedAt.IsZero():
		return fmt.Errorf("%w: a key needs an ID, a state, a namespace and a time", ErrInvalid)
	case len(k.Public) == 0:
		return fmt.Errorf("%w: key %s has no public key", ErrInvalid, k.ID)
	case k.State.HoldsPrivate() != (len(k.Sealed) > 0):
		return fmt.Errorf("%w: key %s in the state %s has a wrong sealed private key", ErrConflict, k.ID, k.State)
	}

	if _, err := ParseKeyID(string(k.ID)); err != nil {
		return err
	}

	return nil
}

// CheckMove checks the shape of a move.
func CheckMove(m KeyMove) error {
	if m.ID == "" || !m.From.Valid() || !m.To.Valid() || m.From == m.To || m.From == KeyDeleted || m.At.IsZero() {
		return fmt.Errorf("%w: key move %q from %q to %q", ErrInvalid, m.ID, m.From, m.To)
	}

	return nil
}

// CheckKeySet checks the rules for the keys of one namespace after a change:
// at most one active key, at most one next key, at most MaxRetiredKeys
// retired keys, and sealed private keys only for next and active keys. A
// store calls it on the result of Apply in the same transaction.
func CheckKeySet(keys []Key) error {
	count := map[KeyState]int{}

	for _, k := range keys {
		count[k.State]++

		if k.State.HoldsPrivate() != (len(k.Sealed) > 0) {
			return fmt.Errorf("%w: key %s in the state %s has a wrong sealed private key", ErrConflict, k.ID, k.State)
		}
	}

	switch {
	case count[KeyActive] > 1:
		return fmt.Errorf("%w: the namespace would have two active keys", ErrConflict)
	case count[KeyNext] > 1:
		return fmt.Errorf("%w: the namespace would have two next keys", ErrConflict)
	case count[KeyRetired] > MaxRetiredKeys:
		return fmt.Errorf("%w: the namespace would have more than %d retired keys", ErrConflict, MaxRetiredKeys)
	case count[KeyNext] > 0 && count[KeyActive] == 0:
		return fmt.Errorf("%w: the namespace would have a next key and no active key", ErrConflict)
	}

	return nil
}
