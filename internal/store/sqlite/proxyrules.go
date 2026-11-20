package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/store/sqlite/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/jobproxy"
	"opendustpan.org/opendustpan/pkg/jobproxy/rulestore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Values of the scope_kind column of proxy_rule_sets.
const (
	proxyScopeNamespace = "namespace"
	proxyScopeUnit      = "unit"
)

// ProxyRules returns the store of proxy rule sets. Each write checks the
// installation state and the fence of its namespace, checks the version, and
// appends its audit entry in its own transaction.
func (s *Store) ProxyRules() rulestore.Handle {
	return rulestore.Handle{RuleStore: &ProxyRuleStore{s: s}}
}

// ProxyRuleStore is rulestore.RuleStore over the database of a Store.
type ProxyRuleStore struct {
	s *Store
}

var _ rulestore.RuleStore = (*ProxyRuleStore)(nil)

// proxyScopeKind returns the scope_kind column of scope. The unit_id column is
// scope.UnitID, which is empty for a namespace scope.
func proxyScopeKind(scope rulestore.Scope) string {
	if scope.UnitID == "" {
		return proxyScopeNamespace
	}

	return proxyScopeUnit
}

// checkProxyWrite runs the checks that Put and Delete share before the
// transaction opens.
//
// Defense in depth: the service and the handler check the scope too; these
// checks stay so that a new caller cannot write under the fence or the audit
// entry of another namespace.
func checkProxyWrite(scope rulestore.Scope, fence namespace.Fence, e audit.Entry) error {
	if err := rulestore.ValidScope(scope); err != nil {
		return fmt.Errorf("proxy rule set: %w", err)
	}

	if e.Namespace != scope.Namespace {
		return fmt.Errorf("%w: the audit entry names another namespace than the scope", jobproxy.ErrInvalid)
	}

	if fence.Namespace != scope.Namespace {
		return fmt.Errorf("%w: the fence names another namespace than the scope", namespace.ErrFenced)
	}

	return nil
}

// Get implements rulestore.RuleStore.
func (p *ProxyRuleStore) Get(ctx context.Context, scope rulestore.Scope) (rulestore.Stored, error) {
	if err := rulestore.ValidScope(scope); err != nil {
		return rulestore.Stored{}, fmt.Errorf("proxy rule set: %w", err)
	}

	p.s.mu.RLock()
	defer p.s.mu.RUnlock()

	return getProxyRuleSet(ctx, p.s.queries, scope)
}

// getProxyRuleSet reads the proxy rule set of scope with q.
func getProxyRuleSet(ctx context.Context, q *db.Queries, scope rulestore.Scope) (rulestore.Stored, error) {
	kind, unit := proxyScopeKind(scope), scope.UnitID

	row, err := q.GetProxyRuleSet(ctx, db.GetProxyRuleSetParams{
		Namespace: scope.Namespace.String(), ScopeKind: kind, UnitID: unit,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return rulestore.Stored{}, rulestore.ErrNotFound
	}

	if err != nil {
		return rulestore.Stored{}, fmt.Errorf("select proxy rule set: %w", err)
	}

	doc := []byte(row.Document)

	// Defense in depth: Put parses the document too. This check stays so that a
	// row from a restored backup or another version of the schema is never served
	// as a proxy rule set.
	if _, parseErr := jobproxy.Parse(doc); parseErr != nil {
		return rulestore.Stored{}, fmt.Errorf("stored proxy rule set: %w", parseErr)
	}

	return rulestore.Stored{
		Scope: scope, Document: doc, Digest: row.Digest, Version: row.Version, UpdatedAt: row.UpdatedAtMs,
	}, nil
}

// Put implements rulestore.RuleStore.
func (p *ProxyRuleStore) Put(
	ctx context.Context, c rulestore.Change, fence namespace.Fence, e audit.Entry,
) (rulestore.Stored, error) {
	if err := checkProxyWrite(c.Scope, fence, e); err != nil {
		return rulestore.Stored{}, err
	}

	// Defense in depth: the handler parses the document too; the store parses it
	// again so that a caller that skips that check stores nothing.
	if _, err := jobproxy.Parse(c.Document); err != nil {
		return rulestore.Stored{}, fmt.Errorf("proxy rule set: %w", err)
	}

	kind, unit := proxyScopeKind(c.Scope), c.Scope.UnitID
	digest := rulestore.DocumentDigest(c.Document)
	at := e.At.UnixMilli()

	p.s.mu.RLock()
	defer p.s.mu.RUnlock()

	err := p.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		var (
			n   int64
			err error
		)

		if c.Expected == 0 {
			n, err = q.InsertProxyRuleSet(ctx, db.InsertProxyRuleSetParams{
				Namespace: c.Scope.Namespace.String(), ScopeKind: kind, UnitID: unit,
				Document: string(c.Document), Digest: digest, UpdatedAtMs: at,
			})
		} else {
			n, err = q.UpdateProxyRuleSet(ctx, db.UpdateProxyRuleSetParams{
				Document: string(c.Document), Digest: digest, Version: c.Expected, UpdatedAtMs: at,
				Namespace: c.Scope.Namespace.String(), ScopeKind: kind, UnitID: unit,
			})
		}

		if err != nil {
			return fmt.Errorf("write proxy rule set: %w", err)
		}

		if n == 0 {
			return rulestore.ErrConflict
		}

		return nil
	})
	if err != nil {
		return rulestore.Stored{}, err
	}

	return rulestore.Stored{
		Scope: c.Scope, Document: append([]byte(nil), c.Document...), Digest: digest, Version: c.Expected + 1,
		UpdatedAt: at,
	}, nil
}

// Delete implements rulestore.RuleStore.
func (p *ProxyRuleStore) Delete(
	ctx context.Context, scope rulestore.Scope, expected int64, fence namespace.Fence, e audit.Entry,
) error {
	if err := checkProxyWrite(scope, fence, e); err != nil {
		return err
	}

	kind, unit := proxyScopeKind(scope), scope.UnitID

	p.s.mu.RLock()
	defer p.s.mu.RUnlock()

	return p.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, err := q.GetProxyRuleSet(ctx, db.GetProxyRuleSetParams{
			Namespace: scope.Namespace.String(), ScopeKind: kind, UnitID: unit,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return rulestore.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select proxy rule set: %w", err)
		}

		if row.Version != expected {
			return rulestore.ErrConflict
		}

		n, err := q.DeleteProxyRuleSet(ctx, db.DeleteProxyRuleSetParams{
			Namespace: scope.Namespace.String(), ScopeKind: kind, UnitID: unit, Version: expected,
		})
		if err != nil {
			return fmt.Errorf("delete proxy rule set: %w", err)
		}

		if n == 0 {
			return rulestore.ErrConflict
		}

		return nil
	})
}
