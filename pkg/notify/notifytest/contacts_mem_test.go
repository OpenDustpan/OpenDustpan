package notifytest_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/notifytest"
)

// TestMemContactsConformance runs the suite of notify.Contacts against the
// reference adapter in memory, so that its cases run before the identity
// package implements the port.
func TestMemContactsConformance(t *testing.T) {
	t.Parallel()

	notifytest.RunContacts(t, func(*testing.T) notifytest.ContactsHarness {
		p := &memPeople{
			mu:       sync.Mutex{},
			contacts: map[string][]notify.Contact{},
			disabled: map[string]bool{},
			groups:   map[string][]string{},
			grants:   map[grantKey]bool{},
		}

		return notifytest.ContactsHarness{Contacts: p, People: p}
	})
}

// errExists reports a person that exists.
var errExists = errors.New("the person exists")

type grantKey struct {
	id, action string
	scope      namespace.Name
}

// memPeople is the reference adapter of notify.Contacts in memory.
type memPeople struct {
	mu       sync.Mutex
	contacts map[string][]notify.Contact
	disabled map[string]bool
	groups   map[string][]string
	grants   map[grantKey]bool
}

func (m *memPeople) AddPerson(_ context.Context, id string, cs []notify.Contact) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.contacts[id]; ok {
		return fmt.Errorf("add person %s: %w", id, errExists)
	}

	m.contacts[id] = slices.Clone(cs)

	return nil
}

func (m *memPeople) Disable(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.disabled[id] = true

	return nil
}

func (m *memPeople) AddGroup(_ context.Context, name string, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.groups[name] = slices.Clone(ids)

	return nil
}

func (m *memPeople) Grant(_ context.Context, id, action string, scope namespace.Name) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.grants[grantKey{id: id, action: action, scope: scope}] = true

	return nil
}

func (m *memPeople) ContactsOf(_ context.Context, id string) ([]notify.Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.disabled[id] {
		return nil, nil
	}

	return slices.Clone(m.contacts[id]), nil
}

func (m *memPeople) Members(_ context.Context, name string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.groups[name]), nil
}

func (m *memPeople) Admins(_ context.Context, scope namespace.Name) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	action := notify.ActionManage
	if scope == audit.ServerScope {
		action = notify.ActionManageServer
	}

	var out []string

	for _, id := range slices.Sorted(maps.Keys(m.contacts)) {
		if m.grants[grantKey{id: id, action: action, scope: scope}] {
			out = append(out, id)
		}
	}

	return out, nil
}
