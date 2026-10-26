package approverdirectorytest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/principal"
)

// memory is the reference adapter: a directory over maps. It shows the
// contract in the smallest code, and the suite runs on it too, so a case that
// no adapter can meet shows up here first.
type memory struct {
	mu      sync.Mutex
	groups  map[string]*memoryGroup
	nextRow int
}

type memoryGroup struct {
	name    string
	members map[string]memberRow
}

type memberRow struct {
	kind     principal.Kind
	disabled bool
}

// NewMemory returns a fixture over a directory that keeps its groups in
// memory.
func NewMemory(_ *testing.T) Fixture {
	m := &memory{mu: sync.Mutex{}, groups: map[string]*memoryGroup{}, nextRow: 0}

	return Fixture{
		Directory: m,
		Group: func(_ *testing.T, name string) string {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.nextRow++
			id := fmt.Sprintf("group-%d", m.nextRow)
			m.groups[id] = &memoryGroup{name: name, members: map[string]memberRow{}}

			return id
		},
		Member: func(_ *testing.T, group, subject string, kind principal.Kind, disabled bool) string {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.nextRow++
			id := fmt.Sprintf("%s#%d", subject, m.nextRow)
			m.byName(group).members[id] = memberRow{kind: kind, disabled: disabled}

			return id
		},
		Remove: func(_ *testing.T, group, id string) {
			m.mu.Lock()
			defer m.mu.Unlock()

			delete(m.byName(group).members, id)
		},
		Delete: func(_ *testing.T, id string) {
			m.mu.Lock()
			defer m.mu.Unlock()

			delete(m.groups, id)
		},
		Rename: func(_ *testing.T, id, name string) {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.groups[id].name = name
		},
	}
}

// ResolveGroup implements gate.ApproverDirectory.
func (m *memory) ResolveGroup(_ context.Context, name string) (gate.GroupRef, error) {
	none := gate.GroupRef{ID: "", Name: ""}

	if err := principal.ValidateGroupName(name); err != nil {
		return none, fmt.Errorf("%w: %w", gate.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, g := range m.groups {
		if strings.EqualFold(g.name, name) {
			return gate.GroupRef{ID: id, Name: g.name}, nil
		}
	}

	return none, fmt.Errorf("%w: group %q", gate.ErrNotFound, name)
}

// GroupByID implements gate.ApproverDirectory.
func (m *memory) GroupByID(_ context.Context, id string) (gate.GroupRef, gate.MemberCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	group, ok := m.groups[id]
	if !ok {
		return gate.GroupRef{ID: "", Name: ""}, gate.MemberCounts{Enabled: 0, Persons: 0},
			fmt.Errorf("%w: group %q", gate.ErrNotFound, id)
	}

	counts := gate.MemberCounts{Enabled: 0, Persons: 0}

	for _, row := range group.members {
		if row.disabled {
			continue
		}

		counts.Enabled++

		if row.kind == principal.KindPerson {
			counts.Persons++
		}
	}

	return gate.GroupRef{ID: id, Name: group.name}, counts, nil
}

// byName returns the group called name without regard to case. The caller
// holds the lock and names a group that exists.
func (m *memory) byName(name string) *memoryGroup {
	for _, g := range m.groups {
		if strings.EqualFold(g.name, name) {
			return g
		}
	}

	return nil
}
