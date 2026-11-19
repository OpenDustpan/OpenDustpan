package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TrustRules is trustrule.Store over the database of a Store. Each write
// checks the installation state and the fence of its namespace, and appends
// its audit entry in its own transaction. SQLite runs one write transaction
// at a time, so the count of the limit is exact.
type TrustRules struct {
	s *Store
}

var _ trustrule.Store = TrustRules{s: nil}

// TrustRules returns the store of the workload trust rules.
func (s *Store) TrustRules() TrustRules {
	return TrustRules{s: s}
}

// Create implements trustrule.Store.
func (t TrustRules) Create(ctx context.Context, r trustrule.Rule, fence namespace.Fence, e audit.Entry) error {
	row, err := trustrulestore.CheckCreate(&r, fence)
	if err != nil {
		return err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		if refErr := checkRuleRefs(ctx, q, &row, r.Namespace); refErr != nil {
			return refErr
		}

		n, insErr := q.InsertTrustRule(ctx, db.InsertTrustRuleParams{
			ID: row.ID, Namespace: row.Namespace, Name: row.Name, Issuer: row.Issuer, Audience: row.Audience,
			KeySource: row.KeySource, StaticKeys: row.StaticKeys, ConditionsJson: row.ConditionsJSON,
			PrincipalID: row.PrincipalID, CreatedBy: row.CreatedBy, CreatedAtMs: row.CreatedAtMs,
			Revision: row.Revision, SpiffeIDPattern: row.SpiffeIDPattern, BundleID: row.BundleID,
		})
		if insErr != nil {
			return fmt.Errorf("insert trust rule: %w", insErr)
		}

		if n == 0 {
			return trustrule.ErrExists
		}

		count, countErr := q.CountTrustRulesInNamespace(ctx, row.Namespace)
		if countErr != nil {
			return fmt.Errorf("count the trust rules of the namespace: %w", countErr)
		}

		if count > trustrule.MaxPerNamespace {
			return trustrule.ErrLimit
		}

		return nil
	})
}

// Get implements trustrule.Store. A row that fails its checks is not served.
func (t TrustRules) Get(ctx context.Context, ns namespace.Name, id trustrule.ID) (trustrule.Rule, error) {
	if err := trustrulestore.CheckKey(ns, id); err != nil {
		return trustrule.Rule{}, err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	row, err := t.s.queries.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: ns.String(), ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return trustrule.Rule{}, trustrule.ErrNotFound
	}

	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("select trust rule: %w", err)
	}

	r, err := trustrulestore.Rule(trustRuleRow(row))
	if err != nil {
		return trustrule.Rule{}, trustrule.ErrNotFound
	}

	// Defense in depth: the query matched the namespace. This check stays so
	// that a change of the query cannot serve a rule of another namespace.
	if r.Namespace != ns {
		return trustrule.Rule{}, trustrule.ErrNotFound
	}

	return r, nil
}

// List implements trustrule.Store. A row that fails its checks is left out.
func (t TrustRules) List(ctx context.Context, ns namespace.Name) ([]trustrule.Rule, error) {
	if _, err := namespace.ParseName(ns.String()); err != nil {
		return nil, fmt.Errorf("%w: namespace %q", trustrule.ErrInvalid, ns.String())
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	rows, err := t.s.queries.ListTrustRulesOfNamespace(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select trust rules: %w", err)
	}

	out := make([]trustrule.Rule, 0, len(rows))

	for i := range rows {
		r, ruleErr := trustrulestore.Rule(trustRuleListRow(rows[i]))
		// Defense in depth: the query matched the namespace. This check stays
		// so that a change of the query cannot list a rule of another
		// namespace.
		if ruleErr != nil || r.Namespace != ns {
			continue
		}

		out = append(out, r)
	}

	return out, nil
}

// Delete implements trustrule.Store.
func (t TrustRules) Delete(
	ctx context.Context, ns namespace.Name, id trustrule.ID, fence namespace.Fence, e audit.Entry,
) error {
	if err := trustrulestore.CheckDelete(ns, id, fence); err != nil {
		return err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		n, err := q.DeleteTrustRule(ctx, db.DeleteTrustRuleParams{Namespace: ns.String(), ID: string(id)})
		if err != nil {
			return fmt.Errorf("delete trust rule: %w", err)
		}

		if n == 0 {
			return trustrule.ErrNotFound
		}

		return nil
	})
}

