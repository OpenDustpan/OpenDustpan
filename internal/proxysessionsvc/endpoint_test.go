package proxysessionsvc_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/proxysessionsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/runner"
)

// reasonEndpointChanged is the fixed reason of the audit entry of a credential
// that a save cleared.
const reasonEndpointChanged = "upstream-endpoint-changed"

// TestPutSettingsClearsCredentialOnEndpointChange is the table of the service
// layer. A save with no credential field moves the upstream proxy. The stored
// credential stays only when the parsed endpoint is the same. This test fails
// when the service keeps the credential for another endpoint, or compares the
// text of the URL.
func TestPutSettingsClearsCredentialOnEndpointChange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		url   string
		clear bool
	}{
		{"other host", "https://other.example.com:3128", true},
		{"other scheme", "http://proxy.example.com:3128", true},
		{"other port", "https://proxy.example.com:3129", true},
		{"old host in the path", "https://evil.example.com:3128/https://proxy.example.com:3128", true},
		{"Unicode look-alike", "https://prоxy.example.com:3128", true},
		{"punycode form of another name", "https://xn--prxy-5cd.example.com:3128", true},
		{"two trailing dots", "https://proxy.example.com..:3128", true},
		{"same host, upper case", "HTTPS://PROXY.EXAMPLE.COM:3128", false},
		{"same host, one trailing dot", "https://proxy.example.com.:3128", false},
		{"same host, other path", "https://proxy.example.com:3128/redirect?to=https://evil.example.com", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			p := pool(poolA, nsAcme)

			mustPut(t, f, putRequest(p))

			move := putRequest(p)
			move.UpstreamURL = tc.url
			move.Credential = proxysessionsvc.CredentialChange{Present: false, Value: nil}
			move.IfMatch = "1"

			got := mustPut(t, f, move)
			if got.CredentialSet == tc.clear {
				t.Errorf("CredentialSet after a save to %q = %v, want %v", tc.url, got.CredentialSet, !tc.clear)
			}

			if tc.clear {
				checkCleared(t, f, p, tc.url)
			} else {
				checkKept(t, f, p, tc.url)
			}
		})
	}
}

// checkCleared checks the stored row and the last audit entry after a save to
// url that cleared the credential: the row has none and the entry says why.
func checkCleared(t *testing.T, f fixture, p runner.Pool, url string) {
	t.Helper()

	if rec := storedRecord(t, f, p); len(rec.Credential) != 0 {
		t.Errorf("stored credential after a save to %q is set, want none", url)
	}

	last := lastEntry(f)

	for _, want := range [][2]string{
		{"credentialCleared", "true"}, {"reason", reasonEndpointChanged}, {"credentialChanged", "true"},
	} {
		if !hasChange(last, want[0], want[1]) {
			t.Errorf("entry after a save to %q = %+v, want %s=%s", url, last, want[0], want[1])
		}
	}
}

// checkKept checks the stored row and the last audit entry after a save to url
// that kept the credential: the row has it and the entry has no clearing.
func checkKept(t *testing.T, f fixture, p runner.Pool, url string) {
	t.Helper()

	if rec := storedRecord(t, f, p); len(rec.Credential) == 0 {
		t.Errorf("stored credential after a save to %q is empty, want it kept", url)
	}

	last := lastEntry(f)
	if hasChange(last, "credentialCleared", "true") || hasChange(last, "reason", reasonEndpointChanged) ||
		hasChange(last, "credentialChanged", "true") {
		t.Errorf("entry after a save to %q = %+v, want no clearing", url, last)
	}
}

func storedRecord(t *testing.T, f fixture, p runner.Pool) runner.ProxySettingsRecord {
	t.Helper()

	rec, err := f.settings.ProxySettings(t.Context(), p.ID)
	if err != nil {
		t.Fatalf("ProxySettings: %v", err)
	}

	return rec
}

func lastEntry(f fixture) audit.Entry {
	entries := f.settings.auditEntries()

	return entries[len(entries)-1]
}

// TestPutSettingsEndpointChangeWithNewCredential checks that a save that moves
// the upstream proxy and sends a new credential keeps the new one, and that a
// null credential is a removal, not a clearing for a reason.
func TestPutSettingsEndpointChangeWithNewCredential(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := pool(poolA, nsAcme)

	mustPut(t, f, putRequest(p))

	move := putRequest(p)
	move.UpstreamURL = "https://other.example.com:3128"
	move.IfMatch = "1"

	if got := mustPut(t, f, move); !got.CredentialSet {
		t.Error("CredentialSet after a move with a new credential = false, want true")
	}

	remove := putRequest(p)
	remove.UpstreamURL = "https://third.example.com:3128"
	remove.Credential = proxysessionsvc.CredentialChange{Present: true, Value: nil}
	remove.IfMatch = "2"

	if got := mustPut(t, f, remove); got.CredentialSet {
		t.Error("CredentialSet after a move with a null credential = true, want false")
	}

	entries := f.settings.auditEntries()
	for i, e := range entries[1:] {
		if hasChange(e, "credentialCleared", "true") || hasChange(e, "reason", reasonEndpointChanged) {
			t.Errorf("entry %d = %+v, want no cleared reason: the save named the credential", i+1, e)
		}
	}
}

// TestPutSettingsClearedCredentialIsNotRestored checks that a later save with
// no credential field, back at the old endpoint, does not bring the credential
// back.
func TestPutSettingsClearedCredentialIsNotRestored(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	p := pool(poolA, nsAcme)

	mustPut(t, f, putRequest(p))

	for i, url := range []string{"https://other.example.com:3128", upstreamURL} {
		req := putRequest(p)
		req.UpstreamURL = url
		req.Credential = proxysessionsvc.CredentialChange{Present: false, Value: nil}

		if got := mustPut(t, f, req); got.CredentialSet {
			t.Errorf("save %d to %q: CredentialSet = true, want false", i+1, url)
		}
	}
}
