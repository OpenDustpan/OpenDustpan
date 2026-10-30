package issuersvc_test

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/profile/profiletest"
)

// host is a static file host: the place where a person copies the two files.
type host struct {
	mu    sync.Mutex
	files map[string]string
	code  int
	srv   *httptest.Server
}

func newHost(t *testing.T) *host {
	t.Helper()

	hs := &host{mu: sync.Mutex{}, files: map[string]string{}, code: http.StatusOK, srv: nil}

	hs.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs.mu.Lock()
		defer hs.mu.Unlock()

		body, ok := hs.files[r.URL.Path]
		if !ok || hs.code != http.StatusOK {
			code := hs.code
			if code == http.StatusOK {
				code = http.StatusNotFound
			}

			http.Error(w, "no", code)

			return
		}

		_, _ = w.Write([]byte(body))
	}))

	t.Cleanup(hs.srv.Close)

	return hs
}

// url is the issuerURL that a namespace sets to use this host.
func (hs *host) url() string { return hs.srv.URL + "/acme" }

func (hs *host) set(path, body string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()

	hs.files[path] = body
}

// copyFiles copies the two files that Export answers.
func (hs *host) copyFiles(t *testing.T, f issuersvc.Files) {
	t.Helper()

	hs.set("/acme/.well-known/openid-configuration", string(f.Discovery))
	hs.set("/acme/jwks", string(f.JWKS))
}

func guardedClient(hs *host, rules egress.Rules) *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(hs.srv.Certificate())

	return egresshttp.ClientWith(rules, egress.NoRedirect, egress.HTTPSOnly, egresshttp.Options{RootCAs: pool, Resolver: nil})
}

// newURLHarness is a harness whose namespace sets an issuerURL on a host.
func newURLHarness(t *testing.T) (*harness, *host) {
	t.Helper()

	hs := newHost(t)
	h := newHarness(t, func(c *issuerConfig, _ *harness) {
		c.HTTP = guardedClient(hs, egress.Rules{Allow: nil, LoopbackForTests: true})
	})

	if _, err := h.svc.Patch(t.Context(), h.admin, h.ns.Name, profile.SettingsPatch{IssuerURL: str(hs.url())}, 0, okFresh); err != nil {
		t.Fatalf("set issuerURL: %v", err)
	}

	return h, hs
}

func (h *harness) export() issuersvc.Files {
	h.t.Helper()

	f, err := h.svc.Export(h.t.Context(), h.admin, h.ns.Name)
	if err != nil {
		h.t.Fatalf("Export: %v", err)
	}

	return f
}

func (h *harness) checkCopy() profile.CopyCheck {
	h.t.Helper()

	c, err := h.svc.CheckCopy(h.t.Context(), h.admin, h.ns.Name)
	if err != nil {
		h.t.Fatalf("CheckCopy: %v", err)
	}

	return c
}

func stepOK(c profile.CopyCheck, name string) (ok, found bool) {
	for _, s := range c.Steps {
		if s.Name == name {
			return s.OK, true
		}
	}

	return false, false
}

func TestNoKeyNoTokenForANamespaceWithAnIssuerURL(t *testing.T) {
	t.Parallel()

	h, _ := newURLHarness(t)

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login with no key", err, profile.ErrIssuerKeyMissing)

	if !strings.Contains(err.Error(), "/issuer/keys") {
		t.Errorf("the error does not name the route that makes the keys: %v", err)
	}

	if got := h.keysOf(h.ns); len(got) != 0 {
		t.Errorf("a refused open made %d keys that no copied file holds", len(got))
	}

	_, err = h.svc.Export(t.Context(), h.admin, h.ns.Name)
	wantErr(t, "Export with no key", err, profile.ErrIssuerKeyMissing)

	if got := h.keysOf(h.ns); len(got) != 0 {
		t.Errorf("an export made %d keys", len(got))
	}
}

