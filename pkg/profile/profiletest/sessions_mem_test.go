package profiletest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// TestSessions runs the open session suite against the reference store in
// memory.
func TestSessions(t *testing.T) {
	t.Parallel()

	profiletest.RunSessions(t, func(t *testing.T) profiletest.SessionStore {
		t.Helper()

		m := newMem()

		return profiletest.SessionStore{
			Profiles: m, Sessions: memSessions{m: m}, Namespaces: memNamespaces{m: m}, Audit: memAudit{m: m},
			Records: m.records,
		}
	})
}

// memSessions is the reference store of open sessions over a map.
type memSessions struct{ m *mem }

// sessionKey keys a session by namespace and ID.
func sessionKey(ns namespace.Name, id string) string { return ns.String() + "\x00" + id }

func (s memSessions) Put(
	ctx context.Context, sess profile.Session, f namespace.Fence, es []audit.Entry,
) error {
	if err := checkMemPut(sess, f, es); err != nil {
		return err
	}

	s.m.mu.Lock()
	defer s.m.mu.Unlock()

	if err := insertMemSession(s.m, sess, f); err != nil {
		return err
	}

	// The map has no transaction: the completion runs before the store, so
	// a lost record leaves no session and no entry, as the SQL adapters do
	// in their transaction.
	if err := s.storeMemCompletion(ctx, profile.SessionWritten{Session: sess}); err != nil {
		return err
	}

	key := sessionKey(sess.Namespace, sess.ID)
	s.m.sessions[key] = sess
	s.m.entries[f.Namespace] = append(s.m.entries[f.Namespace], es...)

	return nil
}

// checkMemPut checks the entries, the session and the fence of a Put before
// it writes.
func checkMemPut(sess profile.Session, f namespace.Fence, es []audit.Entry) error {
	if len(es) == 0 {
		return fmt.Errorf("%w: a write needs at least one audit entry", profile.ErrInvalid)
	}

	for n := range es {
		if err := es[n].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if es[n].Namespace != f.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, es[n].Namespace, f.Namespace)
		}
	}

	if err := sess.Valid(); err != nil {
		return err
	}

	if sess.Namespace != f.Namespace {
		return fmt.Errorf("%w: session names %q, fence names %q", profile.ErrInvalid, sess.Namespace, f.Namespace)
	}

	return nil
}

// insertMemSession checks the namespace, the root, the ID and the live limit
// of sess against m. It changes nothing.
func insertMemSession(m *mem, sess profile.Session, f namespace.Fence) error {
	ns, ok := m.namespaces[f.Namespace]
	if !ok {
		return namespace.ErrNotFound
	}

	if ns.FailoverVersion != f.FailoverVersion {
		return namespace.ErrFenced
	}

	if _, exists := m.profiles[sess.Root]; !exists {
		return fmt.Errorf("%w: root profile %s", profile.ErrNotFound, sess.Root)
	}

	key := sessionKey(sess.Namespace, sess.ID)
	if _, exists := m.sessions[key]; exists {
		return fmt.Errorf("%w: session %s is in use", profile.ErrConflict, sess.ID)
	}

	live := 0

	for key := range m.sessions {
		got := m.sessions[key]
		if got.Namespace == sess.Namespace && got.Owner == sess.Owner && got.Live(sess.CreatedAt) {
			live++
		}
	}

	if live >= profile.MaxLiveSessions {
		return profile.ErrSessionLimit
	}

	return nil
}

func (s memSessions) Get(
	_ context.Context, ns namespace.Name, id string, owner profile.SessionOwner, now time.Time,
) (profile.Session, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()

	sess, ok := s.m.sessions[sessionKey(ns, id)]
	if !ok || sess.Owner != owner || !sess.Live(now) {
		return profile.Session{}, profile.ErrNotFound
	}

	return sess, nil
}

func (s memSessions) Delete(
	_ context.Context, ns namespace.Name, id string, owner profile.SessionOwner,
) error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()

	sess, ok := s.m.sessions[sessionKey(ns, id)]
	if !ok || sess.Owner != owner {
		return profile.ErrNotFound
	}

	delete(s.m.sessions, sessionKey(ns, id))

	if !sess.Live(time.Now()) {
		return profile.ErrNotFound
	}

	return nil
}

func (s memSessions) Sweep(_ context.Context, now time.Time, limit int) (int, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: sweep limit must be 1 or more", profile.ErrInvalid)
	}

	s.m.mu.Lock()
	defer s.m.mu.Unlock()

	deleted := 0

	for key := range s.m.sessions {
		if deleted >= limit {
			break
		}

		if !s.m.sessions[key].Live(now) {
			delete(s.m.sessions, key)

			deleted++
		}
	}

	return deleted, nil
}

// storeMemCompletion stores the answer of the Completion that ctx carries.
func (s memSessions) storeMemCompletion(ctx context.Context, w profile.SessionWritten) error {
	c, ok := idempotency.CompletionFrom(ctx)
	if !ok {
		return nil
	}

	return c.Finish(w, func(token string, a idempotency.Answer) error {
		return s.m.records.Complete(ctx, c.Ref, token, a)
	})
}
