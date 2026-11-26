package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/store/codec"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
)

// Column names that the backup lists of the agent tables share with other
// lists.
const (
	agentColLastUsedAtMs = "last_used_at_ms"
	agentColHost         = "host"
)

var (
	_ agent.SecretItems          = AgentSecretItems{s: nil, sealer: nil}
	_ agent.ConnectionStore      = AgentConnections{s: nil}
	_ agent.EgressAllowlistStore = AgentEgressAllowlist{s: nil}
)

// AgentSecretItems returns the store of the secret items of the model
// connection. It seals each value with sealer, which is
// *secret.InstallationSecrets in production.
func (s *Store) AgentSecretItems(sealer agent.SecretSealer) AgentSecretItems {
	return AgentSecretItems{s: s, sealer: sealer}
}

// AgentConnections returns the store of the current and the pending model
// connection document.
func (s *Store) AgentConnections() AgentConnections {
	return AgentConnections{s: s}
}

// AgentEgressAllowlist returns the store of the egress allowlist.
func (s *Store) AgentEgressAllowlist() AgentEgressAllowlist {
	return AgentEgressAllowlist{s: s}
}

// AgentSecretItems implements agent.SecretItems over one database.
type AgentSecretItems struct {
	s      *Store
	sealer agent.SecretSealer
}

// AgentConnections implements agent.ConnectionStore over one database.
type AgentConnections struct {
	s *Store
}

// AgentEgressAllowlist implements agent.EgressAllowlistStore over one
// database.
type AgentEgressAllowlist struct {
	s *Store
}

// checkServerEntry returns agent.ErrInvalid unless e is valid and in the
// scope audit.ServerScope.
//
// Defense in depth: the service checks the entry too; this check stays so that
// a new caller cannot write a record of another scope.
func checkServerEntry(e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: audit entry: %w", agent.ErrInvalid, err)
	}

	if e.Namespace != audit.ServerScope {
		return fmt.Errorf("%w: the audit entry is not in the scope %q", agent.ErrInvalid, audit.ServerScope)
	}

	return nil
}

func agentSecretItemOf(ref, name string, created, changed, used int64) agent.SecretItem {
	return agent.SecretItem{
		Ref:        agent.SecretRef(ref),
		Name:       name,
		CreatedAt:  created,
		ChangedAt:  changed,
		LastUsedAt: used,
	}
}

// Create implements agent.SecretItems.
func (k AgentSecretItems) Create(
	ctx context.Context, name string, value []byte, e audit.Entry,
) (agent.SecretItem, error) {
	// Defense in depth: the handler and the service check the name and the
	// value too; this check stays so that a new caller cannot skip it.
	if err := agent.CheckSecretItem(name, value); err != nil {
		return agent.SecretItem{}, err
	}

	if err := checkServerEntry(e); err != nil {
		return agent.SecretItem{}, err
	}

	ref := agent.NewSecretRef()

	// Seal before the transaction: the first seal stores a key in its own
	// transaction.
	sealed, err := k.sealer.SealBound(ctx, agent.SecretItemsScope, []byte(ref), value)
	if err != nil {
		return agent.SecretItem{}, fmt.Errorf("seal secret item: %w", err)
	}

	now := codec.Millis(time.Now().UTC())

	row := db.CreateAgentSecretItemParams{
		Ref: string(ref), Name: name, Ciphertext: sealed, CreatedAtMs: now, ChangedAtMs: now,
	}

	err = k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		if _, insErr := tx.q.CreateAgentSecretItem(ctx, row); insErr != nil {
			return fmt.Errorf("insert secret item: %w", insErr)
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
	if err != nil {
		return agent.SecretItem{}, err
	}

	return agentSecretItemOf(string(ref), name, now, now, 0), nil
}

// List implements agent.SecretItems.
func (k AgentSecretItems) List(ctx context.Context) ([]agent.SecretItem, error) {
	rows, err := k.s.queries.ListAgentSecretItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("select secret items: %w", err)
	}

	out := make([]agent.SecretItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, agentSecretItemOf(r.Ref, r.Name, r.CreatedAtMs, r.ChangedAtMs, r.LastUsedAtMs))
	}

	return out, nil
}

