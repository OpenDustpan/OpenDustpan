package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxFilterJSON bounds the encoded text of a filter, in bytes. A store
// refuses a longer text when it writes and when it reads.
const MaxFilterJSON = 1 << 20

// Filters keeps the source filter of each namespace. Store embeds it. It is
// a port of its own, so that the filter can be served apart from the
// snapshots.
type Filters interface {
	// Filter returns the source filter of the namespace ns. A namespace that
	// never set it gives the empty filter: no submodule, no Git LFS object,
	// no protected branch, and FailOnDenied false. It returns
	// namespace.ErrNotFound for an unknown namespace, and ErrInvalid for a
	// namespace name that namespace.ParseName refuses and for a stored value
	// that DecodeFilter refuses, so the caller fails closed. The error does
	// not hold the stored value.
	Filter(ctx context.Context, ns namespace.Name) (Filter, error)
	// SetFilter stores f as the source filter of the namespace ns and appends
	// the audit entry e in the transaction. When e holds one change, the
	// store sets the Old value of that change to the value that the write
	// replaces, read in the same transaction: the text that EncodeFilter
	// gives for it. For a stored value that DecodeFilter refuses, the Old
	// value holds the raw stored value, quoted and marked invalid. It
	// returns ErrInvalid when f fails Filter.Validate or when ns fails
	// namespace.ParseName. A valid f replaces a stored value that
	// DecodeFilter refuses. It returns namespace.ErrFenced when the fence is
	// stale or names another namespace than ns, and namespace.ErrNotFound for
	// an unknown namespace. On every error the store changes nothing. A new
	// filter applies to the fetches that start later. It changes no snapshot
	// that exists.
	SetFilter(ctx context.Context, ns namespace.Name, f Filter, fence namespace.Fence, e audit.Entry) error
}

// filterDocument is the stored and shown form of a Filter. A list is never
// null, so one filter has one text.
type filterDocument struct {
	Submodules        []URLPattern `json:"submodules"`
	LargeFiles        []URLPattern `json:"largeFiles"`
	FailOnDenied      bool         `json:"failOnDenied"`
	ProtectedBranches []string     `json:"protectedBranches"`
}

// EncodeFilter returns the text of f: a JSON object with the keys
// "submodules", "largeFiles", "failOnDenied" and "protectedBranches", where
// a list that is empty is []. One filter always gives one text. It returns
// ErrInvalid when f fails Filter.Validate, and ErrFilterTooLarge when the text has
// more than MaxFilterJSON bytes.
func EncodeFilter(f Filter) (string, error) {
	if err := f.Validate(); err != nil {
		return "", err
	}

	doc := filterDocument{
		Submodules:        orEmpty(f.Submodules),
		LargeFiles:        orEmpty(f.LargeFiles),
		FailOnDenied:      f.FailOnDenied,
		ProtectedBranches: orEmpty(f.ProtectedBranches),
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("%w: encode the filter: %w", ErrInvalid, err)
	}

	if len(raw) > MaxFilterJSON {
		return "", fmt.Errorf("%w: the filter has at most %d bytes", ErrFilterTooLarge, MaxFilterJSON)
	}

	return string(raw), nil
}

// DecodeFilter returns the filter that text holds. The empty text is the
// empty filter. Any other text must be one JSON object with the keys of
// EncodeFilter only, at most MaxFilterJSON bytes, and no data after the
// object, and its value must pass Filter.Validate. It returns ErrInvalid
// for a text that is not so, and ErrFilterTooLarge above MaxFilterJSON bytes. The
// error does not hold the text.
func DecodeFilter(text string) (Filter, error) {
	var none Filter

	if text == "" {
		return none, nil
	}

	if len(text) > MaxFilterJSON {
		return none, fmt.Errorf("%w: the filter has at most %d bytes", ErrFilterTooLarge, MaxFilterJSON)
	}

	// A JSON null decodes to an empty document, so the first byte must open an
	// object.
	if text[0] != '{' {
		return none, fmt.Errorf("%w: the filter is not a filter document", ErrInvalid)
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(text)))
	dec.DisallowUnknownFields()

	var doc filterDocument
	if err := dec.Decode(&doc); err != nil {
		return none, fmt.Errorf("%w: the filter is not a filter document", ErrInvalid)
	}

	// Nothing may follow the document, also not a second document.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return none, fmt.Errorf("%w: data follows the filter document", ErrInvalid)
	}

	f := Filter(doc)

	// The error of Validate quotes a value, so it is not wrapped.
	if f.Validate() != nil {
		return none, fmt.Errorf("%w: the filter document fails the checks of a filter", ErrInvalid)
	}

	return f, nil
}

// orEmpty returns list, or an empty list for nil.
func orEmpty[T any](list []T) []T {
	if list == nil {
		return []T{}
	}

	return list
}
