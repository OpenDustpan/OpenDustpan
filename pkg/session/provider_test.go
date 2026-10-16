package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

var errProviderStoreDown = errors.New("provider store down")

// memoryProviders keeps the first issuer of each provider ID.
type memoryProviders struct {
	err     error
	issuers map[string]string
}

func (m *memoryProviders) BindProvider(_ context.Context, id, issuer string, _ time.Time) (string, error) {
	if m.err != nil {
		return "", m.err
	}

	if recorded, ok := m.issuers[id]; ok {
		return recorded, nil
	}

	m.issuers[id] = issuer

	return issuer, nil
}

func TestBindProviders(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}
	corp := session.Provider{ID: "corp", Issuer: "https://idp.example"}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("second start with the same issuer: %v", err)
	}

	moved := session.Provider{ID: "corp", Issuer: "https://other.example"}

	err := session.BindProviders(ctx, store, []session.Provider{moved}, time.Now())
	if err == nil || !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("start with another issuer = %v, want ErrIssuerChanged", err)
	}

	want := `sign-in provider "corp" has the issuer "https://idp.example" in the database and the issuer ` +
		`"https://other.example" in the configuration, so set a new provider ID for the new issuer`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	store.err = errProviderStoreDown

	err = session.BindProviders(ctx, store, []session.Provider{corp}, time.Now())
	if !errors.Is(err, errProviderStoreDown) {
		t.Fatalf("start with the store down = %v, want the store error", err)
	}
}
