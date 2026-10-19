package profiletest_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// TestMemoryStore runs the suite against the reference store in memory.
func TestMemoryStore(t *testing.T) {
	t.Parallel()

	profiletest.Run(t, func(t *testing.T) profiletest.Store {
		t.Helper()

		m := newMem()

		return profiletest.Store{Profiles: m, Namespaces: memNamespaces{m: m}, Audit: memAudit{m: m}}
	})
}

// memProfile is a profile with its revisions. A deleted profile keeps them.
type memProfile struct {
	profile.Profile

	revisions []profile.Revision
	deleted   bool
}

// mem is the reference store: a profile store, a namespace store and an audit
// store over maps.
type mem struct {
	mu         sync.Mutex
	namespaces map[namespace.Name]namespace.Namespace
	profiles   map[profile.ID]*memProfile
	entries    map[namespace.Name][]audit.Entry
}

func newMem() *mem {
	return &mem{
		mu:         sync.Mutex{},
		namespaces: map[namespace.Name]namespace.Namespace{},
		profiles:   map[profile.ID]*memProfile{},
		entries:    map[namespace.Name][]audit.Entry{},
	}
}

type memNamespaces struct{ m *mem }

func (n memNamespaces) CreateNamespace(_ context.Context, ns namespace.Namespace) error {
	n.m.mu.Lock()
	defer n.m.mu.Unlock()

	n.m.namespaces[ns.Name] = ns

	return nil
}