func TestTheServerSignsOnlyWithAKeyThatACopyHolds(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login before the first copy", err, profile.ErrIssuerKeyMissing)

	for _, route := range []string{"/issuer/export", "/issuer/keys/check-copy"} {
		if !strings.Contains(err.Error(), route) {
			t.Errorf("the error does not name %s: %v", route, err)
		}
	}

	f := h.export()
	hs.copyFiles(t, f)

	c := h.checkCopy()
	if !c.OK || len(c.Verified) != 2 {
		t.Fatalf("copy check = %+v", c)
	}

	for _, s := range []string{"discovery", "jwks", "active-key", "next-key"} {
		if ok, found := stepOK(c, s); !found || !ok {
			t.Errorf("step %s: ok %v, found %v", s, ok, found)
		}
	}

	tok := h.mustLogin()
	if tok.Issuer != hs.url() {
		t.Errorf("iss = %q, want the issuerURL %q", tok.Issuer, hs.url())
	}

	claims, err := profiletest.VerifyToken(f.JWKS, tok.Value, profiletest.Expect{
		Now: h.clock.Now(), Issuer: hs.url(), Audience: "sts.amazonaws.com",
	})
	if err != nil || claims["iss"] != hs.url() {
		t.Errorf("the token does not verify against the copied files: %v", err)
	}

	d, err := h.svc.Discovery(t.Context(), h.ns.ID)
	if err != nil || d.Issuer != hs.url() || d.JWKSURI != hs.url()+"/jwks" {
		t.Errorf("discovery = %+v, %v", d, err)
	}
}

func TestACopyThatLacksTheActiveKeyFails(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()

	f := h.export()
	hs.copyFiles(t, f)
	hs.set("/acme/jwks", `{"keys":[]}`)

	c := h.checkCopy()
	if c.OK || len(c.Verified) != 0 {
		t.Fatalf("copy check = %+v", c)
	}

	if ok, _ := stepOK(c, "active-key"); ok {
		t.Error("the active key step passes")
	}

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after a failed check", err, profile.ErrIssuerKeyMissing)
}

func TestTheCopyCheckRefusesADocumentThatNamesAnotherIssuerOrKeyAddress(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()

	f := h.export()

	for name, disc := range map[string]string{
		"another issuer":       strings.ReplaceAll(string(f.Discovery), hs.url(), "https://evil.example.com/acme"),
		"another jwks_uri":     strings.ReplaceAll(string(f.Discovery), hs.url()+"/jwks", "https://127.0.0.1:1/jwks"),
		"not JSON":             "<html>",
		"trailing data":        string(f.Discovery) + "{}",
		"an empty body":        "",
		"an issuer with slash": strings.ReplaceAll(string(f.Discovery), hs.url()+`"`, hs.url()+`/"`),
	} {
		hs.copyFiles(t, f)
		hs.set("/acme/.well-known/openid-configuration", disc)

		c := h.checkCopy()
		if c.OK {
			t.Errorf("%s: the copy check passes", name)
		}

		if ok, _ := stepOK(c, "discovery"); ok {
			t.Errorf("%s: the discovery step passes", name)
		}

		if _, found := stepOK(c, "jwks"); found {
			t.Errorf("%s: the check read the JWKS after a bad discovery document", name)
		}
	}
}

func TestTheCopyCheckFailsOnABadAnswerFromTheHost(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()
	hs.copyFiles(t, h.export())

	hs.mu.Lock()
	hs.code = http.StatusInternalServerError
	hs.mu.Unlock()

	c := h.checkCopy()
	if c.OK {
		t.Error("the check passes on an answer of 500")
	}

	hs.mu.Lock()
	hs.code = http.StatusOK
	hs.mu.Unlock()

	hs.set("/acme/jwks", strings.Repeat("x", egress.MaxBodyBytes+10))

	if c = h.checkCopy(); c.OK {
		t.Error("the check passes on a body above the limit")
	}

	hs.set("/acme/jwks", `{"keys":[{"kid":"x"}]} extra`)

	if c = h.checkCopy(); c.OK {
		t.Error("the check passes on a JWKS with trailing data")
	}
}

