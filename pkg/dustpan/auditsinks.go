package dustpan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"opendustpan.org/opendustpan/internal/metrics"
	"opendustpan.org/opendustpan/pkg/audit"
)

// WithAuditSinks sets the log sinks that receive a copy of each audit entry
// (ADR 60 decision 13). The SQL table stays the record of the audit log.
// Each sink needs a unique name: it keys the cursor of the sink in the
// database and labels its metrics. No sink is the default: without this
// option the server ships nothing. The namespace store must implement
// audit.Feed, as both SQL adapters do. The server never closes the sinks:
// the caller closes them after Run returns.
func WithAuditSinks(sinks ...audit.ShipSink) Option {
	return func(o *settings) { o.auditSinks = append(o.auditSinks, sinks...) }
}

// newAuditShipper builds the shipper of the configured audit sinks, or nil
// when none is configured. The failure and delivery hooks count on the
// server metrics, labeled with the sink name only.
func newAuditShipper(cfg settings, observed *metrics.ServerMetrics) (*audit.Shipper, error) {
	if len(cfg.auditSinks) == 0 {
		//nolint:nilnil // No sink without an error means the server ships nothing.
		return nil, nil
	}

	feed, ok := cfg.namespaceStore.(audit.Feed)
	if !ok {
		return nil, fmt.Errorf("namespace store keeps no audit sink cursors: %w", ErrMissingOption)
	}

	if len(cfg.auditSinks) > metrics.MaxSinkSeries {
		cfg.logger.Warn("more audit sinks than metric series: the metrics of the later sinks are not counted",
			slog.Int("sinks", len(cfg.auditSinks)), slog.Int("series", metrics.MaxSinkSeries))
	}

	shipper, err := audit.NewShipper(audit.ShipperConfig{
		Feed:       feed,
		Namespaces: cfg.namespaceStore,
		Logger:     cfg.logger,
		OnShipped:  observed.AddAuditShipped,
		OnFailure:  observed.IncAuditSinkFailure,
		OnSkipped:  observed.AddAuditSkipped,
		Sinks:      cfg.auditSinks,
	})
	if err != nil {
		return nil, fmt.Errorf("audit sinks: %w", err)
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
