package principal

import (
	"encoding/base64"
	"fmt"
	"slices"
	"unicode/utf8"
)

// Page limits of the list operations.
const (
	// DefaultLimit is the page size when a request names none.
	DefaultLimit = 100
	// MaxLimit is the largest page size a request can name.
	MaxLimit = 1000
)

// PageRequest asks for one page of a list. Prefix keeps the records whose
// sort key starts with it: the subject of a principal or a member, the name
// of a group without regard to case. Cursor is the opaque NextCursor of the
// page before, or empty for the first page. Limit is 1 to MaxLimit, and 0
// means DefaultLimit.
type PageRequest struct {
	Prefix string
	Cursor string
	Limit  int
}

// Page is one page of a list. Total is the count of every record that
// matches the prefix, on every page. NextCursor is empty on the last page.
type Page[T any] struct {
	Items      []T
	NextCursor string
	Total      int64
}

// Query is a PageRequest as a store reads it: the records whose key is after
// After, at or after Low and, when High is not empty, before High, in key
// order, at most Limit of them. Low and High bound the keys that start with
// the prefix. Every bound is valid UTF-8. A list also answers its total: the
// count of the records between Low and High, with no regard to After.
type Query struct {
	After string
	Low   string
	High  string
	Limit int
}

// query validates req and returns the Query for a store. It asks for one
// row more than the page holds, so the Service can tell whether a next page
// exists. key maps the prefix to the sort key, for example to lower case.
func (req PageRequest) query(key func(string) string) (Query, int, error) {
	limit := req.Limit

	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 1 || limit > MaxLimit:
		return Query{}, 0, fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalid, req.Limit, MaxLimit)
	default:
	}

	if !utf8.ValidString(req.Prefix) || len(req.Prefix) > maxSubjectLength {
		return Query{}, 0, fmt.Errorf("%w: prefix", ErrInvalid)
	}

	after, err := decodeCursor(req.Cursor)
	if err != nil {
		return Query{}, 0, err
	}

	low := key(req.Prefix)

	return Query{After: after, Low: low, High: prefixEnd(low), Limit: limit + 1}, limit, nil
}

// pageOf trims rows to limit and sets the cursor of the next page from the
// key of the last row it keeps.
func pageOf[T any](rows []T, total int64, limit int, key func(T) string) Page[T] {
	if len(rows) <= limit {
		return Page[T]{Items: rows, NextCursor: "", Total: total}
	}

	rows = rows[:limit]

	return Page[T]{Items: rows, NextCursor: encodeCursor(key(rows[len(rows)-1])), Total: total}
}

// prefixEnd returns the first string after every string that starts with
// prefix, or "" when no bound exists. It increments the last rune that can
// grow. UTF-8 orders strings as their code points do, so for valid UTF-8 the
// range [prefix, prefixEnd) holds exactly the strings with the prefix.
func prefixEnd(prefix string) string {
	runes := []rune(prefix)

	for i, last := range slices.Backward(runes) {
		next := last + 1
		if next == surrogateFirst {
			next = surrogateLast + 1
		}

		if next <= utf8.MaxRune {
			return string(append(runes[:i:i], next))
		}
	}

	return ""
}

// The UTF-16 surrogates, which are no valid rune.
const (
	surrogateFirst = 0xD800
	surrogateLast  = 0xDFFF
)

// encodeCursor makes the opaque cursor of a sort key.
func encodeCursor(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

// decodeCursor reads a cursor of encodeCursor. An empty cursor is the start.
func decodeCursor(cursor string) (string, error) {
	if cursor == "" {
		return "", nil
	}

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !utf8.Valid(raw) {
		return "", fmt.Errorf("%w: cursor", ErrInvalid)
	}

	return string(raw), nil
}
