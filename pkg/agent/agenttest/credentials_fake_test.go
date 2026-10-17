package agenttest_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/agent"
	"opendustpan.org/opendustpan/pkg/agent/agenttest"
)

func TestCredentialsSuite(t *testing.T) {
	t.Parallel()

	agenttest.RunCredentials(t, func(t *testing.T) agenttest.CredentialsHarness {
		t.Helper()

		f := newFakeCredentials()

		return agenttest.CredentialsHarness{
			Credentials: f,
			Token:       f.harnessCredential(false),
			Session:     f.harnessCredential(false),
			BreakGlass:  f.harnessCredential(true),
			Revoke:      func(_ *testing.T, id string) { f.end(id) },
			Expire:      func(_ *testing.T, id string) { f.end(id) },
			EndSession:  func(_ *testing.T, id string) { f.end(id) },
			Disable:     func(_ *testing.T, subject string) { f.disable(subject) },
		}
	})
}

// fakeCredential is one credential of fakeCredentials.
type fakeCredential struct {
	subject    string
	breakGlass bool
	ended      bool
}

// fakeCredentials is an identity service in memory. A revoke, an expiry and
// a sign-out all end a credential.
type fakeCredentials struct {
	mu       sync.Mutex
	creds    map[string]fakeCredential
	disabled map[string]bool
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{mu: sync.Mutex{}, creds: map[string]fakeCredential{}, disabled: map[string]bool{}}
}

func (f *fakeCredentials) Resolve(_ context.Context, subject, credentialID string) (agent.Resolved, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c, ok := f.creds[credentialID]
	if !ok || c.ended || c.subject != subject || f.disabled[subject] {
		return agent.Resolved{Principal: principal(""), BreakGlass: false}, agent.ErrCredentialEnded
	}

	return agent.Resolved{Principal: principal(subject), BreakGlass: c.breakGlass}, nil
}

// add makes a principal and one credential of it, and returns the subject.
// The ID of the credential is credentialOf(subject).
func (f *fakeCredentials) add(breakGlass bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	subject := fmt.Sprintf("person-%d", len(f.creds)+1)
	f.creds[credentialOf(subject)] = fakeCredential{subject: subject, breakGlass: breakGlass, ended: false}

	return subject
}

func credentialOf(subject string) string { return "cred-of-" + subject }

// harnessCredential makes a credential for the harness.
func (f *fakeCredentials) harnessCredential(breakGlass bool) func(*testing.T) (string, string) {
	return func(*testing.T) (string, string) {
		subject := f.add(breakGlass)

		return subject, credentialOf(subject)
	}
}

func (f *fakeCredentials) end(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := f.creds[id]
	c.ended = true
	f.creds[id] = c
}

func (f *fakeCredentials) disable(subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.disabled[subject] = true
}
