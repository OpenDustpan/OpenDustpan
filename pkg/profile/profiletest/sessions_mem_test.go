package profiletest_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// sessionKey is the key of a session in memory: the namespace and the ID.
type sessionKey struct {
	ns namespace.Name
	id string
}

// memSessions is the reference profile.Sessions over a map. It shares the
// namespaces, the audit entries and the replay records of mem.
type memSessions struct {
	m        *mem
	sessions map[sessionKey]profile.Session
}

func newMemSessions() profiletest.SessionStore {
	m := newMem()

	return profiletest.SessionStore{
		Sessions:   &memSessions{m: m, sessions: map[sessionKey]profile.Session{}},
		Namespaces: memNamespaces{m: m},
		Audit:      memAudit{m: m},
		Records:    m.records,
	}
}

// checkSession applies the rules of a session that Put documents.
func checkSession(s profile.Session, fence namespace.Fence) error {
	switch {
	case !strings.HasPrefix(s.ID, profile.SessionIDPrefix) || s.ID == profile.SessionIDPrefix:
		return fmt.Errorf("%w: the ID of a session starts with %q", profile.ErrInvalid, profile.SessionIDPrefix)
	case len(s.Sealed) == 0:
		return fmt.Errorf("%w: a session holds a sealed value", profile.ErrInvalid)
	case !s.ExpiresAt.After(s.CreatedAt):
		return fmt.Errorf("%w: a session ends after it starts", profile.ErrInvalid)
	case s.Owner.ID == "" || (s.Owner.Kind != profile.OwnerPrincipal && s.Owner.Kind != profile.OwnerJob):
		return fmt.Errorf("%w: a session has an owner", profile.ErrInvalid)
	case s.Namespace != fence.Namespace:
		return fmt.Errorf("%w: the session names %q, the fence %q", profile.ErrInvalid, s.Namespace, fence.Namespace)
	}

	return nil
}

func (x *memSessions) Put(
	ctx context.Context, s profile.Session, fence namespace.Fence, es []audit.Entry,
) error {
	x.m.mu.Lock()
	defer x.m.mu.Unlock()

	if err := x.m.begin(fence, es); err != nil {
		return err
	}

	if err := checkSession(s, fence); err != nil {
		return err
	}

	if _, ok := x.sessions[sessionKey{s.Namespace, s.ID}]; ok {
		return fmt.Errorf("%w: the session %q exists", profile.ErrConflict, s.ID)
	}

	live := 0

	for _, o := range x.sessions {
		if o.Namespace == s.Namespace && o.Owner == s.Owner && o.Live(s.CreatedAt) {
			live++
		}
	}

	if live >= profile.MaxLiveSessions {
		return profile.ErrSessionLimit
	}

	if c, ok := idempotency.CompletionFrom(ctx); ok {
		err := c.Finish(profile.SessionWritten{Session: s}, func(token string, a idempotency.Answer) error {
			return x.m.records.Complete(ctx, c.Ref, token, a)
		})
		if err != nil {
			return err
		}
	}

	x.sessions[sessionKey{s.Namespace, s.ID}] = s
	x.m.entries[fence.Namespace] = append(x.m.entries[fence.Namespace], es...)

	return nil
}

func (x *memSessions) Get(
	_ context.Context, ns namespace.Name, id string, owner profile.SessionOwner, now time.Time,
) (profile.Session, error) {
	x.m.mu.Lock()
	defer x.m.mu.Unlock()

	s, ok := x.sessions[sessionKey{ns, id}]
	if !ok || s.Owner != owner || !s.Live(now) {
		return profile.Session{}, profile.ErrNotFound
	}

	return s, nil
}

func (x *memSessions) Delete(_ context.Context, ns namespace.Name, id string, owner profile.SessionOwner) error {
	x.m.mu.Lock()
	defer x.m.mu.Unlock()

	s, ok := x.sessions[sessionKey{ns, id}]
	if !ok || s.Owner != owner {
		return profile.ErrNotFound
	}

	delete(x.sessions, sessionKey{ns, id})

	if !s.Live(time.Now()) {
		return profile.ErrNotFound
	}

	return nil
}

func (x *memSessions) Sweep(_ context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: limit must be 1 or more", profile.ErrInvalid)
	}

	x.m.mu.Lock()
	defer x.m.mu.Unlock()

	n := 0

	for k, s := range x.sessions {
		if n == limit {
			break
		}

		if !s.Live(now) {
			delete(x.sessions, k)

			n++
		}
	}

	return n, nil
}
