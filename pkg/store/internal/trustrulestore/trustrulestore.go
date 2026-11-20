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
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
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
	ID              string
	Namespace       string
	Name            string
	Issuer          string
	Audience        string
	KeySource       string
	StaticKeys      string
	ConditionsJSON  string
	PrincipalID     string
	CreatedBy       string
	CreatedAtMs     int64
	Revision        int64
	SpiffeIDPattern string
	BundleID        string
}

// RowOf returns the row of r. It fails when the conditions do not encode. A
// rule with revision 0 is stored with revision 1.
func RowOf(r *trustrule.Rule) (Row, error) {
	conds, err := EncodeConditions(r.Conditions)
	if err != nil {
		return Row{}, err
	}

	revision := max(r.Revision, 1)

	return Row{
		ID: string(r.ID), Namespace: r.Namespace.String(), Name: r.Name, Issuer: r.Issuer, Audience: r.Audience,
		KeySource: string(r.Keys), StaticKeys: r.StaticKeys, ConditionsJSON: conds, PrincipalID: r.PrincipalID,
		CreatedBy: r.CreatedBy, CreatedAtMs: r.CreatedAt.UnixMilli(), Revision: revision,
		SpiffeIDPattern: r.SpiffeIDPattern, BundleID: r.Bundle,
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
		CreatedBy: row.CreatedBy, Conditions: conds, Revision: row.Revision,
		SpiffeIDPattern: row.SpiffeIDPattern, Bundle: row.BundleID,
	}

	if err := r.Validate(); err != nil {
		return trustrule.Rule{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	if row.Revision < 1 {
		return trustrule.Rule{}, fmt.Errorf("%w: revision %d", ErrCorrupt, row.Revision)
	}

	return r, nil
}

// CheckCreate checks a create before the transaction: the rule, and the
// namespace of the fence against the namespace of the rule. It returns the
// row to write.
func CheckCreate(r *trustrule.Rule, fence namespace.Fence) (Row, error) {
	// Defense in depth: the service checks the rule too, among them the * in a
	// SPIFFE ID. This check stays so that a restore or a new caller cannot
	// store a rule that names more than one SPIFFE ID.
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

// CheckUpdate checks an update before the transaction: the key, the rule, and
// the namespace of the fence against the namespace of the update. It returns
// the row to write. The row carries the revision of r.
func CheckUpdate(ns namespace.Name, r *trustrule.Rule, fence namespace.Fence) (Row, error) {
	if err := CheckKey(ns, r.ID); err != nil {
		return Row{}, err
	}

	// Defense in depth: the service checks the rule too, among them the * in a
	// SPIFFE ID. This check stays so that a new caller cannot store a rule
	// that names more than one SPIFFE ID.
	if err := r.Validate(); err != nil {
		return Row{}, fmt.Errorf("update trust rule: %w", err)
	}

	// Defense in depth: as in CheckCreate, the namespace of the update must
	// be the namespace of the fence and the namespace of the rule. The query
	// of the store also keys the write on the namespace.
	if ns != fence.Namespace || r.Namespace != ns {
		return Row{}, fmt.Errorf("%w: the update names %q and %q, the fence names %q",
			namespace.ErrFenced, ns, r.Namespace, fence.Namespace)
	}

	return RowOf(r)
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
// deleted, it is not a service principal, or it is not a principal of ns.
func CheckPrincipal(p PrincipalRow, ns namespace.Name) error {
	switch {
	case !p.Found:
		return fmt.Errorf("%w: no such principal", trustrule.ErrPrincipal)
	case p.DeletedAtMs != 0:
		return fmt.Errorf("%w: the principal is deleted", trustrule.ErrPrincipal)
	case principal.Kind(p.Kind) != principal.KindService:
		return fmt.Errorf("%w: the principal is not a service principal", trustrule.ErrPrincipal)
	case p.Namespace == "" || p.Namespace != ns.String():
		return fmt.Errorf("%w: the principal is not a principal of the namespace %q", trustrule.ErrPrincipal, ns)
	default:
		return nil
	}
}

// CheckExchange checks an exchange before the transaction: the rule ID, the
// kind of the token, its principal and its times.
func CheckExchange(x *trustrule.Exchange) error {
	if err := x.Rule.Validate(); err != nil {
		return err
	}

	// Defense in depth: the service makes only tokens of the kinds jwt-exchange
	// and mtls-exchange for an exchange. This check stays so that no caller
	// of the store makes a personal or a service token through the path of
	// an exchange.
	if x.Token.Kind != accesstoken.KindJWTExchange && x.Token.Kind != accesstoken.KindMTLSExchange {
		return fmt.Errorf("%w: an exchange stores only a token of the kind %q or %q, not %q", trustrule.ErrInvalid,
			accesstoken.KindJWTExchange, accesstoken.KindMTLSExchange, x.Token.Kind)
	}

	if x.Token.PrincipalID == "" || !x.Token.ExpiresAt.After(x.Token.CreatedAt) {
		return fmt.Errorf("%w: the token of the exchange has no principal or no lifetime", trustrule.ErrInvalid)
	}

	// Defense in depth: the mTLS service sets the SPIFFE ID from a verified
	// chain, and the JWT service never sets it. This check stays so that the
	// store never stores an mTLS token without its match in the transaction.
	mtls := x.Token.Kind == accesstoken.KindMTLSExchange
	if mtls != (x.SPIFFEID != "") || strings.Contains(x.SPIFFEID, "*") {
		return fmt.Errorf("%w: a token of the kind %q needs a SPIFFE ID with no *, other kinds need none",
			trustrule.ErrInvalid, accesstoken.KindMTLSExchange)
	}

	return checkExchangeBundleFields(x)
}

// checkExchangeBundleFields checks the bundle ID and the bundle version of an
// exchange of the kind mtls-exchange, or the lack of both for another kind.
func checkExchangeBundleFields(x *trustrule.Exchange) error {
	mtls := x.Token.Kind == accesstoken.KindMTLSExchange

	// Defense in depth: the mTLS service sets the bundle and its version from
	// the verify of the chain. This check stays so that the store never
	// stores an mTLS token with no bundle version to check in the
	// transaction, and never a JWT token with one.
	if mtls {
		if x.BundleID.Validate() != nil || x.BundleVersion < 1 {
			return fmt.Errorf("%w: a token of the kind %q needs a bundle ID and a bundle version of 1 or more",
				trustrule.ErrInvalid, accesstoken.KindMTLSExchange)
		}
	} else if x.BundleID != "" || x.BundleVersion != 0 {
		return fmt.Errorf("%w: a token of the kind %q has no bundle", trustrule.ErrInvalid, x.Token.Kind)
	}

	return nil
}

// CheckExchangeBundle returns trustrule.ErrNotFound unless the bundle of the
// namespace of the fence exists, is the bundle x.BundleID, is the bundle of the
// rule of the exchange (the row of x.Rule in rows), and has x.BundleVersion as its active
// version. The adapters call it in the transaction of the exchange with the
// stored bundle row of that namespace, so a rotate that came after the verify
// of the chain still refuses the exchange. An exchange of the kind
// jwt-exchange has no bundle, so it passes with no check. Any other kind
// returns trustrule.ErrInvalid.
func CheckExchangeBundle(row *BundleRow, rows []Row, x *trustrule.Exchange) error {
	switch x.Token.Kind {
	case accesstoken.KindMTLSExchange:
		return checkMtlsExchangeBundle(row, rows, x)
	case accesstoken.KindJWTExchange:
		return nil
	case accesstoken.KindPersonal, accesstoken.KindService:
		// No exchange makes these kinds.
		return unknownExchangeKind(x.Token.Kind)
	}

	// Fail closed on a kind that no code knows.
	return unknownExchangeKind(x.Token.Kind)
}

// unknownExchangeKind is the refusal of a token kind that no exchange makes.
func unknownExchangeKind(kind accesstoken.Kind) error {
	return fmt.Errorf("%w: an exchange has no token of the kind %q", trustrule.ErrInvalid, kind)
}

// checkMtlsExchangeBundle is the bundle check of CheckExchangeBundle for the
// kind mtls-exchange.
func checkMtlsExchangeBundle(row *BundleRow, rows []Row, x *trustrule.Exchange) error {
	ruleBundleID := ""

	for i := range rows {
		if rows[i].ID == string(x.Rule) {
			ruleBundleID = rows[i].BundleID
		}
	}

	switch {
	case row == nil:
		return fmt.Errorf("%w: the namespace has no trust bundle", trustrule.ErrNotFound)
	case row.ID != string(x.BundleID) || ruleBundleID != string(x.BundleID):
		return fmt.Errorf("%w: the bundle of the exchange is not the bundle of its rule and its namespace",
			trustrule.ErrNotFound)
	case row.Active != x.BundleVersion:
		return fmt.Errorf("%w: version %d of the bundle is not the active version", trustrule.ErrNotFound,
			x.BundleVersion)
	default:
		return nil
	}
}

// CheckExchangeMatch returns trustrule.ErrNotFound unless exactly one row of
// rows has the SPIFFE ID pattern x.SPIFFEID and the bundle of the row of
// x.Rule, and that row is the row of x.Rule. The adapters call it in the
// transaction of the exchange with the rule rows of the namespace of the
// fence, so a rule that another call added after the service read its rules
// still refuses the exchange. It returns nil for an exchange with no SPIFFE
// ID.
func CheckExchangeMatch(rows []Row, x *trustrule.Exchange) error {
	if x.SPIFFEID == "" {
		return nil
	}

	bundle := ""

	for i := range rows {
		if rows[i].ID == string(x.Rule) {
			bundle = rows[i].BundleID
		}
	}

	if bundle == "" {
		return fmt.Errorf("%w: the rule of the exchange has no trust bundle", trustrule.ErrNotFound)
	}

	matched := make([]string, 0, 1)

	for i := range rows {
		if rows[i].BundleID == bundle && rows[i].SpiffeIDPattern == x.SPIFFEID {
			matched = append(matched, rows[i].ID)
		}
	}

	if len(matched) != 1 || matched[0] != string(x.Rule) {
		return fmt.Errorf("%w: %d rules name the SPIFFE ID of the exchange, want only its rule",
			trustrule.ErrNotFound, len(matched))
	}

	return nil
}

// CheckExchangeRule returns trustrule.ErrNotFound when the stored rule row of
// an exchange does not name the principal of the token, or fails the checks
// of a rule.
func CheckExchangeRule(row Row, principalID string) error {
	r, err := Rule(row)
	if err != nil || r.PrincipalID != principalID {
		return fmt.Errorf("%w: the rule does not name the principal of the token", trustrule.ErrNotFound)
	}

	return nil
}

// CheckExchangePrincipal is CheckPrincipal for an exchange: a disabled
// principal fails too. disabledAtMs is zero for a principal that is not
// disabled.
func CheckExchangePrincipal(p PrincipalRow, disabledAtMs int64, ns namespace.Name) error {
	if err := CheckPrincipal(p, ns); err != nil {
		return err
	}

	if disabledAtMs != 0 {
		return fmt.Errorf("%w: the principal is disabled", trustrule.ErrPrincipal)
	}

	return nil
}

// ExchangeKindNeedsBundle tells an adapter what to do with the kind of an
// exchange before it reads any row: true for mtls-exchange (run the match and
// the bundle check), false for jwt-exchange (no check), and
// trustrule.ErrInvalid for any other kind.
func ExchangeKindNeedsBundle(kind accesstoken.Kind) (bool, error) {
	switch kind {
	case accesstoken.KindMTLSExchange:
		return true, nil
	case accesstoken.KindJWTExchange:
		return false, nil
	case accesstoken.KindPersonal, accesstoken.KindService:
		// No exchange makes these kinds.
		return false, unknownExchangeKind(kind)
	}

	// Fail closed on a kind that no code knows.
	return false, unknownExchangeKind(kind)
}
