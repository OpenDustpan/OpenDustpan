package codec

import (
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
)

// ItemKeyRow is one row of item_keys with the name of its namespace, in the
// column types of both dialects.
type ItemKeyRow struct {
	ID           string
	Namespace    string
	ScopeID      string
	Kind         string
	Item         string
	WrapperID    string
	CreatedAtMs  int64
	ShreddedAtMs int64
}

// ToItemKeyRow converts a record into a row. ShreddedAtMs is 0 for a record
// that is not shredded.
func ToItemKeyRow(k secret.ItemKey) ItemKeyRow {
	return ItemKeyRow{
		ID: string(k.ID), Namespace: k.Ref.Scope.Namespace.String(), ScopeID: k.Ref.Scope.ID,
		Kind: string(k.Ref.Kind), Item: k.Ref.Item, WrapperID: k.WrapperID,
		CreatedAtMs: Millis(k.CreatedAt), ShreddedAtMs: OptionalMillis(k.ShreddedAt),
	}
}

// FromItemKeyRow converts a row into a record and checks it again with the
// rules of a new record, so a row that another version or a restore wrote
// is never served. It returns an error that wraps secret.ErrInvalidItemRef
// for a row that fails.
//
// Defense in depth: the store checks the record before it writes it. This
// check stays so that a damaged row does not open content of another item.
func FromItemKeyRow(r ItemKeyRow) (secret.ItemKey, error) {
	ns, err := namespace.ParseName(r.Namespace)
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("%w: namespace of item key %q", secret.ErrInvalidItemRef, r.ID)
	}

	scope, err := secret.NewScope(ns, r.ScopeID)
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("item key %q: %w", r.ID, err)
	}

	ref, err := secret.NewItemRef(scope, secret.ItemKind(r.Kind), r.Item)
	if err != nil {
		return secret.ItemKey{}, fmt.Errorf("item key %q: %w", r.ID, err)
	}

	id, err := secret.ParseItemKeyID(r.ID)
	if err != nil {
		return secret.ItemKey{}, err
	}

	if r.WrapperID == "" {
		return secret.ItemKey{}, fmt.Errorf("%w: item key %q has no wrapper ID", secret.ErrInvalidItemRef, r.ID)
	}

	return secret.ItemKey{
		CreatedAt: FromMillis(r.CreatedAtMs), ShreddedAt: FromOptionalMillis(r.ShreddedAtMs),
		Ref: ref, ID: id, WrapperID: r.WrapperID,
	}, nil
}

// maxItemKeyPage bounds one page of ListItemKeys.
const maxItemKeyPage = 1000

// CheckNewItemKey returns the error of a create of k under fence: the
// record must be valid and not shredded, and its namespace must be the
// namespace of the fence. It returns an error that wraps
// secret.ErrInvalidItemRef or namespace.ErrFenced.
//
// Defense in depth: the fence check of the transaction compares the
// namespace row with the fence only. This check stays so that a record of
// one namespace never lands under the fence of another namespace, whoever
// calls the store.
func CheckNewItemKey(k secret.ItemKey, fence namespace.Fence) error {
	if _, err := secret.NewItemRef(k.Ref.Scope, k.Ref.Kind, k.Ref.Item); err != nil {
		return fmt.Errorf("item key: %w", err)
	}

	if _, err := secret.ParseItemKeyID(string(k.ID)); err != nil {
		return err
	}

	switch {
	case k.WrapperID == "":
		return fmt.Errorf("%w: item key %q has no wrapper ID", secret.ErrInvalidItemRef, k.ID)
	case !k.ShreddedAt.IsZero():
		return fmt.Errorf("%w: item key %q is created shredded", secret.ErrInvalidItemRef, k.ID)
	case k.CreatedAt.IsZero():
		return fmt.Errorf("%w: item key %q has no creation time", secret.ErrInvalidItemRef, k.ID)
	case k.Ref.Scope.Namespace != fence.Namespace:
		return fmt.Errorf("%w: item key names %q, fence names %q", namespace.ErrFenced,
			k.Ref.Scope.Namespace, fence.Namespace)
	default:
		return nil
	}
}

// CheckShredItemKey returns the error of a shred of ref at the time at under
// fence, with the same rules as CheckNewItemKey.
func CheckShredItemKey(ref secret.ItemRef, at time.Time, fence namespace.Fence) error {
	if _, err := secret.NewItemRef(ref.Scope, ref.Kind, ref.Item); err != nil {
		return fmt.Errorf("item key: %w", err)
	}

	switch {
	case at.IsZero() || at.UnixMilli() <= 0:
		return fmt.Errorf("%w: a shred needs a time", secret.ErrInvalidItemRef)
	case ref.Scope.Namespace != fence.Namespace:
		return fmt.Errorf("%w: item key names %q, fence names %q", namespace.ErrFenced,
			ref.Scope.Namespace, fence.Namespace)
	default:
		return nil
	}
}

// CheckListItemKeys returns the error of a list of the records of scope with
// at most limit records.
func CheckListItemKeys(scope secret.Scope, limit int) error {
	if _, err := secret.NewScope(scope.Namespace, scope.ID); err != nil {
		return fmt.Errorf("list item keys: %w", err)
	}

	if limit < 1 || limit > maxItemKeyPage {
		return fmt.Errorf("%w: a page has 1 to %d records, got %d", secret.ErrInvalidItemRef, maxItemKeyPage, limit)
	}

	return nil
}
