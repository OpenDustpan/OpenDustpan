package session_test

import (
	"context"
	"errors"
	"strings"
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
	if !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("start with another issuer = %v, want ErrIssuerChanged", err)
	}

	for _, part := range []string{`"corp"`, `"https://idp.example"`, `"https://other.example"`, "new provider ID"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q does not name %s", err, part)
		}
	}

	store.err = errProviderStoreDown

	err = session.BindProviders(ctx, store, []session.Provider{corp}, time.Now())
	if !errors.Is(err, errProviderStoreDown) {
		t.Fatalf("start with the store down = %v, want the store error", err)
	}
}
