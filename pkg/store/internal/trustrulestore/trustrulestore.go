// Package trustrulestore holds the rules of the trust rule table that the
// SQLite and PostgreSQL adapters share: the JSON form of the conditions, the
// checks before a write, and the check of a stored row. A row is untrusted
// when it is read, because a backup, a restore or another version of the
// server can have written it.
package trustrulestore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// ErrCorrupt reports a stored row that does not pass the checks of a rule.
// The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored trust rule is not valid")

// conditionDoc is one condition in the JSON of the conditions column.
type conditionDoc struct {
	Claim  string   `json:"claim"`
	Values []string `json:"values"`
}

// EncodeConditions returns the JSON of cs. The conditions and their values
// keep their order.
func EncodeConditions(cs []trustrule.Condition) (string, error) {
	docs := make([]conditionDoc, 0, len(cs))
	for _, c := range cs {
		docs = append(docs, conditionDoc{Claim: c.Claim, Values: append([]string{}, c.Values...)})
	}

	b, err := json.Marshal(docs)
	if err != nil {
		return "", fmt.Errorf("encode trust rule conditions: %w", err)
	}

	return string(b), nil
}

// decodeConditions reads the JSON that EncodeConditions wrote. It refuses a
// field that it does not know and text after the document.
func decodeConditions(s string) ([]trustrule.Condition, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.DisallowUnknownFields()

	var docs []conditionDoc
	if err := dec.Decode(&docs); err != nil {
		return nil, fmt.Errorf("%w: conditions: %w", ErrCorrupt, err)
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: conditions: text after the document", ErrCorrupt)
	}

	out := make([]trustrule.Condition, 0, len(docs))
	for _, d := range docs {
		out = append(out, trustrule.Condition{Claim: d.Claim, Values: d.Values})
	}

	return out, nil
}

// Row is a trust rule row as the database holds it. Namespace is the name of
// the namespace of the rule.
type Row struct {
	ID             string
	Namespace      string
	Name           string
	Issuer         string
	Audience       string
	KeySource      string
	StaticKeys     string
	ConditionsJSON string
	PrincipalID    string
	CreatedBy      string
	CreatedAtMs    int64
}

// RowOf returns the row of r. It fails when the conditions do not encode.
func RowOf(r *trustrule.Rule) (Row, error) {
	conds, err := EncodeConditions(r.Conditions)
	if err != nil {
		return Row{}, err
	}

	return Row{
		ID: string(r.ID), Namespace: r.Namespace.String(), Name: r.Name, Issuer: r.Issuer, Audience: r.Audience,
		KeySource: string(r.Keys), StaticKeys: r.StaticKeys, ConditionsJSON: conds, PrincipalID: r.PrincipalID,
		CreatedBy: r.CreatedBy, CreatedAtMs: r.CreatedAt.UnixMilli(),
	}, nil
}

// Rule returns the rule of a stored row, or ErrCorrupt when the row does not
// pass the checks of a rule.
func Rule(row Row) (trustrule.Rule, error) {
	conds, err := decodeConditions(row.ConditionsJSON)
	if err != nil {
		return trustrule.Rule{}, err
	}

	r := trustrule.Rule{
		CreatedAt: time.UnixMilli(row.CreatedAtMs).UTC(), ID: trustrule.ID(row.ID),
		Namespace: namespace.Name(row.Namespace), Name: row.Name, Issuer: row.Issuer, Audience: row.Audience,
		Keys: trustrule.KeySource(row.KeySource), StaticKeys: row.StaticKeys, PrincipalID: row.PrincipalID,
		CreatedBy: row.CreatedBy, Conditions: conds,
	}

	if err := r.Validate(); err != nil {
		return trustrule.Rule{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return r, nil
}

// CheckCreate checks a create before the transaction: the rule, and the
// namespace of the fence against the namespace of the rule. It returns the
// row to write.
func CheckCreate(r *trustrule.Rule, fence namespace.Fence) (Row, error) {
	if err := r.Validate(); err != nil {
		return Row{}, fmt.Errorf("create trust rule: %w", err)
	}

	// Defense in depth: the fence check of the transaction compares the
	// namespace of the fence with the namespace row. This check stays so that
	// a rule of one namespace cannot be written under the fence of another
	// namespace, whatever the audit entry says. It does not rely on the
	// service.
	if r.Namespace != fence.Namespace {
		return Row{}, fmt.Errorf("%w: the rule names %q, the fence names %q",
			namespace.ErrFenced, r.Namespace, fence.Namespace)
	}

	return RowOf(r)
}

// CheckKey returns ErrInvalid for a namespace or an ID that fails validation.
func CheckKey(ns namespace.Name, id trustrule.ID) error {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return fmt.Errorf("%w: namespace %q", trustrule.ErrInvalid, ns.String())
	}

	return id.Validate()
}

// CheckDelete checks a delete before the transaction.
func CheckDelete(ns namespace.Name, id trustrule.ID, fence namespace.Fence) error {
	if err := CheckKey(ns, id); err != nil {
		return err
	}

	// Defense in depth: as in CheckCreate, the namespace of the delete must
	// be the namespace of the fence.
	if ns != fence.Namespace {
		return fmt.Errorf("%w: the delete names %q, the fence names %q", namespace.ErrFenced, ns, fence.Namespace)
	}

	return nil
}

// PrincipalRow is the principal of a rule as the transaction of a create
// reads it. Found is false when no row has the ID. DeletedAtMs is zero for a
// principal that is not deleted.
type PrincipalRow struct {
	Kind        string
	Namespace   string
	DeletedAtMs int64
	Found       bool
}

// CheckPrincipal returns trustrule.ErrPrincipal when p cannot be the
// principal of a rule of the namespace ns: it does not exist, it is
// deleted, it is a person, or it is not a principal of ns.
func CheckPrincipal(p PrincipalRow, ns namespace.Name) error {
	switch {
	case !p.Found:
		return fmt.Errorf("%w: no such principal", trustrule.ErrPrincipal)
	case p.DeletedAtMs != 0:
		return fmt.Errorf("%w: the principal is deleted", trustrule.ErrPrincipal)
	case principal.Kind(p.Kind) == principal.KindPerson:
		return fmt.Errorf("%w: the principal is a person", trustrule.ErrPrincipal)
	case p.Namespace == "" || p.Namespace != ns.String():
		return fmt.Errorf("%w: the principal is not a principal of the namespace %q", trustrule.ErrPrincipal, ns)
	default:
		return nil
	}
}