// Get implements agent.SecretItems.
func (k AgentSecretItems) Get(ctx context.Context, ref agent.SecretRef) (agent.SecretItem, error) {
	r, err := k.s.queries.GetAgentSecretItem(ctx, string(ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.SecretItem{}, agent.ErrSecretNotFound
	}

	if err != nil {
		return agent.SecretItem{}, fmt.Errorf("select secret item: %w", err)
	}

	return agentSecretItemOf(r.Ref, r.Name, r.CreatedAtMs, r.ChangedAtMs, r.LastUsedAtMs), nil
}

// Replace implements agent.SecretItems.
func (k AgentSecretItems) Replace(
	ctx context.Context, ref agent.SecretRef, value []byte, e audit.Entry,
) (agent.SecretItem, error) {
	// Defense in depth: the service and the handler check the value too;
	// this check stays so that a new caller cannot store a value that they
	// would refuse.
	if err := agent.CheckSecretValue(value); err != nil {
		return agent.SecretItem{}, err
	}

	if err := checkServerEntry(e); err != nil {
		return agent.SecretItem{}, err
	}

	sealed, err := k.sealer.SealBound(ctx, agent.SecretItemsScope, []byte(ref), value)
	if err != nil {
		return agent.SecretItem{}, fmt.Errorf("seal secret item: %w", err)
	}

	var out agent.SecretItem

	err = k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		r, repErr := tx.q.ReplaceAgentSecretItem(ctx, db.ReplaceAgentSecretItemParams{
			Ciphertext: sealed, ChangedAtMs: codec.Millis(time.Now().UTC()), Ref: string(ref),
		})
		if errors.Is(repErr, pgx.ErrNoRows) {
			return agent.ErrSecretNotFound
		}

		if repErr != nil {
			return fmt.Errorf("update secret item: %w", repErr)
		}

		out = agentSecretItemOf(r.Ref, r.Name, r.CreatedAtMs, r.ChangedAtMs, r.LastUsedAtMs)

		return appendScopedServerEntry(ctx, tx, &e)
	})
	if err != nil {
		return agent.SecretItem{}, err
	}

	return out, nil
}

// Delete implements agent.SecretItems. The foreign key of the reference rows
// refuses the delete while a stored document uses the item.
func (k AgentSecretItems) Delete(ctx context.Context, ref agent.SecretRef, e audit.Entry) error {
	if err := checkServerEntry(e); err != nil {
		return err
	}

	return k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		n, err := tx.q.DeleteAgentSecretItem(ctx, string(ref))
		if isForeignKeyViolation(err) {
			return agent.ErrSecretInUse
		}

		if err != nil {
			return fmt.Errorf("delete secret item: %w", err)
		}

		if n == 0 {
			return agent.ErrSecretNotFound
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
}

// Open implements agent.SecretItems.
func (k AgentSecretItems) Open(ctx context.Context, ref agent.SecretRef) ([]byte, error) {
	sealed, err := k.s.queries.GetAgentSecretItemCiphertext(ctx, string(ref))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, agent.ErrSecretNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("select secret item: %w", err)
	}

	value, err := k.sealer.OpenBound(ctx, agent.SecretItemsScope, []byte(ref), sealed)
	if err != nil {
		return nil, fmt.Errorf("open secret item: %w", err)
	}

	return value, nil
}

// storeAgentRefs replaces the reference rows of the document ref by the
// references of spec. A reference to an item that does not exist fails with
// agent.ErrSecretNotFound.
func storeAgentRefs(ctx context.Context, q *db.Queries, ref string, spec agent.ConnectionSpec) error {
	refs, err := spec.SecretRefs()
	if err != nil {
		return fmt.Errorf("%w: %w", agent.ErrInvalid, err)
	}

	if delErr := q.DeleteAgentSecretRefs(ctx, ref); delErr != nil {
		return fmt.Errorf("delete reference rows: %w", delErr)
	}

	for _, secretRef := range refs {
		insErr := q.InsertAgentSecretRef(ctx, db.InsertAgentSecretRefParams{Ref: ref, Secret: string(secretRef)})
		if isForeignKeyViolation(insErr) {
			return fmt.Errorf("%w: %s", agent.ErrSecretNotFound, secretRef)
		}

		if insErr != nil {
			return fmt.Errorf("insert reference row: %w", insErr)
		}
	}

	return nil
}

func parseStoredSpec(raw []byte) (agent.ConnectionSpec, error) {
	spec, err := agent.ParseConnectionSpec(raw)
	if err != nil {
		return agent.ConnectionSpec{}, fmt.Errorf("stored connection document: %w", err)
	}

	return spec, nil
}

// Current implements agent.ConnectionStore.
func (k AgentConnections) Current(ctx context.Context) (agent.ConnectionSpec, int64, error) {
	row, err := k.s.queries.GetAgentConnectionCurrent(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.ConnectionSpec{}, 0, agent.ErrDisabled
	}

	if err != nil {
		return agent.ConnectionSpec{}, 0, fmt.Errorf("select connection: %w", err)
	}

	spec, err := parseStoredSpec(row.Document)

	return spec, row.Version, err
}

