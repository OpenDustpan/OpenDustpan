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

// RotationOptions say how a rotation runs. Promote says that a promotion may
// run. A namespace with an issuerURL sets it only when the copy check found
// the next key. A scheduled step with no promotion still deletes old retired
// keys and makes the next key. A normal rotation with no promotion returns
// ErrIssuerKeyMissing.
type RotationOptions struct {
	Kind    RotationKind
	Promote bool
}

// PlanRotation plans one rotation step at now for the live keys of a
// namespace (the states next, active and retired).
//
// A normal rotation with no next key makes one and promotes nothing: a
// relying party must see a key in the JWKS before the key signs.
func PlanRotation(keys []Key, now time.Time, o RotationOptions) (RotationPlan, error) {
	switch o.Kind {
	case RotateEmergency:
		return planEmergency(keys, now), nil
	case RotateNormal, RotateScheduled:
		return planStep(keys, now, o)
	default:
		return RotationPlan{}, fmt.Errorf("%w: rotation kind %q", ErrInvalid, o.Kind)
	}
}

func planEmergency(keys []Key, now time.Time) RotationPlan {
	plan := RotationPlan{Moves: nil, NewActive: true, NewNext: true}

	for i := range keys {
		if keys[i].State != KeyDeleted {
			plan.Moves = append(plan.Moves, KeyMove{At: now, ID: keys[i].ID, From: keys[i].State, To: KeyDeleted})
		}
	}

	return plan
}

func byState(keys []Key, s KeyState) []Key {
	var out []Key

	for i := range keys {
		if keys[i].State == s {
			out = append(out, keys[i])
		}
	}

	slices.SortFunc(out, func(a, b Key) int { return a.CreatedAt.Compare(b.CreatedAt) })

	return out
}

// stepPlan builds the plan of one scheduled or normal step. It remembers the
// keys that the plan deletes, so no key moves twice.
type stepPlan struct {
	plan RotationPlan
	gone map[KeyID]bool
	now  time.Time
}

func (p *stepPlan) move(k *Key, to KeyState) {
	p.plan.Moves = append(p.plan.Moves, KeyMove{At: p.now, ID: k.ID, From: k.State, To: to})

	if to == KeyDeleted {
		p.gone[k.ID] = true
	}
}

// deleteOld deletes the retired keys that stayed for RetiredKeyKeep.
func (p *stepPlan) deleteOld(retired []Key) {
	for i := range retired {
		if !retired[i].RetiredAt.Add(RetiredKeyKeep).After(p.now) {
			p.move(&retired[i], KeyDeleted)
		}
	}
}

// promote retires the active key and activates the next key. The JWKS holds at
// most MaxRetiredKeys retired keys: the key that retires now needs a place, so
// the oldest retired keys go first.
func (p *stepPlan) promote(cur, next *Key, retired []Key) {
	p.move(cur, KeyRetired)
	p.move(next, KeyActive)

	live := 0

	for i := range retired {
		if !p.gone[retired[i].ID] {
			live++
		}
	}

	for i := range retired {
		if live < MaxRetiredKeys {
			break
		}

		if !p.gone[retired[i].ID] {
			p.move(&retired[i], KeyDeleted)

			live--
		}
	}
}

func planStep(keys []Key, now time.Time, o RotationOptions) (RotationPlan, error) {
	p := &stepPlan{plan: RotationPlan{Moves: nil, NewActive: false, NewNext: false}, gone: map[KeyID]bool{}, now: now}

	active, next, retired := byState(keys, KeyActive), byState(keys, KeyNext), byState(keys, KeyRetired)

	p.deleteOld(retired)

	if len(active) == 0 {
		// No key: the first make of keys is not a rotation.
		return p.plan, nil
	}

	cur := &active[0]
	end := cur.ActivatedAt.Add(ActiveKeyLifetime)
	normal := o.Kind == RotateNormal

	if normal && !o.Promote && len(next) > 0 {
		return RotationPlan{}, fmt.Errorf("%w: the copy check did not find the next key", ErrIssuerKeyMissing)
	}

	// A scheduled step promotes at the end of the lifetime. A normal rotation
	// promotes now. A next key that this step makes never signs in this step.
	if len(next) > 0 && o.Promote && (normal || !end.After(now)) {
		p.promote(cur, &next[0], retired)
		p.plan.NewNext = normal

		return p.plan, nil
	}

	if len(next) == 0 && (normal || !end.Add(-NextKeyLead).After(now)) {
		p.plan.NewNext = true
	}

	return p.plan, nil
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

	for i := range keys {
		k := &keys[i]
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
