// Package viewstore holds the rules of the saved view tables that the SQLite
// and PostgreSQL adapters share: the JSON form of a query, and the check of a
// stored row. A row is untrusted when it is read, because a backup, a restore
// or another version of the server can have written it.
package viewstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/savedview"
)

// ErrCorrupt reports a stored row that does not pass the checks of a view. The
// adapters do not serve such a row.
var ErrCorrupt = errors.New("stored saved view is not valid")

type labelDoc struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type queryDoc struct {
	Labels     []labelDoc `json:"labels"`
	References []string   `json:"references"`
}

// EncodeQuery returns the JSON of q. The lists keep their order.
func EncodeQuery(q savedview.Query) (string, error) {
	doc := queryDoc{Labels: make([]labelDoc, 0, len(q.Labels)), References: q.References}
	if doc.References == nil {
		doc.References = []string{}
	}

	for _, l := range q.Labels {
		doc.Labels = append(doc.Labels, labelDoc(l))
	}

	b, err := json.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("encode saved view query: %w", err)
	}

	return string(b), nil
}

// decodeQuery reads the JSON that EncodeQuery wrote. It refuses a field that it
// does not know and text after the document.
func decodeQuery(s string) (savedview.Query, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	var doc queryDoc
	if err := dec.Decode(&doc); err != nil {
		return savedview.Query{}, fmt.Errorf("%w: query: %w", ErrCorrupt, err)
	}

	// A second decode must reach the end of the text. dec.More does not say
	// this: it is false before a stray closing brace.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return savedview.Query{}, fmt.Errorf("%w: query: text after the document", ErrCorrupt)
	}

	q := savedview.Query{Labels: nil, References: nil}

	for _, l := range doc.Labels {
		q.Labels = append(q.Labels, savedview.Label(l))
	}

	q.References = append(q.References, doc.References...)

	return q, nil
}

// Row is a saved view row as the database holds it. Namespace is the name of
// the namespace.
type Row struct {
	ID          string
	Namespace   string
	Owner       string
	Name        string
	QueryJSON   string
	CreatedAtMs int64
}

// View builds the view of a stored row and checks it again with
// savedview.View.Validate. It returns ErrCorrupt for a row that fails.
func View(r Row) (savedview.View, error) {
	q, err := decodeQuery(r.QueryJSON)
	if err != nil {
		return savedview.View{}, err
	}

	v := savedview.View{
		CreatedAt: time.UnixMilli(r.CreatedAtMs).UTC(),
		ID:        savedview.ID(r.ID),
		Namespace: namespace.Name(r.Namespace),
		Name:      r.Name,
		Owner:     r.Owner,
		Query:     q,
	}

	if err := v.Validate(); err != nil {
		return savedview.View{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return v, nil
}

// ErrOverLimit reports stored rows in which an owner or a namespace has more
// saved views than savedview.MaxPerOwner or savedview.MaxPerNamespace.
var ErrOverLimit = errors.New("saved views over the limit")

// CheckCounts checks the counts of the view v of a stored row: ofOwner is the
// number of views of the owner in the namespace, and inNamespace is the
// number of views in the namespace. It returns ErrOverLimit for a count over a
// limit. The error names the owner or the namespace, the count and the limit.
// The caller builds v with View, so that the names in the error are valid.
//
// Defense in depth: the create of a view refuses a view over a limit. This
// check stays so that a backup that a person edited cannot bring in more
// views than the create allows.
func CheckCounts(v savedview.View, ofOwner, inNamespace int64) error {
	if ofOwner > savedview.MaxPerOwner {
		return fmt.Errorf("owner %q has %d saved views in namespace %q, the limit is %d: %w",
			v.Owner, ofOwner, v.Namespace, savedview.MaxPerOwner, ErrOverLimit)
	}

	if inNamespace > savedview.MaxPerNamespace {
		return fmt.Errorf("namespace %q has %d saved views, the limit is %d: %w",
			v.Namespace, inNamespace, savedview.MaxPerNamespace, ErrOverLimit)
	}

	return nil
}
