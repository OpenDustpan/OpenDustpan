package dustpan_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/setting"
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

		// The reason shows the bounds of the gate package, so that a change of
		// a bound shows in the text.
		wantRange := fmt.Sprintf("from %d to %d", gate.MinRetentionHours, gate.MaxRetentionHours)
		if value == `23` && !strings.Contains(answer, wantRange) {
			t.Errorf("answer to %s = %s, want it to hold %q", body, answer, wantRange)
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
	if code != http.StatusOK || strings.Count(entries, gate.RetentionKey) < 1 || !strings.Contains(entries, "8760") ||
		!strings.Contains(entries, `"old":"0"`) {
		t.Fatalf("audit entries = %d %s, want an entry that names %s, the old value 0 and 8760",
			code, entries, gate.RetentionKey)
	}
}

// TestDecisionRecordRetentionIgnoresRefusedStoredValue proves that a stored
// value that the declaration refuses, as after a restore from another
// version, is not read: the item has the default value and the default source.
// This is the layer below the PATCH check, so the test writes the value
// straight into the settings store.
func TestDecisionRecordRetentionIgnoresRefusedStoredValue(t *testing.T) {
	t.Parallel()

	for _, stored := range []string{"23", "024"} {
		t.Run(stored, func(t *testing.T) {
			t.Parallel()

			p := newPorts(t)
			storeRawRetention(t, p.st.SettingStore(), stored)

			opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
				dustpan.WithSettings(p.st.SettingStore(), nil, nil),
				dustpan.WithIdempotencyStore(p.st.Idempotency(testWrapper(t))),
			)

			srv, err := dustpan.NewServer(opts...)
			if err != nil {
				t.Fatalf("new server: %v", err)
			}

			ts := httptest.NewServer(startedHandler(t, srv))
			t.Cleanup(ts.Close)

			doc, _ := readSettings(t, ts.URL)

			item := itemOf(t, doc, gate.RetentionKey)
			if item.Value != float64(0) || item.Source != "default" {
				t.Fatalf("%s with stored %q = %+v, want value 0 and source default", gate.RetentionKey, stored, item)
			}
		})
	}
}

// storeRawRetention writes value for the retention key into store through a
// declaration with no check, so that the store holds a value that the real
// declaration refuses.
func storeRawRetention(t *testing.T, store setting.Store, value string) {
	t.Helper()

	reg, err := setting.NewRegistry([]setting.Declaration{{
		Key: gate.RetentionKey, Level: setting.LevelInstallation, Kind: setting.KindString, Default: "0",
		Secret: false, Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive,
		Check: nil, ReducesSecurity: nil, Doc: "A declaration with no check, for a test.",
	}})
	if err != nil {
		t.Fatalf("registry: %v", err)
	}

	_, err = store.Update(t.Context(), setting.InstallationScope(), namespace.Fence{Namespace: "", FailoverVersion: 0},
		setting.ChangeInput{
			Registry: reg, Static: nil, Patch: setting.Patch{{Key: gate.RetentionKey, Value: value, Delete: false}},
			Entry: audit.Entry{
				At: time.UnixMilli(1), Namespace: audit.ServerScope, Subject: "admin", IDPSubject: "", TokenID: "",
				Action: string(identity.ActionServerManageSettings), Object: audit.ServerScope.String(),
				Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "",
				Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "",
				ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "",
				JobID: "", Origin: nil,
			},
			IfMatch: 0, Fresh: true, AtMs: 1_700_000_000_000,
		})
	if err != nil {
		t.Fatalf("store raw %q: %v", value, err)
	}
}
