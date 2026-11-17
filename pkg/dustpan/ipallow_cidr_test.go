package dustpan_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
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

	if got := itemOf(t, doc, "ipAllowlist.enabled").Value; got != true {
		t.Fatalf("ipAllowlist.enabled = %v, want true", got)
	}

	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "127.0.0.1/32,198.51.100.0/24" {
		t.Fatalf("ipAllowlist.installation = %v, want the stored list", got)
	}

	status, body = patchSettings(t, ts.URL, etag, `{"ipAllowlist.installation":"999.999.0.0/16"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("PATCH an invalid CIDR = %d %s, want 400", status, body)
	}

	if !strings.Contains(body, "999.999.0.0/16") {
		t.Fatalf("PATCH an invalid CIDR = %s, want the bad entry in the message", body)
	}

	doc, _ = readSettings(t, ts.URL)

	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "127.0.0.1/32,198.51.100.0/24" {
		t.Fatalf("ipAllowlist.installation after the refused change = %v, want the stored list", got)
	}
}

// TestTooManyCidrsRefused proves the design's 256-entry cap: the list accepts
// 256 entries and refuses 257 without storing the change.
func TestTooManyCidrsRefused(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, dustpan.DefaultDrainPeriod)
	_, etag := readSettings(t, ts.URL)

	items := make([]string, 0, 257)
	for i := range 257 {
		items = append(items, fmt.Sprintf("10.%d.%d.0/24", i/256, i%256))
	}

	body := fmt.Sprintf(`{"ipAllowlist.installation":%q}`, strings.Join(items, ","))
	status, answer := patchSettings(t, ts.URL, etag, body)

	if status != http.StatusBadRequest {
		t.Fatalf("PATCH 257 CIDRs = %d %s, want 400", status, answer)
	}

	doc, _ := readSettings(t, ts.URL)
	if got := itemOf(t, doc, "ipAllowlist.installation").Value; got != "" {
		t.Fatalf("ipAllowlist.installation after the refused change = %v, want the empty default", got)
	}
}
