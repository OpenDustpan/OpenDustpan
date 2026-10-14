package accesstoken

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
)

// Page limits of the list operation.
const (
	// DefaultLimit is the page size when a request names none.
	DefaultLimit = 100
	// MaxLimit is the largest page size a request can name.
	MaxLimit = 1000
	// MaxCursorLength bounds a cursor, in bytes.
	MaxCursorLength = 128
)

// PageRequest asks for one page of the tokens, newest first. PrincipalID
// keeps the tokens of one principal, or of every principal when it is
// empty. IncludeEnded also keeps the revoked and the expired tokens. Cursor
// is the opaque NextCursor of the page before, or empty for the first page.
// Limit is 1 to MaxLimit. 0 means that the request names no limit, and
// reads DefaultLimit rows.
type PageRequest struct {
	PrincipalID  string
	Cursor       string
	Limit        int
	IncludeEnded bool
}

// Page is one page of tokens. Total is the count of every token that
// matches the filters, on every page. NextCursor is empty on the last page.
type Page struct {
	Items      []Token
	NextCursor string
	Total      int64
}

// query validates req and returns the Query for a store at the time now.
// It asks for one row more than the page holds, so the Service can tell
// whether a next page exists.
func (req PageRequest) query(now time.Time) (Query, int, error) {
	limit := req.Limit

	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 1 || limit > MaxLimit:
		return Query{}, 0, fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalid, req.Limit, MaxLimit)
	default:
	}

	if req.PrincipalID != "" && !ident.Valid(req.PrincipalID, maxIDLength) {
		return Query{}, 0, fmt.Errorf("%w: principal ID", ErrInvalid)
	}

	q := Query{
		ActiveAt:       time.Time{},
		AfterCreatedAt: time.Time{},
		PrincipalID:    req.PrincipalID,
		AfterID:        "",
		Limit:          limit + 1,
		HasAfter:       false,
	}

	if !req.IncludeEnded {
		q.ActiveAt = now
	}

	if req.Cursor != "" {
		at, id, err := decodeCursor(req.Cursor)
		if err != nil {
			return Query{}, 0, err
		}

		q.AfterCreatedAt, q.AfterID, q.HasAfter = at, id, true
	}

	return q, limit, nil
}

// pageOf trims rows to limit and sets the cursor of the next page from the
// last row it keeps.
func pageOf(rows []Token, total int64, limit int) Page {
	if len(rows) <= limit {
		return Page{Items: rows, NextCursor: "", Total: total}
	}

	rows = rows[:limit]
	last := rows[len(rows)-1]

	return Page{Items: rows, NextCursor: encodeCursor(last.CreatedAt, last.ID), Total: total}
}

// encodeCursor makes the opaque cursor of a row: its creation time in Unix
// milliseconds and its ID.
func encodeCursor(at time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at.UnixMilli(), 10) + "." + id))
}

// decodeCursor reads a cursor of encodeCursor.
func decodeCursor(cursor string) (time.Time, string, error) {
	bad := fmt.Errorf("%w: cursor", ErrInvalid)

	if len(cursor) > MaxCursorLength {
		return time.Time{}, "", fmt.Errorf("%w: cursor longer than %d bytes", ErrInvalid, MaxCursorLength)
	}

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", bad
	}

	msText, id, ok := strings.Cut(string(raw), ".")
	if !ok || !strings.HasPrefix(id, idPrefix) || !ident.Valid(id, maxIDLength) {
		return time.Time{}, "", bad
	}

	ms, err := strconv.ParseInt(msText, 10, 64)
	if err != nil || ms < 0 {
		return time.Time{}, "", bad
	}

	return time.UnixMilli(ms).UTC(), id, nil
}
