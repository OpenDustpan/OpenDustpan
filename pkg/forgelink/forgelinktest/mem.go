package forgelinktest

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"opendustpan.org/opendustpan/internal/opsmem"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Mem is the reference adapter of forgelink.Store in memory. The mutex stands for
// the transaction of each write, and opsmem keeps the audit log.
type Mem struct {
	*opsmem.Domain

	mu       sync.Mutex
	links    map[forgelink.Account]forgelink.Link
	requests map[string]forgelink.Request
}

// Entries returns the audit entries of the server scope, in append order.
func (m *Mem) Entries(ctx context.Context) []audit.Entry {
	got, err := m.AuditEntries(ctx, audit.ServerScope)
	if err != nil {
		return nil
	}

	return got
}

// NewMem returns an empty reference store.
func NewMem() *Mem {
	return &Mem{
		Domain:   opsmem.New(),
		mu:       sync.Mutex{},
		links:    map[forgelink.Account]forgelink.Link{},
		requests: map[string]forgelink.Request{},
	}
}

var _ forgelink.Store = (*Mem)(nil)

func serverFence() namespace.Fence { return namespace.Fence{Namespace: "", FailoverVersion: 0} }

// write runs change under the mutex and appends e when change succeeds.
func (m *Mem) write(e audit.Entry, change func() error) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("%w: %w", audit.ErrInvalidEntry, err)
	}

	if e.Namespace != audit.ServerScope {
		return fmt.Errorf("%w: the entry is not in the server scope", audit.ErrInvalidEntry)
	}

	return m.Write(serverFence(), opsmem.Scope(audit.ServerScope), []audit.Entry{e}, func() error {
		m.mu.Lock()
		defer m.mu.Unlock()

		return change()
	})
}

func (m *Mem) CreateRequest(_ context.Context, r forgelink.Request) error {
	if err := r.Validate(); err != nil {
		return err
	}

	if r.IsProved() || !r.UsedAt.IsZero() {
		return forgelink.ErrInvalid
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.requests[r.TokenSHA256]; ok {
		return forgelink.ErrConflict
	}

	open := 0

	for hash, old := range m.requests {
		switch {
		case old.Principal != r.Principal:
		case !old.ExpiresAt.After(r.CreatedAt):
			delete(m.requests, hash)
		case old.UsedAt.IsZero():
			open++
		}
	}

	if open >= forgelink.MaxOpenRequests {
		return forgelink.ErrLimit
	}

	m.requests[r.TokenSHA256] = r

	return nil
}

func (m *Mem) GetRequest(_ context.Context, hash string) (forgelink.Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.requests[hash]
	if !ok {
		return forgelink.Request{}, forgelink.ErrNotFound
	}

	return r, nil
}

func (m *Mem) ProveRequest(
	_ context.Context, hash string, p forgelink.Proof, now time.Time,
) (forgelink.Request, error) {
	if err := p.Validate(); err != nil {
		return forgelink.Request{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	r, ok := m.requests[hash]
	if !ok {
		return forgelink.Request{}, forgelink.ErrNotFound
	}

	if err := r.Check(now); err != nil {
		return forgelink.Request{}, err
	}

	if r.IsProved() {
		if r.Proved.Account != p.Account {
			return forgelink.Request{}, forgelink.ErrConflict
		}

		return r, nil
	}

	r.Proved, r.ProvedAt = p, now
	m.requests[hash] = r

	return r, nil
}

func (m *Mem) AcceptRequest(
	_ context.Context, hash, principal, previous string, now time.Time, e audit.Entry,
) (forgelink.Link, string, error) {
	var (
		link     forgelink.Link
		replaced string
	)

	err := m.write(e, func() error {
		r, ok := m.requests[hash]
		if !ok {
			return forgelink.ErrNotFound
		}

		if err := r.CheckAccept(principal, now); err != nil {
			return err
		}

		current, found := m.links[r.Proved.Account]

		switch {
		case previous == "" && found, previous != "" && (!found || current.Principal != previous):
			return forgelink.ErrConflict
		case found:
			replaced = current.Principal
		}

		link = r.LinkOf(now)
		r.UsedAt = now
		m.requests[hash] = r
		m.links[link.Account] = link

		return nil
	})
	if err != nil {
		return forgelink.Link{}, "", err
	}

	return link, replaced, nil
}

func (m *Mem) DeleteLink(_ context.Context, a forgelink.Account, e audit.Entry) error {
	if err := a.Validate(); err != nil {
		return err
	}

	return m.write(e, func() error {
		if _, ok := m.links[a]; !ok {
			return forgelink.ErrNotFound
		}

		delete(m.links, a)

		return nil
	})
}

func (m *Mem) LinkOf(_ context.Context, a forgelink.Account) (forgelink.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.links[a]
	if !ok {
		return forgelink.Link{}, forgelink.ErrNotFound
	}

	return l, nil
}

func (m *Mem) LinksOf(_ context.Context, principal string) ([]forgelink.Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := []forgelink.Link{}

	for _, l := range m.links {
		if l.Principal == principal {
			out = append(out, l)
		}
	}

	slices.SortFunc(out, func(a, b forgelink.Link) int {
		return cmp.Or(
			cmp.Compare(a.Account.Kind, b.Account.Kind), cmp.Compare(a.Account.Host, b.Account.Host),
			cmp.Compare(a.Account.ID, b.Account.ID),
		)
	})

	return out, nil
}