func TestTheCopyCheckGoesThroughTheEgressGuard(t *testing.T) {
	t.Parallel()

	hs := newHost(t)
	h := newHarness(t, func(c *issuerConfig, _ *harness) {
		// The default rules refuse a loopback address.
		c.HTTP = guardedClient(hs, egress.Rules{Allow: nil, LoopbackForTests: false})
	})

	if _, err := h.svc.Patch(t.Context(), h.admin, h.ns.Name, profile.SettingsPatch{IssuerURL: str(hs.url())}, 0, okFresh); err != nil {
		t.Fatal(err)
	}

	h.createKeys()
	hs.copyFiles(t, h.export())

	c := h.checkCopy()
	if c.OK {
		t.Fatal("the copy check reached a loopback address")
	}

	for _, s := range c.Steps {
		if strings.Contains(s.Detail, "127.0.0.1") {
			t.Errorf("the answer names the address that the guard refused: %q", s.Detail)
		}
	}
}

func TestTheCopyCheckFailsClosedWithNoClient(t *testing.T) {
	t.Parallel()

	hs := newHost(t)
	h := newHarness(t)

	if _, err := h.svc.Patch(t.Context(), h.admin, h.ns.Name, profile.SettingsPatch{IssuerURL: str(hs.url())}, 0, okFresh); err != nil {
		t.Fatal(err)
	}

	h.createKeys()
	hs.copyFiles(t, h.export())

	if c := h.checkCopy(); c.OK {
		t.Error("a server with no client passes the check")
	}
}

func TestACheckNeedsAnIssuerURL(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.createKeys()

	_, err := h.svc.CheckCopy(t.Context(), h.admin, h.ns.Name)
	wantErr(t, "CheckCopy", err, profile.ErrInvalid)

	_, err = h.svc.Export(t.Context(), h.admin, h.ns.Name)
	wantErr(t, "Export", err, profile.ErrInvalid)
}

func TestManualRotationWaitsForTheCopy(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()
	hs.copyFiles(t, h.export())
	h.checkCopy()

	old := h.mustLogin()

	h.clock.Advance(time.Minute)

	// The first rotation: the next key is in the copy, so it promotes. The
	// new next key is in no copy.
	res, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	if err != nil || !res.Promoted {
		t.Fatalf("first rotation = %+v, %v", res, err)
	}

	before := h.keysOf(h.ns)

	// The second rotation needs the new next key in the copy. It is not.
	h.clock.Advance(time.Minute)

	_, err = h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false})
	wantErr(t, "rotation before the copy", err, profile.ErrIssuerKeyMissing)

	for _, route := range []string{"/issuer/export", "/issuer/keys/check-copy"} {
		if !strings.Contains(err.Error(), route) {
			t.Errorf("the error does not name %s: %v", route, err)
		}
	}

	after := h.keysOf(h.ns)
	if len(after) != len(before) {
		t.Fatal("a refused rotation changed the keys")
	}

	for i := range before {
		if before[i].ID != after[i].ID || before[i].State != after[i].State {
			t.Error("a refused rotation moved a key")
		}
	}

	// After the copy it works.
	hs.copyFiles(t, h.export())

	if res, err = h.svc.Rotate(t.Context(), h.admin, h.ns.Name, issuersvc.RotateRequest{Reason: "", Emergency: false}); err != nil || !res.Promoted {
		t.Fatalf("rotation after the copy = %+v, %v", res, err)
	}

	// A token of the first key signed before the rotations still verifies
	// against the copy.
	if _, err = profiletest.VerifyToken(h.export().JWKS, old.Value, profiletest.Expect{
		Now: old.IssuedAt, Issuer: hs.url(), Audience: "sts.amazonaws.com",
	}); err != nil {
		t.Errorf("the old token does not verify against the new copy: %v", err)
	}
}

