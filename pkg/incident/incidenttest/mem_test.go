package incidenttest_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/incident"
	"opendustpan.org/opendustpan/pkg/incident/incidenttest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	incidenttest.Run(t, func(*testing.T) incidenttest.Harness {
		db := &memStore{Domain: opsmem.New(), incidents: map[string]incident.Incident{}, notes: nil}

		return incidenttest.Harness{Store: db, Domain: db}
	})
}

// memStore is the reference adapter of incident.Store in memory.
type memStore struct {
	*opsmem.Domain

	incidents map[string]incident.Incident
	notes     []incident.Note
}

// translate maps a fence that does not belong to the namespace of the row
// to the sentinel of the port: want for this method.
func translate(err, want error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%w: %w", want, err)
	}

	return err
}

func (m *memStore) Create(_ context.Context, in incident.Incident, fence namespace.Fence, e audit.Entry) error {
	if err := in.ValidateNew(); err != nil {
		return err
	}

	err := m.Write(fence, opsmem.Scope(in.Namespace), []audit.Entry{e}, func() error {
		if _, ok := m.incidents[in.ID]; ok {
			return fmt.Errorf("create %s: %w", in.ID, incident.ErrExists)
		}

		in.Version = 1
		in.Units = slices.Clone(in.Units)
		in.Jobs = slices.Clone(in.Jobs)
		in.Reviews = slices.Clone(in.Reviews)
		in.Classifications = slices.Clone(in.Classifications)
		in.Reports = slices.Clone(in.Reports)
		m.incidents[in.ID] = in

		return nil
	})

	return translate(err, incident.ErrInvalid)
}

func (m *memStore) Get(_ context.Context, id string) (incident.Incident, error) {
	var (
		in incident.Incident
		ok bool
	)

	m.Read(func() { in, ok = m.incidents[id] })

	if !ok {
		return in, fmt.Errorf("get %s: %w", id, incident.ErrNotFound)
	}

	in.Classifications = slices.Clone(in.Classifications)
	in.Reports = slices.Clone(in.Reports)

	return in, nil
}

func (m *memStore) Update(_ context.Context, in incident.Incident, fence namespace.Fence, e audit.Entry) error {
	err := m.Write(fence, m.scopeOf("update", in.ID), []audit.Entry{e}, func() error {
		old := m.incidents[in.ID]

		switch {
		case old.Namespace != in.Namespace:
			return fmt.Errorf("update %s: %w: an incident stays in its namespace", in.ID, incident.ErrInvalid)
		case old.Version != in.Version:
			return fmt.Errorf("update %s: %w", in.ID, incident.ErrConflict)
		case old.State == incident.StateClosed:
			return fmt.Errorf("update %s: %w: the incident is closed", in.ID, incident.ErrTransition)
		}

		in.State, in.OpenedAt, in.OpenedBy = old.State, old.OpenedAt, old.OpenedBy
		in.AcknowledgedAt, in.MitigatedAt, in.ResolvedAt, in.ClosedAt = old.AcknowledgedAt, old.MitigatedAt,
			old.ResolvedAt, old.ClosedAt
		in.Classifications, in.Reports = old.Classifications, old.Reports

		if err := in.Validate(); err != nil {
			return fmt.Errorf("update %s: %w", in.ID, err)
		}

		in.Units = slices.Clone(in.Units)
		in.Jobs = slices.Clone(in.Jobs)
		in.Reviews = slices.Clone(in.Reviews)
		in.Version++
		m.incidents[in.ID] = in

		return nil
	})

	return translate(err, incident.ErrNotFound)
}

