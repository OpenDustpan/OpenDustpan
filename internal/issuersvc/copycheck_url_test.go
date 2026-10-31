package issuersvc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuerfetch"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/profile"
)

// hookFetcher calls before once, at the first read of a document, and then
// reads through next. It plays a PATCH of the issuerURL that comes while a
// copy check is in flight.
type hookFetcher struct {
	next   issuersvc.Fetcher
	before func()
	done   bool
}

func (f *hookFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	if !f.done {
		f.done = true
		f.before()
	}

	body, err := f.next.Fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	return body, nil
}

func TestACheckOfOneURLHoldsNothingForAnotherURL(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()
	hs.copyFiles(t, h.export())

	if c := h.checkCopy(); !c.OK || c.URL != hs.url() {
		t.Fatalf("copy check = %+v, want OK for %s", c, hs.url())
	}

	h.mustLogin()

	// The new address has no file: the host answers 404 there.
	other := hs.srv.URL + "/other"
	set := h.settings()

	if _, err := h.patch(h.admin, patchURL(other), set.Version, okFresh); err != nil {
		t.Fatalf("patch: %v", err)
	}

	after := h.settings()
	if after.CopyCheck != nil {
		t.Errorf("the PATCH of issuerURL left the copy check %+v", after.CopyCheck)
	}

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after the URL changed, before a new check", err, profile.ErrIssuerKeyMissing)

	// A check that cannot read the new address must not bring back the
	// verified keys of the old address.
	c := h.checkCopy()
	if c.OK || len(c.Verified) != 0 || c.URL != other {
		t.Errorf("copy check of the unreadable address = %+v, want a failed check of %s with no key", c, other)
	}

	_, err = h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after a check of an unreadable address", err, profile.ErrIssuerKeyMissing)
}

func TestACopyCheckThatStartedBeforeAPatchIsNotStored(t *testing.T) {
	t.Parallel()

	hs := newHost(t)
	hook := &hookFetcher{
		next:   issuerfetch.New(guardedClient(hs, egress.Rules{Allow: nil, LoopbackForTests: true})),
		before: nil,
		done:   false,
	}

	h := newHarness(t, func(c *issuerConfig, _ *harness) { c.Fetcher = hook })
	h.setIssuerURL(hs.url())
	h.createKeys()
	hs.copyFiles(t, h.export())

	other := hs.srv.URL + "/other"
	hook.before = func() {
		if _, err := h.patch(h.admin, patchURL(other), h.settings().Version, okFresh); err != nil {
			t.Errorf("patch during the check: %v", err)
		}
	}

	_, err := h.svc.CheckCopy(t.Context(), h.admin, h.ns.Name)
	wantErr(t, "CheckCopy that a PATCH overtook", err, profile.ErrConflict)

	set := h.settings()
	if set.IssuerURL != other || set.CopyCheck != nil {
		t.Errorf("settings = url %q, copy check %+v; want %q and no check", set.IssuerURL, set.CopyCheck, other)
	}

	_, err = h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after a check of the old address", err, profile.ErrIssuerKeyMissing)
}

func TestACopyThatHoldsTheKidButNotTheKeyFails(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(jwks *profile.JWKS)
	}{
		{"a wrong modulus", func(j *profile.JWKS) { j.Keys[0].N = j.Keys[1].N }},
		{"a wrong exponent", func(j *profile.JWKS) { j.Keys[0].E = "AQAC" }},
		{"a wrong algorithm", func(j *profile.JWKS) { j.Keys[0].Alg = "RS512" }},
		{"a wrong key type", func(j *profile.JWKS) { j.Keys[0].Kty = "EC" }},
		{"a wrong use", func(j *profile.JWKS) { j.Keys[0].Use = "enc" }},
		{"the same kid twice", func(j *profile.JWKS) { j.Keys = append(j.Keys, j.Keys[0]) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, hs := newURLHarness(t)
			h.createKeys()

			f := h.export()

			var jwks profile.JWKS
			if err := json.Unmarshal(f.JWKS, &jwks); err != nil || len(jwks.Keys) < 2 {
				t.Fatalf("exported JWKS = %s, %v", f.JWKS, err)
			}

			active := jwks.Keys[0]

			tc.change(&jwks)

			raw, err := json.Marshal(jwks)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}

			hs.copyFiles(t, f)
			hs.set("/acme/jwks", string(raw))

			c := h.checkCopy()
			if c.OK {
				t.Errorf("the check passes for %s", tc.name)
			}

			if c.Holds(hs.url(), profile.KeyID(active.Kid)) {
				t.Errorf("the check holds the key %s although the copy differs", active.Kid)
			}

			_, err = h.login("sts.amazonaws.com", 5*time.Minute)
			wantErr(t, "Login after a copy that differs", err, profile.ErrIssuerKeyMissing)
		})
	}
}

// settings reads the issuer settings of the namespace from the store.
func (h *harness) settings() profile.IssuerSettings {
	h.t.Helper()

	set, err := h.store.Issuer().GetIssuerSettings(h.t.Context(), h.ns.Name)
	if err != nil {
		h.t.Fatalf("GetIssuerSettings: %v", err)
	}

	return set
}
