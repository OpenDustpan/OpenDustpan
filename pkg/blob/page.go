package blob

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// MaxPageLimit is the largest number of objects that one page of a paged
// list holds. It matches the largest page of an S3 ListObjectsV2 answer.
const MaxPageLimit = 1000

// PageRequest selects one page of the objects under a prefix.
type PageRequest struct {
	// Prefix names a key directory such as "<unit>/", with or without a
	// trailing slash. A prefix that names no directory gives an empty page.
	Prefix string
	// After is the last key of the previous page. The page starts at the
	// first key after it. An empty After starts at the first key. An After
	// outside Prefix fails with ErrInvalidKey. The S3 adapter sends it as the
	// StartAfter parameter of ListObjectsV2, so it is a key, not an opaque
	// token.
	After Key
	// Limit is the largest number of objects in the page, from 1 to
	// MaxPageLimit. A limit outside that range fails with ErrInvalidPage.
	Limit int
}

// Page is one page of the objects under a prefix, ordered by key.
type Page struct {
	// Objects holds at most the limit of the request.
	Objects []Object
	// Next is the After of the next page. It is empty when this page is the
	// last one.
	Next Key
}

// Walk calls fn for each object under prefix, in key order, one page of
// MaxPageLimit objects at a time. It never holds more than one page. A walk
// sees each object that existed for the whole walk exactly once. It stops at
// the first error of fn or of the store and returns it.
//
// Walk checks each page before fn sees an object of it, and fails closed with
// ErrInvalidPage when a page breaks the rules of the port: more objects than
// the limit, an object at or before the key to start after, objects that are
// not in strictly increasing order, a Next that is not the last key of the
// page, or a Next with no object. So fn never sees an object twice, also on a
// store that ignores the After key.
//
// Defense in depth: an adapter and a scoped view keep these rules too. This
// check stays so that a store that breaks them cannot make a caller delete
// or count an object twice, and it does not rely on the adapter.
func Walk(ctx context.Context, s Store, prefix string, fn func(Object) error) error {
	return WalkPages(ctx, s, prefix, func(page []Object) error {
		for _, obj := range page {
			if err := fn(obj); err != nil {
				return err
			}
		}

		return nil
	})
}

// WalkPages calls fn with each page of the objects under prefix, in key
// order, one page of MaxPageLimit objects at a time. It makes the checks of
// Walk, and Walk is built on it. A caller that must act on a whole page, for
// example one that reads a set of references once for the page, uses it. The
// slice is only valid during the call of fn. An empty last page does not reach
// fn.
func WalkPages(ctx context.Context, s Store, prefix string, fn func([]Object) error) error {
	var after Key

	for {
		page, err := s.ListPage(ctx, PageRequest{Prefix: prefix, After: after, Limit: MaxPageLimit})
		if err != nil {
			return err
		}

		if err := checkPage(page, after); err != nil {
			return fmt.Errorf("list %q: %w", prefix, err)
		}

		if len(page.Objects) > 0 {
			if err := fn(page.Objects); err != nil {
				return err
			}
		}

		if page.Next == "" {
			return nil
		}

		after = page.Next
	}
}

// checkPage fails with ErrInvalidPage unless page follows the rules of the
// port for a request that starts after the key after.
func checkPage(page Page, after Key) error {
	if len(page.Objects) > MaxPageLimit {
		return fmt.Errorf("page holds %d objects: %w", len(page.Objects), ErrInvalidPage)
	}

	last := after

	for _, obj := range page.Objects {
		if obj.Key <= last {
			return fmt.Errorf("object %q is not after %q: %w", obj.Key, last, ErrInvalidPage)
		}

		last = obj.Key
	}

	if page.Next != "" && (len(page.Objects) == 0 || page.Next != last) {
		return fmt.Errorf("next key %q is not the last key of the page: %w", page.Next, ErrInvalidPage)
	}

	return nil
}

// Dir returns the key directory that Prefix names, with one trailing slash,
// or "" for the whole store.
func (r PageRequest) Dir() string {
	dir := strings.Trim(r.Prefix, "/")
	if dir == "" {
		return ""
	}

	return dir + "/"
}

// Validate checks the request. It returns ErrInvalidPage for a limit out of
// range, and ErrInvalidKey for an After that is not a valid key or that is
// not inside the directory of Prefix. An adapter calls it before it reads the
// store.
//
// Defense in depth: the callers build the request from keys that they made
// with ParseKey. This check stays so that a request made by hand cannot make
// an adapter list from a position outside the prefix.
func (r PageRequest) Validate() error {
	if r.Limit < 1 || r.Limit > MaxPageLimit {
		return fmt.Errorf("limit %d is not from 1 to %d: %w", r.Limit, MaxPageLimit, ErrInvalidPage)
	}

	if r.After == "" {
		return nil
	}

	if _, err := ParseKey(r.After.String()); err != nil {
		return err
	}

	if !strings.HasPrefix(r.After.String(), r.Dir()) {
		return fmt.Errorf("after key %q is outside prefix %q: %w", r.After, r.Prefix, ErrInvalidKey)
	}

	return nil
}

// PageOf cuts the page that req selects from objects, which must be ordered
// by key and hold only keys under the prefix of req. It is for adapters that
// hold the whole listing already. It does not validate req.
func PageOf(objects []Object, req PageRequest) Page {
	if len(objects) == 0 {
		return Page{Objects: nil, Next: ""}
	}

	start, _ := slices.BinarySearchFunc(objects, req.After, func(o Object, after Key) int {
		if after == "" {
			return 1
		}

		return strings.Compare(o.Key.String(), after.String())
	})

	// BinarySearchFunc finds the first object not before After. The page
	// starts after After, so it skips an object whose key equals After.
	if start < len(objects) && req.After != "" && objects[start].Key == req.After {
		start++
	}

	if start >= len(objects) {
		return Page{Objects: nil, Next: ""}
	}

	end := min(start+req.Limit, len(objects))
	page := Page{Objects: slices.Clone(objects[start:end]), Next: ""}

	if end < len(objects) && end > start {
		page.Next = objects[end-1].Key
	}

	return page
}