func TestAfterAnEmergencyRotationNothingSignsUntilTheCopyHoldsTheNewKey(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()
	hs.copyFiles(t, h.export())
	h.checkCopy()
	h.mustLogin()

	if _, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name,
		issuersvc.RotateRequest{Reason: "leak", Emergency: true}); err != nil {
		t.Fatalf("emergency rotation: %v", err)
	}

	_, err := h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after an emergency rotation", err, profile.ErrIssuerKeyMissing)

	f := h.export()
	hs.copyFiles(t, f)

	_, err = h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login after a copy and before the check", err, profile.ErrIssuerKeyMissing)

	h.checkCopy()

	tok := h.mustLogin()
	if _, err = profiletest.VerifyToken(f.JWKS, tok.Value, profiletest.Expect{
		Now: h.clock.Now(), Issuer: hs.url(), Audience: "sts.amazonaws.com",
	}); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestAnActiveKeyNeverSignsForMoreThan180DaysAndWarnsFromDay90(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()
	hs.copyFiles(t, h.export())
	h.checkCopy()

	if tok := h.mustLogin(); len(tok.Warnings) != 0 {
		t.Errorf("a young key warns: %v", tok.Warnings)
	}

	// The host fails from now on: the schedule cannot promote, and the key
	// keeps signing.
	hs.mu.Lock()
	hs.code = http.StatusInternalServerError
	hs.mu.Unlock()

	h.clock.Advance(90 * day)
	h.rotateAll()

	if got := h.states(); got[profile.KeyActive] != 1 || got[profile.KeyRetired] != 0 {
		t.Fatalf("the schedule promoted without a copy: %v", got)
	}

	tok := h.mustLogin()
	if len(tok.Warnings) != 1 || !strings.Contains(tok.Warnings[0], "90 days are left") {
		t.Errorf("warnings at day 90 = %v", tok.Warnings)
	}

	st, err := h.svc.Get(t.Context(), h.reader, h.ns.Name)
	if err != nil || len(st.Warnings) != 1 {
		t.Errorf("state warnings = %v, %v", st.Warnings, err)
	}

	if !slicesContains(h.outbox.types(), notify.IssuerKeyCopyMissing) {
		t.Errorf("notices = %v, want issuer.key-copy-missing", h.outbox.types())
	}

	h.clock.Advance(89 * day)

	if tok = h.mustLogin(); len(tok.Warnings) != 1 || !strings.Contains(tok.Warnings[0], "1 days are left") {
		t.Errorf("warnings at day 179 = %v", tok.Warnings)
	}

	h.clock.Advance(day)

	_, err = h.login("sts.amazonaws.com", 5*time.Minute)
	wantErr(t, "Login at day 180", err, profile.ErrIssuerKeyExpired)

	for _, route := range []string{"/issuer/export", "/issuer/keys/check-copy"} {
		if !strings.Contains(err.Error(), route) {
			t.Errorf("the error does not name %s: %v", route, err)
		}
	}
}

func TestTheScheduleWaitsForTheCopyOfTheNextKey(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)
	h.createKeys()

	// The copy holds the active key only.
	f := h.export()
	hs.copyFiles(t, f)

	var set profile.JWKS
	if err := jsonUnmarshal(f.JWKS, &set); err != nil || len(set.Keys) != 2 {
		t.Fatalf("export = %v, %v", set, err)
	}

	hs.set("/acme/jwks", jsonString(profile.JWKS{Keys: set.Keys[:1]}))

	if c := h.checkCopy(); c.OK {
		t.Fatal("a copy without the next key passes")
	}

	h.clock.Advance(90 * day)
	h.rotateAll()

	if got := h.states(); got[profile.KeyRetired] != 0 {
		t.Fatalf("the schedule promoted a key that no copy holds: %v", got)
	}

	hs.copyFiles(t, h.export())
	h.rotateAll()

	if got := h.states(); got[profile.KeyRetired] != 1 {
		t.Errorf("the schedule did not promote after the copy: %v", got)
	}
}
