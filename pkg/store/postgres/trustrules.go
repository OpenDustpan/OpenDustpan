package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TrustRules is trustrule.Store over the database of a Store. Each write
// checks the installation state and the fence of its namespace, and appends
// its audit entry in its own transaction. The transaction holds the namespace
// row FOR UPDATE, so the count of the limit is exact.
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

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		p, pErr := q.GetTrustRulePrincipal(ctx, r.PrincipalID)
		if pErr != nil && !errors.Is(pErr, pgx.ErrNoRows) {
			return fmt.Errorf("select the principal of the trust rule: %w", pErr)
		}

		check := trustrulestore.PrincipalRow{
			Kind: p.Kind, Namespace: p.Namespace, DeletedAtMs: p.DeletedAtMs, Found: pErr == nil,
		}
		if checkErr := trustrulestore.CheckPrincipal(check, r.Namespace); checkErr != nil {
			return checkErr
		}

		if bundleErr := checkRuleBundle(ctx, q, row.BundleID, r.Namespace); bundleErr != nil {
			return bundleErr
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

	row, err := t.s.queries.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: ns.String(), ID: string(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return trustrule.Rule{}, trustrule.ErrNotFound
	}

	if err != nil {
		return trustrule.Rule{}, fmt.Errorf("select trust rule: %w", err)
	}

	r, err := trustrulestore.Rule(trustRuleRow(&row))
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

	rows, err := t.s.queries.ListTrustRulesOfNamespace(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select trust rules: %w", err)
	}

	out := make([]trustrule.Rule, 0, len(rows))

	for i := range rows {
		r, ruleErr := trustrulestore.Rule(trustRuleRow(&rows[i]))
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

	var out trustrule.Rule

	err = t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stored, getErr := q.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: ns.String(), ID: string(r.ID)})
		if errors.Is(getErr, pgx.ErrNoRows) {
			return trustrule.ErrNotFound
		}

		if getErr != nil {
			return fmt.Errorf("select trust rule: %w", getErr)
		}

		p, pErr := q.GetTrustRulePrincipal(ctx, r.PrincipalID)
		if pErr != nil && !errors.Is(pErr, pgx.ErrNoRows) {
			return fmt.Errorf("select the principal of the trust rule: %w", pErr)
		}

		check := trustrulestore.PrincipalRow{
			Kind: p.Kind, Namespace: p.Namespace, DeletedAtMs: p.DeletedAtMs, Found: pErr == nil,
		}
		if checkErr := trustrulestore.CheckPrincipal(check, ns); checkErr != nil {
			return checkErr
		}

		if bundleErr := checkRuleBundle(ctx, q, row.BundleID, ns); bundleErr != nil {
			return bundleErr
		}

		taken, countErr := q.CountTrustRulesNamedInNamespace(ctx, db.CountTrustRulesNamedInNamespaceParams{
			Namespace: ns.String(), Name: row.Name, ExceptID: row.ID,
		})
		if countErr != nil {
			return fmt.Errorf("count the trust rules with the name: %w", countErr)
		}

		if taken > 0 {
			return trustrule.ErrExists
		}

		if row.Revision != stored.Revision+1 {
			return trustrule.ErrStale
		}

		n, updErr := q.UpdateTrustRule(ctx, db.UpdateTrustRuleParams{
			Name: row.Name, Issuer: row.Issuer, Audience: row.Audience, KeySource: row.KeySource,
			StaticKeys: row.StaticKeys, ConditionsJson: row.ConditionsJSON, PrincipalID: row.PrincipalID,
			Revision: row.Revision, SpiffeIDPattern: row.SpiffeIDPattern, BundleID: row.BundleID,
			Namespace: row.Namespace, ID: row.ID, StoredRevision: stored.Revision,
		})
		if updErr != nil {
			return fmt.Errorf("update trust rule: %w", updErr)
		}

		if n == 0 {
			return trustrule.ErrStale
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

// checkRuleBundle returns trustrule.ErrBundle when a rule names a bundle that
// is not the bundle of its namespace. An empty bundle ID names no bundle.
func checkRuleBundle(ctx context.Context, q *db.Queries, bundleID string, ns namespace.Name) error {
	if bundleID == "" {
		return nil
	}

	b, err := q.GetTrustBundleOfNamespace(ctx, ns.String())
	if errors.Is(err, pgx.ErrNoRows) {
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

// trustRuleRow returns the shared form of a stored row.
func trustRuleRow(r *db.TrustRule) trustrulestore.Row {
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

	return t.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, err := q.GetTrustRule(ctx, db.GetTrustRuleParams{Namespace: fence.Namespace.String(), ID: string(x.Rule)})
		if errors.Is(err, pgx.ErrNoRows) {
			return trustrule.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select the trust rule of the exchange: %w", err)
		}

		if ruleErr := trustrulestore.CheckExchangeRule(trustRuleRow(&row), x.Token.PrincipalID); ruleErr != nil {
			return ruleErr
		}

		p, err := q.GetExchangePrincipal(ctx, x.Token.PrincipalID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
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
