package chatopstest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/chatops"
	"opendustpan.org/opendustpan/pkg/chatops/chatopstest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// TestMemConformance runs the suite against the reference adapter in
// memory, so that the cases of the suite run before the first SQL adapter.
func TestMemConformance(t *testing.T) {
	t.Parallel()

	chatopstest.Run(t, func(*testing.T) chatopstest.Harness {
		db := &memStore{Domain: opsmem.New(), links: map[userKey]chatops.Link{}, intents: map[string]chatops.Intent{}}

		return chatopstest.Harness{Links: db, Intents: db, Domain: db}
	})
}

type userKey struct{ platform, workspace, user string }

// memStore is the reference adapter of chatops.Links and chatops.Intents
// in memory.
type memStore struct {
	*opsmem.Domain

	links   map[userKey]chatops.Link
	intents map[string]chatops.Intent
}

func noFence() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

func (m *memStore) PutLink(_ context.Context, l chatops.Link, e audit.Entry) error {
	if l.Platform == "" || l.Workspace == "" || l.User == "" || l.Principal == "" {
		return fmt.Errorf("put link: %w", chatops.ErrInvalid)
	}

	return m.Write(noFence(), []audit.Entry{e}, func() error {
		m.links[userKey{l.Platform, l.Workspace, l.User}] = l

		return nil
	})
}

func (m *memStore) DeleteLink(_ context.Context, platform, workspace, user string, e audit.Entry) error {
	return m.Write(noFence(), []audit.Entry{e}, func() error {
		k := userKey{platform, workspace, user}
		if _, ok := m.links[k]; !ok {
			return fmt.Errorf("delete link: %w", chatops.ErrNotFound)
		}

		delete(m.links, k)

		return nil
	})
}

func (m *memStore) LinkOf(_ context.Context, platform, workspace, user string) (chatops.Link, error) {
	var (
		l  chatops.Link
		ok bool
	)

	m.Read(func() { l, ok = m.links[userKey{platform, workspace, user}] })

	if !ok {
		return l, fmt.Errorf("link of %s: %w", user, chatops.ErrNotFound)
	}

	return l, nil
}

func (m *memStore) LinksOf(_ context.Context, principal string) ([]chatops.Link, error) {
	var out []chatops.Link

	m.Read(func() {
		for _, l := range m.links {
			if l.Principal == principal {
				out = append(out, l)
			}
		}
	})

	return out, nil
}

func (m *memStore) CreateIntent(_ context.Context, in chatops.Intent) error {
	if in.ID == "" || in.Principal == "" {
		return fmt.Errorf("create intent: %w", chatops.ErrInvalid)
	}

	return m.Write(noFence(), nil, func() error {
		m.intents[in.ID] = in

		return nil
	})
}

func (m *memStore) GetIntent(_ context.Context, id string) (chatops.Intent, error) {
	var (
		in chatops.Intent
		ok bool
	)

	m.Read(func() { in, ok = m.intents[id] })

	if !ok {
		return in, fmt.Errorf("intent %s: %w", id, chatops.ErrNotFound)
	}

	return in, nil
}

func (m *memStore) UseIntent(_ context.Context, id string, now time.Time) error {
	return m.Write(noFence(), nil, func() error {
		in, ok := m.intents[id]

		switch {
		case !ok:
			return fmt.Errorf("use intent %s: %w", id, chatops.ErrNotFound)
		case !in.UsedAt.IsZero():
			return fmt.Errorf("use intent %s: %w", id, chatops.ErrIntentUsed)
		case !now.Before(in.ExpiresAt):
			return fmt.Errorf("use intent %s: %w", id, chatops.ErrIntentExpired)
		}

		in.UsedAt = now
		m.intents[id] = in

		return nil
	})
}
