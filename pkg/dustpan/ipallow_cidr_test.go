package dustpan_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// TestInvalidCidrRefused proves that the installation settings accept a valid
// CIDR list and refuse an invalid entry without storing it. The first read
// also proves that both installation keys are declared.
func TestInvalidCidrRefused(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, dustpan.DefaultDrainPeriod)

	doc, etag := readSettings(t, ts.URL)
	itemOf(t, doc, "ipAllowlist.enabled")
	itemOf(t, doc, "ipAllowlist.installation")

	// The test client comes from 127.0.0.1, so the list holds that address:
	// after the change the settings route is itself behind the list.
	status, body := patchSettings(t, ts.URL, etag,
		`{"ipAllowlist.enabled":true,"ipAllowlist.installation":"127.0.0.1/32,198.51.100.0/24"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH a valid list = %d %s, want 200", status, body)
	}

	doc, etag = readSettings(t, ts.URL)

	if got, ok := itemOf(t, doc, "ipAllowlist.enabled").Value.(bool); !ok || !got {
		t.Fatalf("ipAllowlist.enabled = %v, want true", got)
	}

	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "127.0.0.1/32,198.51.100.0/24" {
		t.Fatalf("ipAllowlist.installation = %v, want the stored list", got)
	}

	status, body = patchSettings(t, ts.URL, etag, `{"ipAllowlist.installation":"999.999.0.0/16"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PATCH an invalid CIDR = %d %s, want 400", status, body)
	}

	if !strings.Contains(body, `"errorCode":"ip-allowlist.invalid-cidr"`) {
		t.Fatalf("PATCH an invalid CIDR = %s, want ip-allowlist.invalid-cidr", body)
	}

	if !strings.Contains(body, "999.999.0.0/16") {
		t.Fatalf("PATCH an invalid CIDR = %s, want the bad entry in the message", body)
	}

	doc, _ = readSettings(t, ts.URL)

	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "127.0.0.1/32,198.51.100.0/24" {
		t.Fatalf("ipAllowlist.installation after the refused change = %v, want the stored list", got)
	}
}

// TestTooManyCidrsRefused proves the entry cap: the 65th entry answers 400
// with ip-allowlist.invalid-cidr and the change is not stored.
func TestTooManyCidrsRefused(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, dustpan.DefaultDrainPeriod)
	_, etag := readSettings(t, ts.URL)

	items := make([]string, 0, ipallow.MaxEntries+1)
	for i := range ipallow.MaxEntries + 1 {
		items = append(items, fmt.Sprintf("10.0.%d.0/24", i))
	}

	body := fmt.Sprintf(`{"ipAllowlist.installation":%q}`, strings.Join(items, ","))
	status, answer := patchSettings(t, ts.URL, etag, body)

	if status != http.StatusBadRequest || !strings.Contains(answer, `"errorCode":"ip-allowlist.invalid-cidr"`) {
		t.Fatalf("PATCH 65 CIDRs = %d %s, want 400 ip-allowlist.invalid-cidr", status, answer)
	}

	doc, _ := readSettings(t, ts.URL)
	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "" {
		t.Fatalf("ipAllowlist.installation after the refused change = %v, want the empty default", got)
	}
}
