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
	groups  map[string]map[string]memberRow
	nextRow int
}

type memberRow struct {
	kind     principal.Kind
	disabled bool
}

// NewMemory returns a fixture over a directory that keeps its groups in
// memory.
func NewMemory(_ *testing.T) Fixture {
	m := &memory{mu: sync.Mutex{}, groups: map[string]map[string]memberRow{}, nextRow: 0}

	return Fixture{
		Directory: m,
		Group: func(_ *testing.T, name string) {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.groups[strings.ToLower(name)] = map[string]memberRow{}
		},
		Member: func(_ *testing.T, group, subject string, kind principal.Kind, disabled bool) string {
			m.mu.Lock()
			defer m.mu.Unlock()

			m.nextRow++
			id := fmt.Sprintf("%s#%d", subject, m.nextRow)
			m.groups[strings.ToLower(group)][id] = memberRow{kind: kind, disabled: disabled}

			return id
		},
		Remove: func(_ *testing.T, group, id string) {
			m.mu.Lock()
			defer m.mu.Unlock()

			delete(m.groups[strings.ToLower(group)], id)
		},
	}
}

// GroupMembers implements gate.ApproverDirectory.
func (m *memory) GroupMembers(_ context.Context, name string) (gate.MemberCounts, error) {
	if err := principal.ValidateGroupName(name); err != nil {
		return gate.MemberCounts{Enabled: 0, Persons: 0}, fmt.Errorf("%w: %w", gate.ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	members, ok := m.groups[strings.ToLower(name)]
	if !ok {
		return gate.MemberCounts{Enabled: 0, Persons: 0}, fmt.Errorf("%w: group %q", gate.ErrNotFound, name)
	}

	counts := gate.MemberCounts{Enabled: 0, Persons: 0}

	for _, row := range members {
		if row.disabled {
			continue
		}

		counts.Enabled++

		if row.kind == principal.KindPerson {
			counts.Persons++
		}
	}

	return counts, nil
}