func (m *memStore) Step(
	_ context.Context,
	r incident.StepRequest,
	fence namespace.Fence,
	e audit.Entry,
) (incident.Incident, error) {
	var out incident.Incident

	id := r.ID

	err := m.Write(fence, m.scopeOf("step", id), []audit.Entry{e}, func() error {
		old := m.incidents[id]
		if old.Version != r.Version {
			return fmt.Errorf("step %s: %w", id, incident.ErrConflict)
		}

		in, err := incident.Apply(old, r.Step, r.By, r.At)
		if err != nil {
			return fmt.Errorf("step %s: %w", id, err)
		}

		if err := in.Validate(); err != nil {
			return fmt.Errorf("step %s: %w", id, err)
		}

		in.Version++
		m.incidents[id], out = in, in

		return nil
	})
	if err != nil {
		return incident.Incident{}, translate(err, incident.ErrNotFound)
	}

	out.Classifications = slices.Clone(out.Classifications)
	out.Reports = slices.Clone(out.Reports)

	return out, nil
}

func (m *memStore) SetClassification(
	_ context.Context,
	id string,
	c incident.Classification,
	fence namespace.Fence,
	e audit.Entry,
) error {
	if err := c.Validate(); err != nil {
		return err
	}

	c.Criteria = slices.Clone(c.Criteria)

	err := m.Write(fence, m.scopeOf("classify", id), []audit.Entry{e}, func() error {
		in := m.incidents[id]
		if len(in.Classifications) >= incident.MaxRecords {
			return fmt.Errorf(
				"classify %s: %w: at most %d classifications",
				id,
				incident.ErrInvalid,
				incident.MaxRecords,
			)
		}

		in.Classifications = append(slices.Clone(in.Classifications), c)
		in.Version++
		m.incidents[id] = in

		return nil
	})

	return translate(err, incident.ErrNotFound)
}

func (m *memStore) AddReport(
	_ context.Context,
	id string,
	r incident.Report,
	fence namespace.Fence,
	e audit.Entry,
) error {
	if err := r.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, m.scopeOf("add report to", id), []audit.Entry{e}, func() error {
		in := m.incidents[id]
		if len(in.Reports) >= incident.MaxRecords {
			return fmt.Errorf("add report to %s: %w: at most %d reports", id, incident.ErrInvalid, incident.MaxRecords)
		}

		in.Reports = append(slices.Clone(in.Reports), r)
		in.Version++
		m.incidents[id] = in

		return nil
	})

	return translate(err, incident.ErrNotFound)
}

func (m *memStore) List(_ context.Context, f incident.Filter) ([]incident.Incident, error) {
	var out []incident.Incident

	m.Read(func() {
		for id := range m.incidents {
			in := m.incidents[id]

			switch {
			case f.Namespace != "" && in.Namespace != f.Namespace,
				f.State != "" && in.State != f.State,
				f.Unit != "" && !slices.Contains(in.Units, f.Unit):
				continue
			default:
				// The incident matches the filter.
			}

			out = append(out, in)
		}
	})

	slices.SortFunc(out, func(a, b incident.Incident) int {
		return cmp.Or(b.OpenedAt.Compare(a.OpenedAt), strings.Compare(b.ID, a.ID))
	})

	if f.Limit > 0 {
		out = out[:min(f.Limit, len(out))]
	}

	return out, nil
}

func (m *memStore) AddNote(_ context.Context, n incident.Note, fence namespace.Fence, e audit.Entry) error {
	if err := n.Validate(); err != nil {
		return err
	}

	err := m.Write(fence, m.scopeOf("add note to", n.Incident), []audit.Entry{e}, func() error {
		m.notes = append(m.notes, n)

		return nil
	})

	return translate(err, incident.ErrNotFound)
}

func (m *memStore) Notes(_ context.Context, id string) ([]incident.Note, error) {
	var out []incident.Note

	m.Read(func() {
		for _, n := range m.notes {
			if n.Incident == id {
				out = append(out, n)
			}
		}
	})

	slices.SortStableFunc(out, func(a, b incident.Note) int { return a.At.Compare(b.At) })

	return out, nil
}

// scopeOf returns a scope function that looks up the stored incident id,
// for a write on a stored incident. It runs under the mutex of the domain.
func (m *memStore) scopeOf(op, id string) func() (namespace.Name, error) {
	return func() (namespace.Name, error) {
		in, ok := m.incidents[id]
		if !ok {
			return "", fmt.Errorf("%s %s: %w", op, id, incident.ErrNotFound)
		}

		return in.Namespace, nil
	}
}
