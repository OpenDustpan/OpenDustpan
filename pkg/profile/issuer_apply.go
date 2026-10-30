package profile

import (
	"fmt"
	"slices"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// ApplyKeys returns the keys of namespace ns after the change ch, from the
// current keys of ns, deleted keys included. Every adapter of Keys calls it
// inside the transaction of Apply, so the rules of a change are the same for
// all stores.
//
// It returns ErrConflict when Initial is set and a key is not deleted, when
// a move names a key that is not in the state From, when an added key
// repeats an ID, or when the result breaks CheckKeySet. It returns
// ErrInvalid for a key that fails CheckKey or a move that fails CheckMove.
// A key that leaves the state next or active loses its Sealed.
func ApplyKeys(ns namespace.Name, current []Key, ch KeyChange) ([]Key, error) {
	out := slices.Clone(current)

	if ch.Initial {
		for i := range out {
			if out[i].State != KeyDeleted {
				return nil, fmt.Errorf("%w: the namespace already has signing keys", ErrConflict)
			}
		}
	}

	out, err := applyMoves(ns, out, ch.Move)
	if err != nil {
		return nil, err
	}

	out, err = applyAdds(ns, out, ch.Add)
	if err != nil {
		return nil, err
	}

	live := slices.DeleteFunc(slices.Clone(out), func(k Key) bool { return k.State == KeyDeleted })
	if err = CheckKeySet(live); err != nil {
		return nil, err
	}

	return out, nil
}

func applyMoves(ns namespace.Name, keys []Key, moves []KeyMove) ([]Key, error) {
	for _, m := range moves {
		if err := CheckMove(m); err != nil {
			return nil, err
		}

		i := slices.IndexFunc(keys, func(k Key) bool { return k.ID == m.ID })
		if i < 0 || keys[i].Namespace != ns || keys[i].State != m.From {
			return nil, fmt.Errorf("%w: key %s is not in the state %s", ErrConflict, m.ID, m.From)
		}

		keys[i] = moveKey(keys[i], m)
	}

	return keys, nil
}

func applyAdds(ns namespace.Name, keys, add []Key) ([]Key, error) {
	for i := range add {
		k := &add[i]

		if k.Namespace != ns {
			return nil, fmt.Errorf("%w: key %s is not a key of namespace %s", ErrInvalid, k.ID, ns)
		}

		if err := CheckKey(*k); err != nil {
			return nil, err
		}

		if slices.ContainsFunc(keys, func(o Key) bool { return o.ID == k.ID }) {
			return nil, fmt.Errorf("%w: key %s exists", ErrConflict, k.ID)
		}

		keys = append(keys, *k)
	}

	return keys, nil
}

func moveKey(k Key, m KeyMove) Key {
	k.State = m.To

	switch m.To {
	case KeyActive:
		k.ActivatedAt = m.At
	case KeyRetired:
		k.RetiredAt = m.At
	case KeyDeleted:
		k.DeletedAt = m.At
	case KeyNext:
	default:
	}

	if !m.To.HoldsPrivate() {
		k.Sealed = nil
	}

	return k
}
