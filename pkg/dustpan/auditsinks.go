package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"opendustpan.org/opendustpan/internal/instance"
	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
)

// WithAuditSinks sets the log sinks that receive a copy of each audit entry
// (ADR 60 decision 13). The SQL table stays the record of the audit log.
// Each sink needs a unique name: it keys the cursor of the sink in the
// database and labels its metrics. No sink is the default: without this
// option the server ships nothing. The namespace store must implement
// audit.Feed and audit.SinkLeaser, as both SQL adapters do. The server never
// closes the sinks: the caller closes them after Run returns.
func WithAuditSinks(sinks ...audit.ShipSink) Option {
	return func(o *settings) { o.auditSinks = append(o.auditSinks, sinks...) }
}

// WithInstanceID sets the ID of this server process in the sink leases of
// its audit shipper. Each start makes a new ID. Without this option the
// server makes one at start, so each process still holds its own leases.
func WithInstanceID(id string) Option {
	return func(o *settings) { o.instanceID = id }
}

// newAuditShipper builds the shipper of the configured audit sinks, or nil
// when none is configured. The failure and delivery hooks count on the
// server metrics, labeled with the sink name only. Only the holder of the
// lease of a sink ships to it.
func newAuditShipper(cfg *settings, observed *metrics.ServerMetrics) (*audit.Shipper, error) {
	if len(cfg.auditSinks) == 0 {
		//nolint:nilnil // No sink without an error means the server ships nothing.
		return nil, nil
	}

	feed, ok := cfg.namespaceStore.(audit.Feed)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit sink cursors: %w", ErrMissingOption)
	}

	leases, ok := cfg.namespaceStore.(audit.SinkLeaser)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit sink leases: %w", ErrMissingOption)
	}

	if len(cfg.auditSinks) > metrics.MaxSinkSeries {
		cfg.logger.Warn("more audit sinks than metric series: the metrics of the later sinks are not counted",
			slog.Int("sinks", len(cfg.auditSinks)), slog.Int("series", metrics.MaxSinkSeries))
	}

	holder := cfg.instanceID
	if holder == "" {
		holder = instance.NewID()
	}

	shipper, err := audit.NewShipper(audit.ShipperConfig{
		Feed:          feed,
		Namespaces:    cfg.namespaceStore,
		Logger:        cfg.logger,
		OnShipped:     observed.AddAuditShipped,
		OnFailure:     observed.IncAuditSinkFailure,
		OnSkipped:     observed.AddAuditSkipped,
		OnLag:         observed.SetAuditSinkLag,
		Now:           nil,
		Sinks:         cfg.auditSinks,
		InstanceID:    holder,
		Leases:        leases,
		LeaseTTL:      0,
		RenewInterval: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("audit sinks: %w", err)
	}

	for _, sink := range cfg.auditSinks {
		observed.SetAuditSinkLag(sink.Name, 0)
	}

	return shipper, nil
}

// startAuditShipper runs the audit shipper until ctx is done. The returned
// function waits until the shipper stopped. With no shipper it starts
// nothing.
func (s *Server) startAuditShipper(ctx context.Context) func() {
	if s.shipper == nil {
		return func() {}
	}

	var wg sync.WaitGroup

	wg.Go(func() { s.shipper.Run(ctx) })

	return wg.Wait
}
