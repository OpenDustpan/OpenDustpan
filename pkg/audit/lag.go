package audit

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
)

// SinkStatus describes the delivery state of one audit sink: its name and
// kind, the last delivered sequence of each scope, the delivery lag in
// seconds and the last error class. LagSeconds is the age of the oldest
// entry the sink has not delivered yet, or 0 when it is up to date.
// LastError is empty when the last round succeeded.
type SinkStatus struct {
	Name       string
	Kind       string
	Cursors    map[string]int64
	LagSeconds float64
	LastError  string
}

// LagSeconds answers the age of the oldest undelivered entry in seconds.
// A zero oldest means no entry waits, so the lag is 0. A now before oldest
// also answers 0.
func LagSeconds(oldest, now time.Time) float64 {
	if oldest.IsZero() {
		return 0
	}

	return max(now.Sub(oldest).Seconds(), 0)
}

// Lag answers the delivery lag of the sink named sink in seconds: the age
// of the oldest entry above its cursors, or 0 when no entry waits.
func (s *Shipper) Lag(ctx context.Context, sink string, now time.Time) (float64, error) {
	status, err := s.sinkStatus(ctx, ShipSink{Sink: nil, Name: sink, Kind: "", Interval: 0}, nil, now)
	if err != nil {
		return 0, err
	}

	return status.LagSeconds, nil
}

// ListAuditSinks answers the delivery state of every configured sink in
// order. It meets the sink source of the installation API.
func (s *Shipper) ListAuditSinks(ctx context.Context) ([]SinkStatus, error) {
	return s.Status(ctx)
}

// Status answers the delivery state of every configured sink in order: its
// cursors, its lag and its last error.
func (s *Shipper) Status(ctx context.Context) ([]SinkStatus, error) {
	namespaces, err := s.listScopes(ctx)
	if err != nil {
		return nil, err
	}

	now := s.cfg.Now()
	out := make([]SinkStatus, 0, len(s.cfg.Sinks))

	for _, sink := range s.cfg.Sinks {
		status, err := s.sinkStatus(ctx, sink, namespaces, now)
		if err != nil {
			return nil, err
		}

		out = append(out, status)
	}

	return out, nil
}

// listScopes answers every scope the shipper copies: each namespace and the
// installation scope.
func (s *Shipper) listScopes(ctx context.Context) ([]namespace.Namespace, error) {
	namespaces, err := s.cfg.Namespaces.ListNamespaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("list namespaces for sink status: %w", err)
	}

	return append(namespaces, serverScope()), nil
}

// sinkStatus answers the delivery state of one sink. When scopes is nil it
// lists the scopes itself, so Lag can share it.
func (s *Shipper) sinkStatus(
	ctx context.Context, sink ShipSink, scopes []namespace.Namespace, now time.Time,
) (SinkStatus, error) {
	if scopes == nil {
		var err error

		scopes, err = s.listScopes(ctx)
		if err != nil {
			return SinkStatus{}, fmt.Errorf("list namespaces for sink %s: %w", sink.Name, err)
		}
	}

	cursors := make(map[string]int64, len(scopes))

	var lag float64

	for _, ns := range scopes {
		cursor, pending, err := s.scopeState(ctx, sink.Name, ns)
		if err != nil {
			return SinkStatus{}, err
		}

		cursors[ns.Name.String()] = cursor

		if got := LagSeconds(pending, now); got > lag {
			lag = got
		}
	}

	kind := sink.Kind
	if kind == "" {
		for _, configured := range s.cfg.Sinks {
			if configured.Name == sink.Name {
				kind = configured.Kind

				break
			}
		}
	}

	return SinkStatus{
		Name:       sink.Name,
		Kind:       kind,
		Cursors:    cursors,
		LagSeconds: lag,
		LastError:  s.lastErrorOf(sink.Name),
	}, nil
}

// scopeState answers the cursor of sink in ns and the time of its oldest
// pending entry: zero when no entry waits.
func (s *Shipper) scopeState(
	ctx context.Context, sink string, ns namespace.Namespace,
) (int64, time.Time, error) {
	cursor, err := s.cfg.Feed.SinkCursor(ctx, sink, ns.Name)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read cursor of sink %s: %w", sink, err)
	}

	entries, err := s.cfg.Feed.After(ctx, ns.Name, cursor, 1)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read pending entry of sink %s: %w", sink, err)
	}

	if len(entries) == 0 {
		return cursor, time.Time{}, nil
	}

	return cursor, entries[0].At, nil
}