// Update implements trustrule.Store.
func (t TrustRules) Update(
	ctx context.Context, ns namespace.Name, r trustrule.Rule, fence namespace.Fence, e audit.Entry,
) (trustrule.Rule, error) {
	row, err := trustrulestore.CheckUpdate(ns, &r, fence)
	if err != nil {
		return trustrule.Rule{}, err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	var out trustrule.Rule

	err = t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stored, getErr := q.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: ns.String(), ID: string(r.ID)})
		if errors.Is(getErr, sql.ErrNoRows) {
			return trustrule.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select trust rule: %w", getErr)
		}

		if refErr := checkRuleRefs(ctx, q, &row, ns); refErr != nil {
			return refErr
		}

		if writeErr := writeRuleUpdate(ctx, q, &row, stored.Revision); writeErr != nil {
			return writeErr
		}

		// The creator and the creation time are the stored ones.
		row.CreatedBy, row.CreatedAtMs = stored.CreatedBy, stored.CreatedAtMs

		rule, ruleErr := trustrulestore.Rule(row)
		if ruleErr != nil {
			return ruleErr
		}

		out = rule

		return nil
	})
	if err != nil {
		return trustrule.Rule{}, err
	}

	return out, nil
}

// checkRuleRefs checks the principal and the bundle that a rule row names, for
// a write in the namespace ns.
func checkRuleRefs(ctx context.Context, q *db.Queries, row *trustrulestore.Row, ns namespace.Name) error {
	p, err := q.GetTrustRulePrincipal(ctx, row.PrincipalID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("select the principal of the trust rule: %w", err)
	}

	check := trustrulestore.PrincipalRow{
		Kind: p.Kind, Namespace: p.Namespace, DeletedAtMs: p.DeletedAtMs, Found: err == nil,
	}
	if checkErr := trustrulestore.CheckPrincipal(check, ns); checkErr != nil {
		return checkErr
	}

	return checkRuleBundle(ctx, q, row.BundleID, ns)
}

// writeRuleUpdate writes the rule row over the stored revision. A name that
// another rule of the namespace has gives trustrule.ErrExists, and a revision
// that is not one past the stored one gives trustrule.ErrStale.
func writeRuleUpdate(ctx context.Context, q *db.Queries, row *trustrulestore.Row, storedRevision int64) error {
	taken, err := q.CountTrustRulesNamedInNamespace(ctx, db.CountTrustRulesNamedInNamespaceParams{
		Namespace: row.Namespace, Name: row.Name, ExceptID: row.ID,
	})
	if err != nil {
		return fmt.Errorf("count the trust rules with the name: %w", err)
	}

	if taken > 0 {
		return trustrule.ErrExists
	}

	if row.Revision != storedRevision+1 {
		return trustrule.ErrStale
	}

	n, err := q.UpdateTrustRule(ctx, db.UpdateTrustRuleParams{
		Name: row.Name, Issuer: row.Issuer, Audience: row.Audience, KeySource: row.KeySource,
		StaticKeys: row.StaticKeys, ConditionsJson: row.ConditionsJSON, PrincipalID: row.PrincipalID,
		Revision: row.Revision, SpiffeIDPattern: row.SpiffeIDPattern, BundleID: row.BundleID,
		Namespace: row.Namespace, ID: row.ID, StoredRevision: storedRevision,
	})
	if err != nil {
		return fmt.Errorf("update trust rule: %w", err)
	}

	if n == 0 {
		return trustrule.ErrStale
	}

	return nil
}

// checkRuleBundle returns trustrule.ErrBundle when a rule names a bundle that
// is not the bundle of its namespace. An empty bundle ID names no bundle.
func checkRuleBundle(ctx context.Context, q *db.Queries, bundleID string, ns namespace.Name) error {
	if bundleID == "" {
		return nil
	}

	b, err := q.GetTrustBundleOfNamespace(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: the namespace has no trust bundle", trustrule.ErrBundle)
	}

	if err != nil {
		return fmt.Errorf("select the trust bundle of the namespace: %w", err)
	}

	if b.ID != bundleID {
		return fmt.Errorf("%w: the bundle is not the bundle of the namespace", trustrule.ErrBundle)
	}

	return nil
}

