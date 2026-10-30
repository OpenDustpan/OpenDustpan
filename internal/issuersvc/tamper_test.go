package issuersvc_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/secret"
)

// tamperStore is a store whose rows a test changes after the store reads
// them, as a person with write access to the database could.
type tamperStore struct {
	profile.IssuerStore

	live func(ns namespace.Name, keys []profile.Key) []profile.Key
}

func (s *tamperStore) Live(ctx context.Context, ns namespace.Name) ([]profile.Key, error) {
	keys, err := s.IssuerStore.Live(ctx, ns)
	if err != nil || s.live == nil {
		return keys, err
	}

	return s.live(ns, slices.Clone(keys)), nil
}

func withTamper(ts *tamperStore) options {
	return func(c *issuerConfig, h *harness) {
		ts.IssuerStore = h.store.Issuer()
		c.Store = ts
	}
}

func replaceState(keys []profile.Key, st profile.KeyState, change func(k *profile.Key)) []profile.Key {
	for i := range keys {
		if keys[i].State == st {
			change(&keys[i])
		}
	}

	return keys
}

func TestASealedKeyOfAnotherNamespaceNeverSigns(t *testing.T) {
	t.Parallel()

	ts := &tamperStore{}
	h := newHarness(t, withTamper(ts))
	other := h.addNamespace("globex")

	if _, err := h.svc.Login(t.Context(), h.open(other, "namespace:globex:access:write"), "aws", "x", 5*time.Minute); err != nil {
		t.Fatalf("Login in globex: %v", err)
	}

	h.createKeys()

	foreign, _ := h.store.Issuer().Live(t.Context(), other.Name)

	var stolen []byte

	for _, k := range foreign {
		if k.State == profile.KeyActive {
			stolen = k.Sealed
		}
	}

	ts.live = func(ns namespace.Name, keys []profile.Key) []profile.Key {
		if ns != h.ns.Name {
			return keys
		}

		return replaceState(keys, profile.KeyActive, func(k *profile.Key) { k.Sealed = stolen })
	}

	tok, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with a sealed key of another namespace", err, secret.ErrDecrypt)

	if tok.Value != "" {
		t.Error("a token was signed with a key of another namespace")
	}
}

func TestASealedKeyOfAnotherRowNeverSignsUnderThisKid(t *testing.T) {
	t.Parallel()

	ts := &tamperStore{}
	h := newHarness(t, withTamper(ts))
	h.createKeys()

	var nextSealed, nextPublic []byte

	for _, k := range h.keysOf(h.ns) {
		if k.State == profile.KeyNext {
			nextSealed, nextPublic = k.Sealed, k.Public
		}
	}

	for name, change := range map[string]func(k *profile.Key){
		"the sealed key of the next key": func(k *profile.Key) { k.Sealed = nextSealed },
		"the public key of the next key": func(k *profile.Key) { k.Public = nextPublic },
		"a garbled sealed key":           func(k *profile.Key) { k.Sealed = append([]byte("acme/issuer|"), 1, 2, 3) },
		"an empty sealed key":            func(k *profile.Key) { k.Sealed = nil },
		"a garbled public key":           func(k *profile.Key) { k.Public = []byte("not a key") },
	} {
		ts.live = func(_ namespace.Name, keys []profile.Key) []profile.Key {
			return replaceState(keys, profile.KeyActive, change)
		}

		tok, err := h.login("sts.amazonaws.com", 5*time.Minute)
		if err == nil || tok.Value != "" {
			t.Errorf("%s: Login = %v, %q, want a refusal and no token", name, err, tok.Value)
		}
	}
}

func TestARetiredOrDeletedKeyNeverSigns(t *testing.T) {
	t.Parallel()

	ts := &tamperStore{}
	h := newHarness(t, withTamper(ts))
	h.createKeys()

	// Every key of the namespace reads as retired: nothing may sign.
	ts.live = func(_ namespace.Name, keys []profile.Key) []profile.Key {
		for i := range keys {
			keys[i].State = profile.KeyRetired
		}

		return keys
	}

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with no active key", err, profile.ErrIssuerKeyMissing)

	// The next key never signs.
	ts.live = func(_ namespace.Name, keys []profile.Key) []profile.Key {
		return slices.DeleteFunc(keys, func(k profile.Key) bool { return k.State == profile.KeyActive })
	}

	_, err = h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with a next key only", err, profile.ErrIssuerKeyMissing)
}
