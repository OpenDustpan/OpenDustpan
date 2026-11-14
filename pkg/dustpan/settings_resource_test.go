package dustpan_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/setting"
)

// drainPeriodSetting is the key of the drain period in the settings
// resource, as the API names it.
const drainPeriodSetting = "server.drainPeriod"

// settingsPath is the path of the settings resource of the installation.
const settingsPath = "/api/dustpan/v1/settings"

// settingsDocument is the settings resource as the API answers it.
type settingsDocument struct {
	Revision int64          `json:"revision"`
	Settings []settingsItem `json:"settings"`
}

// settingsItem is one setting of an answer.
type settingsItem struct {
	Key           string `json:"key"`
	Value         any    `json:"value"`
	Source        string `json:"source"`
	Locked        bool   `json:"locked"`
	DatabaseLayer bool   `json:"databaseLayer"`
	Apply         string `json:"apply"`
}

// startSettingsServer starts a server with the settings resource over a new
// store, with the drain period period, and stops it when the test ends.
func startSettingsServer(t *testing.T, period time.Duration) *httptest.Server {
	t.Helper()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn),
		dustpan.WithSettings(p.st.SettingStore(), nil, nil),
		dustpan.WithDrainPeriod(period),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	return ts
}

// readSettings reads the settings resource of the installation and answers
// the document and its ETag.
func readSettings(t *testing.T, base string) (settingsDocument, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+settingsPath, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", settingsPath, err)
	}

	data, readErr := io.ReadAll(resp.Body)

	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s, want 200", settingsPath, resp.StatusCode, data)
	}

	var doc settingsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}

	return doc, resp.Header.Get("ETag")
}

// itemOf returns the item of key in doc.
func itemOf(t *testing.T, doc settingsDocument, key string) settingsItem {
	t.Helper()

	i := slices.IndexFunc(doc.Settings, func(s settingsItem) bool { return s.Key == key })
	if i < 0 {
		t.Fatalf("the answer has no setting %s", key)
	}

	return doc.Settings[i]
}

// patchSettings sends one merge patch with the revision etag and answers the
// status and the body.
func patchSettings(t *testing.T, base, etag, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPatch, base+settingsPath, strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)
	req.Header.Set("Content-Type", "application/merge-patch+json")
	req.Header.Set("If-Match", etag)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", settingsPath, err)
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

// TestDrainPeriodSetting proves that the settings resource shows the drain
// period that the server runs with: its value in its unit, its source and
// the fields of a read-only setting.
func TestDrainPeriodSetting(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		period    time.Duration
		wantValue string
	}{
		{name: "the default of the binary", period: dustpan.DefaultDrainPeriod, wantValue: "10s"},
		{name: "another period", period: 5 * time.Second, wantValue: "5s"},
		{name: "no drain", period: 0, wantValue: "0s"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ts := startSettingsServer(t, tc.period)
			doc, _ := readSettings(t, ts.URL)
			item := itemOf(t, doc, drainPeriodSetting)

			if item.Value != tc.wantValue {
				t.Fatalf("%s value = %v, want %q", drainPeriodSetting, item.Value, tc.wantValue)
			}

			if item.Source != "default" {
				t.Fatalf("%s source = %q, want default", drainPeriodSetting, item.Source)
			}

			if !item.Locked || item.DatabaseLayer || item.Apply != "restart" {
				t.Fatalf("%s = %+v, want a locked read-only setting with apply restart", drainPeriodSetting, item)
			}
		})
	}
}

// TestDrainPeriodSettingRefusesPatch proves that a change of the drain period
// is refused as a change of a setting that only the static layer can set.
func TestDrainPeriodSettingRefusesPatch(t *testing.T) {
	t.Parallel()

	ts := startSettingsServer(t, dustpan.DefaultDrainPeriod)
	_, etag := readSettings(t, ts.URL)

	status, body := patchSettings(t, ts.URL, etag, `{"`+drainPeriodSetting+`":"5s"}`)
	if status != http.StatusConflict || !strings.Contains(body, `"errorCode":"settings.static-only"`) {
		t.Fatalf("PATCH %s = %d %s, want 409 settings.static-only", drainPeriodSetting, status, body)
	}
}

func TestWithSettingsRefusesADeclarationThatIsNotValid(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	bad := []setting.Declaration{{
		Key: "Not A Key", Level: setting.LevelInstallation, Kind: setting.KindString, Default: "", Secret: false,
		Database: true, StaticCredentialHost: false, Apply: setting.ApplyLive, Check: nil, ReducesSecurity: nil,
		Doc: "A setting.",
	}}

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn), dustpan.WithSettings(p.st.SettingStore(), bad, nil))

	if _, err := dustpan.NewServer(opts...); !errors.Is(err, setting.ErrInvalidDeclaration) {
		t.Fatalf("NewServer = %v, want ErrInvalidDeclaration", err)
	}
}

func TestWithSettingsAcceptsNoDeclaration(t *testing.T) {
	t.Parallel()

	p := newPorts(t)

	opts := append(fullServerOptions(t, p.st, p.blobs, p.authn), dustpan.WithSettings(p.st.SettingStore(), nil, nil))

	if _, err := dustpan.NewServer(opts...); err != nil {
		t.Fatalf("NewServer: %v", err)
	}
}
