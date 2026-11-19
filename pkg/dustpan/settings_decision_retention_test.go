package dustpan_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/gate"
)

// getAuditEntries reads the audit entries of the installation and answers the
// status and the body.
func getAuditEntries(t *testing.T, base string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		base+"/api/audit/entries?organization=@server&page[size]=100", http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET audit entries: %v", err)
	}

	data, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	return resp.StatusCode, string(data)
}

// TestDecisionRecordRetentionSetting proves that the settings resource lists
// the retention key with the default that keeps forever, stores a valid
// period with an audit entry and refuses a hostile value with 400.
func TestDecisionRecordRetentionSetting(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, dustpan.DefaultDrainPeriod)

	doc, etag := readSettings(t, ts.URL)
	item := itemOf(t, doc, gate.RetentionKey)

	if item.Value != float64(0) {
		t.Fatalf("%s value = %v (%T), want 0", gate.RetentionKey, item.Value, item.Value)
	}

	if item.Source != "default" || !item.DatabaseLayer || item.Locked || item.Apply != "live" {
		t.Fatalf("%s = %+v, want source default, a database layer, not locked, apply live", gate.RetentionKey, item)
	}

	// A hostile value gets 400 and stores nothing.
	refuseHostileRetention(t, ts.URL, etag)

	after, etag := readSettings(t, ts.URL)
	if got := itemOf(t, after, gate.RetentionKey); got.Source != "default" {
		t.Fatalf("after the refusals %s = %+v, want source default", gate.RetentionKey, got)
	}

	checkRetentionStored(t, ts.URL, etag)
}

// TestDecisionRecordRetentionShowsReducesSecurity proves that the item tells
// the reader that a change of the key can reduce security, so a client asks
// for a fresh authentication first.
func TestDecisionRecordRetentionShowsReducesSecurity(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, time.Second)
	doc, _ := readSettings(t, ts.URL)

	if !itemOf(t, doc, gate.RetentionKey).MayReduceSecurity {
		t.Fatalf("%s does not show mayReduceSecurity", gate.RetentionKey)
	}
}

// refuseHostileRetention sends each hostile value and fails unless the answer
// is 400.
func refuseHostileRetention(t *testing.T, base, etag string) {
	t.Helper()

	// Each value is a JSON token as the client sends it. A number is the form
	// that the kind int takes; a text is refused by the kind.
	for _, value := range []string{
		`23`, `87601`, `-1`, `+24`, `024`, `1e3`, `24.0`, `99999999999999999999`, `true`, `null0`,
		`"23"`, `"87601"`, `"-1"`, `"+24"`, `"024"`, `"1e3"`, `" 24"`, `"24.0"`, `"abc"`, `""`, `"8760"`,
	} {
		body := `{"` + gate.RetentionKey + `":` + value + `}`

		status, answer := patchSettings(t, base, etag, body)
		if status != http.StatusBadRequest {
			t.Errorf("PATCH %s = %d %s, want 400", body, status, answer)
		}

		// A body that is not valid JSON names no key. A value that the check
		// refuses names the key.
		if strings.Contains(answer, "settings.value-invalid") && !strings.Contains(answer, gate.RetentionKey) {
			t.Errorf("answer to %s = %s, want it to name the key", body, answer)
		}
	}
}

// checkRetentionStored stores a valid period and checks the item and the audit
// entry.
func checkRetentionStored(t *testing.T, base, etag string) {
	t.Helper()

	// A valid period is stored, with source database, and one audit entry.
	status, answer := patchSettings(t, base, etag, `{"`+gate.RetentionKey+`":8760}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH 8760 = %d %s, want 200", status, answer)
	}

	stored, _ := readSettings(t, base)

	got := itemOf(t, stored, gate.RetentionKey)
	if got.Source != "database" || got.Value != float64(8760) {
		t.Fatalf("%s = %+v, want value 8760 and source database", gate.RetentionKey, got)
	}

	code, entries := getAuditEntries(t, base)
	if code != http.StatusOK || strings.Count(entries, gate.RetentionKey) < 1 || !strings.Contains(entries, "8760") {
		t.Fatalf("audit entries = %d %s, want an entry that names %s and 8760", code, entries, gate.RetentionKey)
	}
}