// trustRuleRow returns the shared form of a stored row. The selects read the
// nullable bundle ID with COALESCE, so it is never NULL here.
func trustRuleRow(r db.GetTrustRuleRow) trustrulestore.Row {
	return trustrulestore.Row{
		ID: r.ID, Namespace: r.Namespace, Name: r.Name, Issuer: r.Issuer, Audience: r.Audience,
		KeySource: r.KeySource, StaticKeys: r.StaticKeys, ConditionsJSON: r.ConditionsJson,
		PrincipalID: r.PrincipalID, CreatedBy: r.CreatedBy, CreatedAtMs: r.CreatedAtMs,
		Revision: r.Revision, SpiffeIDPattern: r.SpiffeIDPattern, BundleID: r.BundleID,
	}
}

// trustRuleListRow returns the shared form of a stored row of a list.
func trustRuleListRow(r db.ListTrustRulesOfNamespaceRow) trustrulestore.Row {
	return trustrulestore.Row{
		ID: r.ID, Namespace: r.Namespace, Name: r.Name, Issuer: r.Issuer, Audience: r.Audience,
		KeySource: r.KeySource, StaticKeys: r.StaticKeys, ConditionsJSON: r.ConditionsJson,
		PrincipalID: r.PrincipalID, CreatedBy: r.CreatedBy, CreatedAtMs: r.CreatedAtMs,
		Revision: r.Revision, SpiffeIDPattern: r.SpiffeIDPattern, BundleID: r.BundleID,
	}
}

var _ trustrule.ExchangeStore = TrustRules{s: nil}

// RecordExchange implements trustrule.ExchangeStore.
func (t TrustRules) RecordExchange(
	ctx context.Context, x trustrule.Exchange, fence namespace.Fence, e audit.Entry,
) error {
	if err := trustrulestore.CheckExchange(&x); err != nil {
		return err
	}

	t.s.mu.RLock()
	defer t.s.mu.RUnlock()

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, err := q.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: fence.Namespace.String(), ID: string(x.Rule)})
		if errors.Is(err, sql.ErrNoRows) {
			return trustrule.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select the trust rule of the exchange: %w", err)
		}

		if ruleErr := trustrulestore.CheckExchangeRule(trustRuleRow(row), x.Token.PrincipalID); ruleErr != nil {
			return ruleErr
		}

		if recheckErr := recheckExchange(ctx, q, fence.Namespace, &x); recheckErr != nil {
			return recheckErr
		}

		p, err := q.GetExchangePrincipal(ctx, x.Token.PrincipalID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("select the principal of the exchange: %w", err)
		}

		check := trustrulestore.PrincipalRow{
			Kind: p.Kind, Namespace: p.Namespace, DeletedAtMs: p.DeletedAtMs, Found: err == nil,
		}
		if checkErr := trustrulestore.CheckExchangePrincipal(check, p.DisabledAtMs, fence.Namespace); checkErr != nil {
			return checkErr
		}

		return insertToken(ctx, q, x.Token, x.Digest)
	})
}

// recheckExchange reads the rules and the bundle of the namespace again in
// the transaction of an mTLS exchange, and checks that the rule still names
// the SPIFFE ID alone, and that the bundle and the version that the service
// verified are still the bundle of the rule and its active version. A rule or
// a rotate that another call made after the verify so refuses the exchange.
// It does nothing for an exchange that is not of the kind mtls-exchange.
func recheckExchange(ctx context.Context, q *db.Queries, ns namespace.Name, x *trustrule.Exchange) error {
	// Defense in depth: CheckExchange refuses an unknown kind before the
	// transaction. This switch stays so that a new caller of the recheck
	// cannot skip a kind: an unknown kind fails closed.
	needsBundle, kindErr := trustrulestore.ExchangeKindNeedsBundle(x.Token.Kind)
	if kindErr != nil {
		return kindErr
	}

	if !needsBundle {
		return nil
	}

	rows, err := q.ListTrustRulesOfNamespace(ctx, ns.String())
	if err != nil {
		return fmt.Errorf("select the trust rules of the exchange: %w", err)
	}

	list := make([]trustrulestore.Row, 0, len(rows))
	for i := range rows {
		list = append(list, trustRuleListRow(rows[i]))
	}

	if matchErr := trustrulestore.CheckExchangeMatch(list, x); matchErr != nil {
		return matchErr
	}

	b, err := q.GetTrustBundleOfNamespace(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return trustrulestore.CheckExchangeBundle(nil, list, x)
	}

	if err != nil {
		return fmt.Errorf("select the trust bundle of the exchange: %w", err)
	}

	return trustrulestore.CheckExchangeBundle(&trustrulestore.BundleRow{
		ID: b.ID, Namespace: b.Namespace, Active: b.ActiveVersion, Revision: b.Revision, CreatedAtMs: b.CreatedAtMs,
	}, list, x)
}