func (n memNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	n.m.mu.Lock()
	defer n.m.mu.Unlock()

	ns, ok := n.m.namespaces[name]
	if !ok {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return ns, nil
}

func (memNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (memNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (memNamespaces) Ping(context.Context) error { return nil }

type memAudit struct{ m *mem }

func (a memAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()

	a.m.entries[e.Namespace] = append(a.m.entries[e.Namespace], e)

	return e, nil
}

func (a memAudit) List(_ context.Context, ns namespace.Name, _ audit.Page) (audit.Listing, error) {
	a.m.mu.Lock()
	defer a.m.mu.Unlock()

	es := slices.Clone(a.m.entries[ns])
	slices.Reverse(es)

	return audit.Listing{Entries: es, Total: int64(len(es))}, nil
}

func (memAudit) Check(context.Context, namespace.Name) (audit.CheckResult, error) {
	return audit.CheckResult{Checked: 0, BadSequence: 0, Unchained: 0, Valid: true}, nil
}

func (m *mem) Create(
	_ context.Context, p profile.Profile, r profile.Revision, f namespace.Fence, e audit.Entry,
) (profile.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, e); err != nil {
		return profile.Profile{}, err
	}

	if r.Number != 1 || r.Profile != p.ID || p.Latest != 1 {
		return profile.Profile{}, profile.ErrInvalid
	}

	count := 0

	for _, q := range m.profiles {
		if q.Namespace != p.Namespace || q.deleted {
			continue
		}

		count++

		if q.Name == p.Name {
			return profile.Profile{}, profile.ErrConflict
		}
	}

	if count >= profile.MaxProfiles {
		return profile.Profile{}, profile.ErrLimit
	}

	m.profiles[p.ID] = &memProfile{Profile: p, revisions: []profile.Revision{r}, deleted: false}
	m.entries[e.Namespace] = append(m.entries[e.Namespace], e)

	return p, nil
}

func (m *mem) Get(_ context.Context, ns namespace.Name, ref profile.Ref) (profile.Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, p := range m.profiles {
		byID := ref.ID != "" && p.ID == ref.ID
		byName := ref.Name != "" && p.Name == ref.Name

		if p.Namespace == ns && !p.deleted && (byID || byName) {
			return p.Profile, nil
		}
	}

	return profile.Profile{}, profile.ErrNotFound
}

func (m *mem) List(_ context.Context, q profile.Filter) (profile.Page, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if q.Limit < 1 {
		return profile.Page{}, profile.ErrInvalid
	}

	var all []profile.Profile

	for _, p := range m.profiles {
		inFolder := q.Folder == "" || p.Name.Folder() == q.Folder

		if p.Namespace == q.Namespace && !p.deleted && p.Name > q.After && inFolder {
			all = append(all, p.Profile)
		}
	}

	slices.SortFunc(all, func(a, b profile.Profile) int { return strings.Compare(string(a.Name), string(b.Name)) })

	if len(all) <= q.Limit {
		return profile.Page{Profiles: all, Next: ""}, nil
	}

	return profile.Page{Profiles: all[:q.Limit], Next: all[q.Limit-1].Name}, nil
}

func (m *mem) Revision(_ context.Context, ns namespace.Name, id profile.ID, number int) (profile.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if number < 0 {
		return profile.Revision{}, profile.ErrInvalid
	}

	p, err := m.live(ns, id)
	if err != nil {
		return profile.Revision{}, err
	}

	if number == 0 {
		number = len(p.revisions)
	}

	if number > len(p.revisions) {
		return profile.Revision{}, profile.ErrNotFound
	}

	return p.revisions[number-1], nil
}

func (m *mem) Revisions(
	_ context.Context, ns namespace.Name, id profile.ID, before, limit int,
) ([]profile.Revision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if limit < 1 {
		return nil, profile.ErrInvalid
	}

	p, err := m.live(ns, id)
	if err != nil {
		return nil, err
	}

	var out []profile.Revision

	for i := len(p.revisions) - 1; i >= 0 && len(out) < limit; i-- {
		if before == 0 || p.revisions[i].Number < before {
			out = append(out, p.revisions[i])
		}
	}

	return out, nil
}

func (m *mem) Save(
	_ context.Context, at profile.Anchor, r profile.Revision, f namespace.Fence, e audit.Entry,
) (profile.Profile, error) {
	ns, id, base := at.Namespace, at.ID, at.Latest

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, e); err != nil {
		return profile.Profile{}, err
	}

	p, err := m.live(ns, id)
	if err != nil {
		return profile.Profile{}, err
	}

	if p.Latest != base {
		return profile.Profile{}, profile.ErrConflict
	}

	if r.Number != base+1 || r.Profile != id {
		return profile.Profile{}, profile.ErrInvalid
	}

	p.revisions = append(p.revisions, r)
	p.Latest = r.Number
	p.UpdatedAt = r.CreatedAt
	m.entries[e.Namespace] = append(m.entries[e.Namespace], e)

	return p.Profile, nil
}

func (m *mem) Delete(_ context.Context, at profile.Anchor, f namespace.Fence, e audit.Entry) error {
	ns, id, ifLatest := at.Namespace, at.ID, at.Latest

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.begin(f, e); err != nil {
		return err
	}

	p, err := m.live(ns, id)
	if err != nil {
		return err
	}

	if ifLatest > 0 && p.Latest != ifLatest {
		return profile.ErrConflict
	}

	p.deleted = true
	m.entries[e.Namespace] = append(m.entries[e.Namespace], e)

	return nil
}

// epochTime is a fixed time for the fields that a case does not read.
func epochTime() time.Time { return time.UnixMilli(1_700_000_000_000).UTC() }

// begin checks the fence and the entry, as the SQL stores do in a transaction.
func (m *mem) begin(f namespace.Fence, e audit.Entry) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("audit entry: %w", err)
	}

	if e.Namespace != f.Namespace {
		return fmt.Errorf("%w: entry names %q", audit.ErrInvalidEntry, e.Namespace)
	}

	ns, ok := m.namespaces[f.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != f.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

func (m *mem) live(ns namespace.Name, id profile.ID) (*memProfile, error) {
	p, ok := m.profiles[id]
	if !ok || p.deleted || p.Namespace != ns {
		return nil, profile.ErrNotFound
	}

	return p, nil
}
