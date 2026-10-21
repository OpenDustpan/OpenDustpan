package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/session"
)

// bindOnce binds id to issuer and fails the test on an error.
func bindOnce(t *testing.T, s session.ProviderStore, id, issuer string) string {
	t.Helper()

	recorded, err := s.BindProvider(t.Context(), id, issuer, now())
	if err != nil {
		t.Fatalf("bind %s to %s: %v", id, issuer, err)
	}

	return recorded
}

// testBindProviderInMaintenanceMode covers a start in maintenance mode, which
// refuses writes: a provider ID that has a record binds without a write, so
// the same issuer succeeds. A refused write would fail the call with
// maintenance.ErrInMaintenance.
func testBindProviderInMaintenanceMode(t *testing.T, s Store) {
	t.Helper()

	bs, ok := s.(session.ProviderStore)
	if !ok {
		t.Skip("the store keeps no console sign-in providers")
	}

	if got := bindOnce(t, bs, "corp", "https://idp.example"); got != "https://idp.example" {
		t.Fatalf("first binding = %q, want the issuer", got)
	}

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	if got := bindOnce(t, bs, "corp", "https://idp.example"); got != "https://idp.example" {
		t.Fatalf("binding with the same issuer in maintenance mode = %q, want the issuer", got)
	}
}

// testBindProviderChangedIssuerInMaintenanceMode covers a changed issuer in
// maintenance mode: the call answers the recorded issuer, so that
// session.BindProviders can refuse the change. The record stays as it was,
// also after maintenance mode.
func testBindProviderChangedIssuerInMaintenanceMode(t *testing.T, s Store) {
	t.Helper()

	bs, ok := s.(session.ProviderStore)
	if !ok {
		t.Skip("the store keeps no console sign-in providers")
	}

	bindOnce(t, bs, "corp", "https://idp.example")

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	if got := bindOnce(t, bs, "corp", "https://other.example"); got != "https://idp.example" {
		t.Fatalf("binding with another issuer in maintenance mode = %q, want the recorded issuer", got)
	}

	if err := s.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal: %v", err)
	}

	if got := bindOnce(t, bs, "corp", "https://third.example"); got != "https://idp.example" {
		t.Fatalf("binding after maintenance mode = %q, want the first issuer", got)
	}
}

// testBindProviderNewIDInMaintenanceMode covers a provider ID with no record
// in maintenance mode: the write is refused, the call fails closed with
// maintenance.ErrInMaintenance, and nothing is recorded.
func testBindProviderNewIDInMaintenanceMode(t *testing.T, s Store) {
	t.Helper()

	bs, ok := s.(session.ProviderStore)
	if !ok {
		t.Skip("the store keeps no console sign-in providers")
	}

	if err := s.SetState(t.Context(), maintenance.StateMaintenance); err != nil {
		t.Fatalf("set installation state to maintenance: %v", err)
	}

	_, err := bs.BindProvider(t.Context(), "partner", "https://idp.example", now())
	if !errors.Is(err, maintenance.ErrInMaintenance) {
		t.Fatalf("bind a new provider ID in maintenance mode: err = %v, want ErrInMaintenance", err)
	}

	if err = s.SetState(t.Context(), maintenance.StateNormal); err != nil {
		t.Fatalf("set installation state to normal: %v", err)
	}

	// The refused call recorded nothing: the next binding wins with its issuer.
	if got := bindOnce(t, bs, "partner", "https://other.example"); got != "https://other.example" {
		t.Fatalf("binding after the refused call = %q, want the new issuer", got)
	}
}

// testBindProviderConcurrentStarts covers instances that start at the same
// time and bind one new provider ID with different issuers: every call
// answers the issuer of the one record, and that issuer is one of those
// offered.
func testBindProviderConcurrentStarts(t *testing.T, s Store) {
	t.Helper()

	bs, ok := s.(session.ProviderStore)
	if !ok {
		t.Skip("the store keeps no console sign-in providers")
	}

	const starts = 8

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		answers = make([]string, 0, starts)
		errs    = make([]error, 0, starts)
	)

	offered := make(map[string]bool, starts)
	release := make(chan struct{})

	for i := range starts {
		issuer := fmt.Sprintf("https://idp-%d.example", i)
		offered[issuer] = true

		wg.Go(func() {
			<-release

			got, err := bs.BindProvider(context.WithoutCancel(t.Context()), "corp", issuer, now())

			mu.Lock()
			defer mu.Unlock()

			answers = append(answers, got)
			errs = append(errs, err)
		})
	}

	close(release)
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("a concurrent start failed: %v", err)
		}
	}

	if len(answers) != starts {
		t.Fatalf("answers = %d, want %d", len(answers), starts)
	}

	for _, got := range answers {
		if got != answers[0] || !offered[got] {
			t.Fatalf("answers = %q, want one issuer for every start, one of those offered", answers)
		}
	}

	if got := bindOnce(t, bs, "corp", "https://late.example"); got != answers[0] {
		t.Fatalf("binding after the starts = %q, want the issuer of the record %q", got, answers[0])
	}
}