// Pending implements agent.ConnectionStore.
func (k AgentConnections) Pending(ctx context.Context, probe agent.ProbeRef) (agent.ConnectionSpec, int64, error) {
	if err := probe.Validate(); err != nil {
		return agent.ConnectionSpec{}, 0, err
	}

	row, err := k.s.queries.GetAgentConnectionPending(ctx, string(probe))
	if errors.Is(err, pgx.ErrNoRows) {
		return agent.ConnectionSpec{}, 0, agent.ErrNotFound
	}

	if err != nil {
		return agent.ConnectionSpec{}, 0, fmt.Errorf("select pending connection: %w", err)
	}

	spec, err := parseStoredSpec(row.Document)

	return spec, row.Version, err
}

// PendingAll implements agent.ConnectionStore.
func (k AgentConnections) PendingAll(ctx context.Context) ([]agent.PendingDocument, error) {
	rows, err := k.s.queries.ListAgentConnectionPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending connections: %w", err)
	}

	out := make([]agent.PendingDocument, 0, len(rows))

	for _, row := range rows {
		spec, err := parseStoredSpec(row.Document)
		if err != nil {
			return nil, err
		}

		out = append(out, agent.PendingDocument{Probe: agent.ProbeRef(row.ProbeID), Spec: spec, Version: row.Version})
	}

	return out, nil
}

