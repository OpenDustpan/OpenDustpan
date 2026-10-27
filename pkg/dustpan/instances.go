package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/pkg/installation"
)

// instanceStopWait bounds the delete of the instance version row at a clean
// stop. The delete runs after the context of Run ended, so it has a deadline
// of its own: a database that does not answer must not hold the stop.
const instanceStopWait = 5 * time.Second

// WithInstanceStore sets the store that keeps the instance version rows. With
// it, Start writes one row for this process with its instance ID, its binary
// version (WithVersion) and its start time, and Run deletes the row when it
// returns at a clean stop. The installation API lists the rows and lets an
// administrator delete the row of a process that died. Without it, the server
// writes no row and the instance routes answer 501.
//
// The row is not a liveness lease: nothing renews it, and nothing reads it to
// decide that an instance is live. A process that dies leaves its row.
func WithInstanceStore(store installation.InstanceStore) Option {
	return func(o *settings) { o.instanceStore = store }
}

// instanceState is the version row of this server process and the store that
// keeps it. The store is nil for a server that writes no row.
type instanceState struct {
	store installation.InstanceStore
	// id and version are the fields of the row. The ID is the instance ID of
	// the server, or a new one when the program sets none.
	id      string
	version string
	// registered is true from the write of the row to its delete, so the
	// stop deletes a row that this process wrote and no other.
	registered *atomic.Bool
}

// newInstanceState returns the row that the server writes for itself. An empty
// version is a dev build, as in the backup manifest.
func newInstanceState(cfg *settings) instanceState {
	id := cfg.instanceID
	if id == "" {
		id = instance.NewID()
	}

	version := cfg.version
	if version == "" {
		version = "dev"
	}

	return instanceState{store: cfg.instanceStore, id: id, version: version, registered: &atomic.Bool{}}
}

// registerInstance writes the version row of this process. It is a start-up
// step that follows the claim of the installation kind, so a database of
// another kind gets no row. A row that cannot be written stops the start: an
// instance that the contract-migration check cannot see could let a migration
// run under it.
func (s *Server) registerInstance(ctx context.Context) error {
	if s.instances.store == nil {
		return nil
	}

	row := installation.Instance{
		StartedAt:     s.now().UTC(),
		ID:            s.instances.id,
		BinaryVersion: s.instances.version,
	}

	if err := s.instances.store.RegisterInstance(ctx, row); err != nil {
		return fmt.Errorf("write instance version row: %w", err)
	}

	s.instances.registered.Store(true)

	s.logger.InfoContext(ctx, "instance version row written",
		slog.String("instance_id", row.ID), slog.String("binary_version", row.BinaryVersion))

	return nil
}

// unregisterInstance deletes the version row of this process at a clean stop.
// A failed delete only logs: the process stops anyway, and an administrator
// can delete the row with DELETE /api/server/instances/{id}.
func (s *Server) unregisterInstance(ctx context.Context) {
	if s.instances.store == nil || !s.instances.registered.Swap(false) {
		return
	}

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), instanceStopWait)
	defer cancel()

	if err := s.instances.store.UnregisterInstance(stopCtx, s.instances.id); err != nil {
		s.logger.WarnContext(ctx, "instance version row not deleted at stop",
			slog.String("instance_id", s.instances.id), slog.String("reason", err.Error()))

		return
	}

	s.logger.InfoContext(ctx, "instance version row deleted at stop", slog.String("instance_id", s.instances.id))
}
