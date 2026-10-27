package blob

import "context"

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

// PagedLister lists the objects under a prefix one page at a time, so that
// a caller never loads all the objects of a unit into memory in one piece.
// A caller walks the pages with Next until Next is empty. A walk sees each
// object that existed for the whole walk exactly once. An object that a
// writer adds or deletes during the walk can be in the walk or not.
//
// PagedLister is the next form of Store.List. When every adapter implements
// it, ListPage replaces List in Store, and every caller moves to it.
type PagedLister interface {
	// ListPage returns one page of the objects under req.Prefix, ordered by
	// key. It returns ErrInvalidPage for a limit out of range, and
	// ErrInvalidKey for an After that is not a valid key or that is outside
	// req.Prefix.
	ListPage(ctx context.Context, req PageRequest) (Page, error)
}
