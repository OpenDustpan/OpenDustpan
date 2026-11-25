//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/agent"
	"opendustpan.org/opendustpan/internal/store/postgres"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// agentSeedProbeRef is the probe reference of a pending connection that an
// e2e step seeds through the store port. The write route is a later change,
// so the step stores the document directly.
const agentSeedProbeRef agent.ProbeRef = "e2e-seed"

// SeedAgentPendingConnection stores spec as the pending model connection
// document of the e2e seed probe. The test puts the document into the store
// through the store port, because the write route is a later change.
func (e *Env) SeedAgentPendingConnection(ctx context.Context, tb testing.TB, spec agent.ConnectionSpec) {
	tb.Helper()

	e.withStore(ctx, tb, func(st namespace.Store) error {
		return seedAgentPendingConnection(ctx, st, spec)
	})
}

// PromoteAgentPendingConnection promotes the pending model connection
// document of the e2e seed probe to the current document.
func (e *Env) PromoteAgentPendingConnection(ctx context.Context, tb testing.TB) {
	tb.Helper()

	e.withStore(ctx, tb, func(st namespace.Store) error {
		return promoteAgentPendingConnection(ctx, st)
	})
}

// seedAgentPendingConnection writes spec as the pending document of the e2e
// seed probe.
func seedAgentPendingConnection(ctx context.Context, st namespace.Store, spec agent.ConnectionSpec) error {
	return withAgentConnections(st, func(store agent.ConnectionStore) error {
		entry := freshAdminEntry(
			time.Now().UTC().Truncate(time.Millisecond),
			"agent/connection/pending/"+string(agentSeedProbeRef),
		)
		entry.Action = "server:manage-agent"

		return store.PutPending(ctx, agentSeedProbeRef, spec, entry)
	})
}

// promoteAgentPendingConnection promotes the pending document of the e2e
// seed probe.
func promoteAgentPendingConnection(ctx context.Context, st namespace.Store) error {
	return withAgentConnections(st, func(store agent.ConnectionStore) error {
		entry := freshAdminEntry(time.Now().UTC().Truncate(time.Millisecond), "agent/connection")
		entry.Action = "server:manage-agent"

		return store.Promote(ctx, agentSeedProbeRef, entry)
	})
}

// withAgentConnections calls fn with the connection store of st. Both SQL
// adapters keep agent connections; a store of another kind fails.
func withAgentConnections(st namespace.Store, fn func(agent.ConnectionStore) error) error {
	switch s := st.(type) {
	case *sqlite.Store:
		return fn(s.AgentConnections())
	case *postgres.Store:
		return fn(s.AgentConnections())
	default:
		return fmt.Errorf("%w: %T", errNoPasskeyRows, st)
	}
}
