package chatopstest_test

import (
	"context"
	"errors"
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
		db := &memStore{
			Domain:   opsmem.New(),
			links:    map[chatops.ChatUser]chatops.Link{},
			requests: map[string]chatops.LinkRequest{},
			intents:  map[string]chatops.Intent{},
		}

		return chatopstest.Harness{Links: db, LinkRequests: db, Intents: db, Domain: db}
	})
}

// memStore is the reference adapter of chatops.Links,
// chatops.LinkRequests and chatops.Intents in memory.
type memStore struct {
	*opsmem.Domain

	links    map[chatops.ChatUser]chatops.Link
	requests map[string]chatops.LinkRequest
	intents  map[string]chatops.Intent
}

// scopeErr translates a fence that does not belong to the row into want.
func scopeErr(op string, err, want error) error {
	if errors.Is(err, opsmem.ErrScope) {
		return fmt.Errorf("%s: %w", op, want)
	}

	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return nil
}

func server() func() (namespace.Name, error) { return opsmem.Scope(audit.ServerScope) }

func (m *memStore) PutLink(_ context.Context, fence namespace.Fence, l chatops.Link, previous string,
	e audit.Entry,
) error {
	if err := l.Validate(); err != nil {
		return fmt.Errorf("put link: %w", err)
	}

	err := m.Write(fence, server(), []audit.Entry{e}, func() error {
		k := l.ChatUser()
		old, ok := m.links[k]

		if (previous == "" && ok) || (previous != "" && (!ok || old.Principal != previous)) {
			return fmt.Errorf("put link: %w", chatops.ErrConflict)
		}

		m.links[k] = l

		return nil
	})

	return scopeErr("put link", err, chatops.ErrInvalid)
}

func (m *memStore) DeleteLink(_ context.Context, fence namespace.Fence, u chatops.ChatUser, e audit.Entry) error {
	err := m.Write(fence, server(), []audit.Entry{e}, func() error {
		k := u
		if _, ok := m.links[k]; !ok {
			return fmt.Errorf("delete link: %w", chatops.ErrNotFound)
		}

		delete(m.links, k)

		return nil
	})

	return scopeErr("delete link", err, chatops.ErrInvalid)
}

func (m *memStore) LinkOf(_ context.Context, u chatops.ChatUser) (chatops.Link, error) {
	var (
		l  chatops.Link
		ok bool
	)

	m.Read(func() { l, ok = m.links[u] })

	if !ok {
		return l, fmt.Errorf("link of %s: %w", u.User, chatops.ErrNotFound)
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

func (m *memStore) CreateLinkRequest(_ context.Context, fence namespace.Fence, r chatops.LinkRequest) error {
	if err := r.Validate(); err != nil {
		return fmt.Errorf("create link request: %w", err)
	}

	if !r.UsedAt.IsZero() || r.UsedBy != "" {
		return fmt.Errorf("create link request: %w: a new request has no use", chatops.ErrInvalid)
	}

	err := m.Write(fence, server(), nil, func() error {
		if _, ok := m.requests[r.TokenSHA256]; ok {
			return fmt.Errorf("create link request: %w", chatops.ErrConflict)
		}

		m.requests[r.TokenSHA256] = r

		return nil
	})

	return scopeErr("create link request", err, chatops.ErrInvalid)
}

func (m *memStore) GetLinkRequest(_ context.Context, tokenSHA256 string) (chatops.LinkRequest, error) {
	var (
		r  chatops.LinkRequest
		ok bool
	)

	m.Read(func() { r, ok = m.requests[tokenSHA256] })

	if !ok {
		return r, fmt.Errorf("link request: %w", chatops.ErrNotFound)
	}

	return r, nil
}

func (m *memStore) UseLinkRequest(_ context.Context, fence namespace.Fence, tokenSHA256, principal string,
	now time.Time,
) (chatops.LinkRequest, error) {
	if principal == "" {
		return chatops.LinkRequest{}, fmt.Errorf("use link request: %w: no principal", chatops.ErrInvalid)
	}

	var out chatops.LinkRequest

	err := m.Write(fence, server(), nil, func() error {
		r, ok := m.requests[tokenSHA256]

		switch {
		case !ok:
			return chatops.ErrNotFound
		case !r.UsedAt.IsZero():
			return chatops.ErrUsed
		case !now.Before(r.ExpiresAt):
			return chatops.ErrExpired
		}

		r.UsedAt, r.UsedBy = now, principal
		m.requests[tokenSHA256] = r
		out = r

		return nil
	})
	if err != nil {
		return chatops.LinkRequest{}, scopeErr("use link request", err, chatops.ErrInvalid)
	}

	return out, nil
}

func (m *memStore) CreateIntent(_ context.Context, fence namespace.Fence, in chatops.Intent, e audit.Entry) error {
	if err := in.Validate(); err != nil {
		return fmt.Errorf("create intent: %w", err)
	}

	if !in.UsedAt.IsZero() {
		return fmt.Errorf("create intent: %w: a new intent has no use", chatops.ErrInvalid)
	}

	err := m.Write(fence, opsmem.Scope(in.Namespace), []audit.Entry{e}, func() error {
		if _, ok := m.intents[in.ID]; ok {
			return fmt.Errorf("create intent %s: %w", in.ID, chatops.ErrConflict)
		}

		m.intents[in.ID] = in

		return nil
	})

	return scopeErr("create intent", err, chatops.ErrInvalid)
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

func (m *memStore) UseIntent(_ context.Context, fence namespace.Fence, id string, now time.Time) error {
	scope := func() (namespace.Name, error) {
		in, ok := m.intents[id]
		if !ok {
			return "", chatops.ErrNotFound
		}

		return in.Namespace, nil
	}

	err := m.Write(fence, scope, nil, func() error {
		in := m.intents[id]

		switch {
		case !in.UsedAt.IsZero():
			return chatops.ErrUsed
		case !now.Before(in.ExpiresAt):
			return chatops.ErrExpired
		}

		in.UsedAt = now
		m.intents[id] = in

		return nil
	})

	return scopeErr("use intent "+id, err, chatops.ErrNotFound)
}