// PutPending implements agent.ConnectionStore.
func (k AgentConnections) PutPending(
	ctx context.Context, probe agent.ProbeRef, c agent.ConnectionSpec, e audit.Entry,
) error {
	if err := probe.Validate(); err != nil {
		return err
	}

	if err := checkServerEntry(e); err != nil {
		return err
	}

	row := db.PutAgentConnectionPendingParams{
		ProbeID: string(probe), Document: c.Marshal(), Version: int64(c.Version()),
		CreatedAtMs: codec.Millis(time.Now().UTC()),
	}

	return k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		if err := tx.q.PutAgentConnectionPending(ctx, row); err != nil {
			return fmt.Errorf("upsert pending connection: %w", err)
		}

		if err := storeAgentRefs(ctx, tx.q, probe.DocumentRef(), c); err != nil {
			return err
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
}

// Promote implements agent.ConnectionStore.
func (k AgentConnections) Promote(ctx context.Context, probe agent.ProbeRef, e audit.Entry) error {
	if err := probe.Validate(); err != nil {
		return err
	}

	if err := checkServerEntry(e); err != nil {
		return err
	}

	return k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		row, err := tx.q.GetAgentConnectionPending(ctx, string(probe))
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select pending connection: %w", err)
		}

		spec, err := parseStoredSpec(row.Document)
		if err != nil {
			return err
		}

		if promoteErr := promoteAgentRows(ctx, tx.q, probe, row, spec); promoteErr != nil {
			return promoteErr
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
}

// promoteAgentRows makes the pending document row the current document and
// moves its reference rows. It runs in the transaction of Promote.
func promoteAgentRows(
	ctx context.Context, q *db.Queries, probe agent.ProbeRef, row db.GetAgentConnectionPendingRow,
	spec agent.ConnectionSpec,
) error {
	now := codec.Millis(time.Now().UTC())
	current := db.PutAgentConnectionCurrentParams{
		Document: row.Document, Version: row.Version, CreatedAtMs: now, ChangedAtMs: now,
	}

	if err := q.PutAgentConnectionCurrent(ctx, current); err != nil {
		return fmt.Errorf("upsert connection: %w", err)
	}

	if err := storeAgentRefs(ctx, q, agent.CurrentDocumentRef, spec); err != nil {
		return err
	}

	if err := q.DeleteAgentSecretRefs(ctx, probe.DocumentRef()); err != nil {
		return fmt.Errorf("delete reference rows: %w", err)
	}

	if _, err := q.DeleteAgentConnectionPending(ctx, string(probe)); err != nil {
		return fmt.Errorf("delete pending connection: %w", err)
	}

	return nil
}

// Delete implements agent.ConnectionStore.
func (k AgentConnections) Delete(ctx context.Context, e audit.Entry) error {
	if err := checkServerEntry(e); err != nil {
		return err
	}

	return k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		n, err := tx.q.DeleteAgentConnectionCurrent(ctx)
		if err != nil {
			return fmt.Errorf("delete connection: %w", err)
		}

		// The pending documents and the reference rows go even when no
		// current document exists: the first connection is pending before
		// it is promoted, and the disconnect removes it.
		pending, err := tx.q.DeleteAgentConnectionPendingAll(ctx)
		if err != nil {
			return fmt.Errorf("delete pending connections: %w", err)
		}

		if err = tx.q.DeleteAgentSecretRefsAll(ctx); err != nil {
			return fmt.Errorf("delete reference rows: %w", err)
		}

		if n == 0 && pending == 0 {
			return agent.ErrDisabled
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
}

// Get implements agent.EgressAllowlistStore.
func (k AgentEgressAllowlist) Get(ctx context.Context) (egress.Allowlist, error) {
	hosts, err := k.s.queries.ListAgentEgressAllowlistHosts(ctx)
	if err != nil {
		return egress.Allowlist{}, fmt.Errorf("select allowlist hosts: %w", err)
	}

	rows, err := k.s.queries.ListAgentEgressAllowlistRules(ctx)
	if err != nil {
		return egress.Allowlist{}, fmt.Errorf("select allowlist rules: %w", err)
	}

	out := egress.Allowlist{Hosts: hosts, CIDRs: nil}

	for _, r := range rows {
		// The stored text is untrusted: a bad value fails closed.
		prefix, parseErr := netip.ParsePrefix(r.Cidr)
		if parseErr != nil {
			return egress.Allowlist{}, fmt.Errorf("stored allowlist rule %q: %w", r.Cidr, parseErr)
		}

		out.CIDRs = append(out.CIDRs, egress.CIDRRule{CIDR: prefix, Port: int(r.Port), HTTP: r.Http != 0})
	}

	return out, nil
}

// Set implements agent.EgressAllowlistStore.
func (k AgentEgressAllowlist) Set(ctx context.Context, a egress.Allowlist, e audit.Entry) error {
	if err := checkServerEntry(e); err != nil {
		return err
	}

	// Defense in depth: the service checks the allowlist too; this check
	// stays so that a new caller cannot store a list that the rules would
	// refuse.
	if err := agent.CheckAllowlist(a); err != nil {
		return err
	}

	return k.s.inServerAuditTx(ctx, func(tx serverAuditTx) error {
		if err := writeAgentAllowlist(ctx, tx.q, a); err != nil {
			return err
		}

		return appendScopedServerEntry(ctx, tx, &e)
	})
}

// writeAgentAllowlist replaces the allowlist rows by a. It runs in the
// transaction of Set.
func writeAgentAllowlist(ctx context.Context, q *db.Queries, a egress.Allowlist) error {
	if err := q.DeleteAgentEgressAllowlistHosts(ctx); err != nil {
		return fmt.Errorf("delete allowlist hosts: %w", err)
	}

	if err := q.DeleteAgentEgressAllowlistRules(ctx); err != nil {
		return fmt.Errorf("delete allowlist rules: %w", err)
	}

	if err := insertAgentHosts(ctx, q, a.Hosts); err != nil {
		return err
	}

	if err := insertAgentRules(ctx, q, a.CIDRs); err != nil {
		return err
	}

	if err := q.SetAgentEgressAllowlist(ctx, codec.Millis(time.Now().UTC())); err != nil {
		return fmt.Errorf("stamp allowlist: %w", err)
	}

	return nil
}

func insertAgentHosts(ctx context.Context, q *db.Queries, hosts []string) error {
	for i, host := range hosts {
		id, err := int32Of(i)
		if err != nil {
			return err
		}

		row := db.InsertAgentEgressAllowlistHostParams{ID: id, Host: host}

		if err = q.InsertAgentEgressAllowlistHost(ctx, row); err != nil {
			return fmt.Errorf("insert allowlist host: %w", err)
		}
	}

	return nil
}

func insertAgentRules(ctx context.Context, q *db.Queries, rules []egress.CIDRRule) error {
	for i, rule := range rules {
		id, err := int32Of(i)
		if err != nil {
			return err
		}

		port, err := int32Of(rule.Port)
		if err != nil {
			return err
		}

		row := db.InsertAgentEgressAllowlistRuleParams{ID: id, Cidr: rule.CIDR.String(), Port: port, Http: 0}
		if rule.HTTP {
			row.Http = 1
		}

		if err = q.InsertAgentEgressAllowlistRule(ctx, row); err != nil {
			return fmt.Errorf("insert allowlist rule: %w", err)
		}
	}

	return nil
}

// int32Of converts n for a column of the type integer. It fails closed for a
// value outside the range.
func int32Of(n int) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("%w: number %d is out of range", agent.ErrInvalid, n)
	}

	return int32(n), nil
}
