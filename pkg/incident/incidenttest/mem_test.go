package incidenttest_test

import (
	"cmp"
	"context"
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

func (m *memStore) Create(_ context.Context, in incident.Incident, fence namespace.Fence, e audit.Entry) error {
	if err := in.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.incidents[in.ID]; ok {
			return fmt.Errorf("create %s: %w", in.ID, incident.ErrExists)
		}

		in.Version = 1
		m.incidents[in.ID] = in

		return nil
	})
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

	return in, nil
}

func (m *memStore) Update(_ context.Context, in incident.Incident, fence namespace.Fence, e audit.Entry) error {
	if err := in.Validate(); err != nil {
		return err
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		old, ok := m.incidents[in.ID]

		switch {
		case !ok:
			return fmt.Errorf("update %s: %w", in.ID, incident.ErrNotFound)
		case old.Version != in.Version:
			return fmt.Errorf("update %s: %w", in.ID, incident.ErrConflict)
		case old.State == incident.StateClosed:
			return fmt.Errorf("update %s: %w: the incident is closed", in.ID, incident.ErrTransition)
		}

		in.Version++
		m.incidents[in.ID] = in

		return nil
	})
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
	if strings.TrimSpace(n.Text) == "" || len(n.Text) > incident.MaxNoteLen {
		return fmt.Errorf("add note: %w: a note has 1 to %d bytes", incident.ErrInvalid, incident.MaxNoteLen)
	}

	return m.Write(fence, []audit.Entry{e}, func() error {
		if _, ok := m.incidents[n.Incident]; !ok {
			return fmt.Errorf("add note to %s: %w", n.Incident, incident.ErrNotFound)
		}

		m.notes = append(m.notes, n)

		return nil
	})
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
