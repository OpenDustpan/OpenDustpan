package inventory

import (
	"cmp"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/namespace"
)

const (
	// defaultUnitPageSize and maxUnitPageSize are the default and the largest
	// page size of the unit list.
	defaultUnitPageSize = 100
	maxUnitPageSize     = 500
)

var (
	errPageSize = fmt.Errorf(
		"%w: pageSize must be a number from 1 to %d", errInvalidQuery, maxUnitPageSize,
	)
	errPageToken = fmt.Errorf(
		"%w: pageToken does not belong to this query: start again without it", errInvalidQuery,
	)
	errPageTokenAt = fmt.Errorf(
		"%w: the pageToken query value does not work with the at query value", errInvalidQuery,
	)
)

// unitPage is the page that a unit list asks for: its size and the unit ID
// after which it starts. An empty After is the first page.
type unitPage struct {
	size  int
	after string
}

// pageTokenWire is the opaque page token of the unit list, as base64url JSON.
// It holds the ID of the last unit of the page and a fingerprint of the query,
// so a token from another query answers 400.
type pageTokenWire struct {
	After       string `json:"u"`
	Fingerprint string `json:"f"`
}

// parseUnitPage reads the pageSize and pageToken query values of a unit list
// in the namespace ns with the filter f.
func parseUnitPage(r *http.Request, ns namespace.Name, f unitFilter) (unitPage, error) {
	query := r.URL.Query()
	page := unitPage{size: defaultUnitPageSize, after: ""}

	if raw := query.Get("pageSize"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxUnitPageSize {
			return unitPage{}, errPageSize
		}

		page.size = n
	}

	if raw := query.Get("pageToken"); raw != "" {
		after, err := decodePageToken(raw, queryFingerprint(ns, f))
		if err != nil {
			return unitPage{}, err
		}

		page.after = after
	}

	return page, nil
}

// queryFingerprint returns a short digest of the namespace and the filters of
// a unit list, so a page token works only for the query that made it.
func queryFingerprint(ns namespace.Name, f unitFilter) string {
	parts := make([]string, 0, 1+len(f.labels)+len(f.references))
	parts = append(parts, "ns="+ns.String())

	for _, l := range f.labels {
		parts = append(parts, "label="+l.key+"="+l.value)
	}

	for ref := range f.references {
		parts = append(parts, "reference="+ref)
	}

	// The order of the query values does not change the query.
	slices.Sort(parts)

	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))

	return hex.EncodeToString(sum[:8])
}

// encodePageToken returns the opaque token for the page that starts after the
// unit after, in the query with the fingerprint fingerprint.
func encodePageToken(after, fingerprint string) string {
	raw, err := json.Marshal(pageTokenWire{After: after, Fingerprint: fingerprint})
	if err != nil {
		return ""
	}

	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodePageToken returns the unit ID after which the page of the token
// starts. A token that does not decode, or that another query made, gives
// errPageToken.
func decodePageToken(token, fingerprint string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return "", errPageToken
	}

	var w pageTokenWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return "", errPageToken
	}

	if w.After == "" || w.Fingerprint != fingerprint {
		return "", errPageToken
	}

	return w.After, nil
}

// sortedByUnitID returns a copy of units in the stable order of the unit list:
// by unit ID.
func sortedByUnitID[T any](units []T, id func(T) string) []T {
	out := slices.Clone(units)
	slices.SortFunc(out, func(a, b T) int { return cmp.Compare(id(a), id(b)) })

	return out
}
