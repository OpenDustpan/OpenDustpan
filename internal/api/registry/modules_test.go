package registry_test

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	apiregistry "opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/pagetoken"
)

const moduleBase = base + "/modules"

// zipOf returns a zip archive with the given files.
func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)

	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip: %v", err)
		}

		if _, err = w.Write([]byte(body)); err != nil {
			t.Fatalf("zip: %v", err)
		}
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("zip: %v", err)
	}

	return buf.Bytes()
}

// plainModule returns a zip archive of a module with one file.
func plainModule(t *testing.T) []byte {
	t.Helper()

	return zipOf(t, map[string]string{"main.tf": "x"})
}

// archiveWire is the archive member of a module release.
type archiveWire struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// moduleWire is the answer of a module publish.
type moduleWire struct {
	Namespace   string      `json:"namespace"`
	Name        string      `json:"name"`
	System      string      `json:"system"`
	Version     string      `json:"version"`
	PublishedBy string      `json:"publishedBy"`
	Archive     archiveWire `json:"archive"`
}

// moduleNameWire is one module in a list.
type moduleNameWire struct {
	Name   string `json:"name"`
	System string `json:"system"`
}

// modulePageWire is one page of the module list.
type modulePageWire struct {
	Modules       []moduleNameWire `json:"modules"`
	NextPageToken string           `json:"nextPageToken"`
}

// publishModule stages data and publishes it as name/system version 1.0.0.
func (f fixture) publishModule(t *testing.T, token, name, system string, data []byte) response {
	t.Helper()

	upload := f.stage(t, data)

	return f.call(t, token, http.MethodPut, moduleBase+"/"+name+"/"+system+"/versions/1.0.0",
		js(t, map[string]string{"upload": upload}))
}

func TestModulePublishFlow(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	data := plainModule(t)

	// A reader may not publish.
	body := js(t, map[string]string{"upload": f.stage(t, data)})
	wantCode(t, "reader publishes", f.call(t, "reader", http.MethodPut, moduleBase+"/vpc/aws/versions/1.0.0", body),
		http.StatusForbidden)

	// A publisher needs no signing key.
	got := f.publishModule(t, "publisher", "vpc", "aws", data)
	wantCode(t, "publish", got, http.StatusOK)

	var doc moduleWire
	if err := json.Unmarshal([]byte(got.body), &doc); err != nil || doc.Namespace != "acme" || doc.Name != "vpc" ||
		doc.System != "aws" || doc.Version != "1.0.0" || doc.PublishedBy != "publisher" ||
		doc.Archive.Name != "vpc-aws-1.0.0.zip" || doc.Archive.Size != int64(len(data)) ||
		len(doc.Archive.SHA256) != 64 {
		t.Fatalf("publish answer = %q, %v", got.body, err)
	}

	checkModuleConflict(t, f, data)
	checkModuleReads(t, f)
}

// checkModuleConflict proves that the same bytes are a repeat and other bytes
// for the version are a conflict.
func checkModuleConflict(t *testing.T, f fixture, data []byte) {
	t.Helper()

	wantCode(t, "repeat", f.publishModule(t, "publisher", "vpc", "aws", data), http.StatusOK)

	other := f.publishModule(t, "publisher", "vpc", "aws", zipOf(t, map[string]string{"main.tf": "y"}))
	wantCode(t, "other bytes", other, http.StatusConflict)

	if !strings.Contains(other.body, "registry.version-exists") {
		t.Errorf("conflict body = %s", other.body)
	}
}

// checkModuleReads proves the three read routes, and that a module is not a
// provider.
func checkModuleReads(t *testing.T, f fixture) {
	t.Helper()

	list := f.call(t, "reader", http.MethodGet, moduleBase+"/vpc/aws/versions", nil)
	wantCode(t, "list versions", list, http.StatusOK)

	if strings.Count(list.body, `"version":`) != 1 {
		t.Errorf("version list = %s", list.body)
	}

	wantCode(t, "get version", f.call(t, "reader", http.MethodGet, moduleBase+"/vpc/aws/versions/1.0.0", nil),
		http.StatusOK)

	mods := f.call(t, "reader", http.MethodGet, moduleBase, nil)
	if mods.code != http.StatusOK || !strings.Contains(mods.body, `"name":"vpc","system":"aws"`) ||
		!strings.Contains(mods.body, `"nextPageToken":""`) {
		t.Errorf("module list = %d %s", mods.code, mods.body)
	}

	prov := f.call(t, "reader", http.MethodGet, base+"/providers", nil)
	if prov.code != http.StatusOK || strings.Contains(prov.body, "vpc") {
		t.Errorf("provider list = %d %s, want no module", prov.code, prov.body)
	}

	for _, target := range []string{"/vpc/gcp/versions", "/net/aws/versions", "/vpc/aws/versions/9.9.9"} {
		wantCode(t, "GET "+target, f.call(t, "reader", http.MethodGet, moduleBase+target, nil), http.StatusNotFound)
	}
}

func TestModulePublishRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"path traversal", zipOf(t, map[string]string{"../evil.tf": "x"})},
		{"absolute path", zipOf(t, map[string]string{"/etc/cron.d/x": "x"})},
		{"not an archive", []byte("module {}")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)

			got := f.publishModule(t, "admin", "vpc", "aws", tc.data)
			if got.code != http.StatusUnprocessableEntity ||
				!strings.Contains(got.body, "registry.module-archive-refused") {
				t.Fatalf("publish = %d %s, want 422 registry.module-archive-refused", got.code, got.body)
			}

			wantCode(t, "the version stays absent",
				f.call(t, "admin", http.MethodGet, moduleBase+"/vpc/aws/versions/1.0.0", nil), http.StatusNotFound)
		})
	}
}

func TestModulePublishHostileRequests(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	upload := f.stage(t, plainModule(t))
	good := `{"upload":"` + upload + `"}`
	never := `{"upload":"` + strings.Repeat("a", 32) + `"}`

	tests := []struct {
		name, target, body string
		code               int
	}{
		{"name with a dot", "/v.pc/aws/versions/1.0.0", good, http.StatusBadRequest},
		{"system in capitals", "/vpc/AWS/versions/1.0.0", good, http.StatusBadRequest},
		{"version with metadata", "/vpc/aws/versions/1.0.0+x", good, http.StatusBadRequest},
		{"upload that is a path", "/vpc/aws/versions/1.0.0", `{"upload":"../../sha256/x"}`, http.StatusBadRequest},
		{"upload never staged", "/vpc/aws/versions/1.0.0", never, http.StatusNotFound},
		{"unknown field", "/vpc/aws/versions/1.0.0", `{"upload":"x","archive":"x"}`, http.StatusBadRequest},
		{"no body", "/vpc/aws/versions/1.0.0", ``, http.StatusBadRequest},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var body []byte
			if tc.body != "" {
				body = []byte(tc.body)
			}

			wantCode(t, tc.name, f.call(t, "admin", http.MethodPut, moduleBase+tc.target, body), tc.code)
		})
	}
}

// pageOf reads one page of the module list.
func pageOf(t *testing.T, f fixture, token string) modulePageWire {
	t.Helper()

	target := moduleBase + "?pageSize=1"
	if token != "" {
		target += "&pageToken=" + token
	}

	got := f.call(t, "reader", http.MethodGet, target, nil)
	wantCode(t, "page", got, http.StatusOK)

	var page modulePageWire
	if err := json.Unmarshal([]byte(got.body), &page); err != nil {
		t.Fatalf("page = %q: %v", got.body, err)
	}

	return page
}

func TestModuleListPaging(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	data := plainModule(t)

	for _, a := range [][2]string{{"vpc", "aws"}, {"vpc", "gcp"}, {"vpc-core", "aws"}, {"net", "aws"}} {
		wantCode(t, "publish "+a[0]+"/"+a[1], f.publishModule(t, "admin", a[0], a[1], data), http.StatusOK)
	}

	var (
		seen  []string
		token string
	)

	for range 5 {
		page := pageOf(t, f, token)
		for _, m := range page.Modules {
			seen = append(seen, m.Name+"/"+m.System)
		}

		if token = page.NextPageToken; token == "" {
			break
		}
	}

	if want := "net/aws vpc/aws vpc/gcp vpc-core/aws"; strings.Join(seen, " ") != want {
		t.Fatalf("modules in page order = %v, want %s", seen, want)
	}
}

// moduleToken is the wire shape of the module page token. The tests build one
// to send a cursor that names no module.
type moduleToken struct {
	After string `json:"a"`
}

// moduleTokenOf returns a page token that holds the cursor after.
func moduleTokenOf(t *testing.T, after string) string {
	t.Helper()

	token, err := pagetoken.Encode(moduleToken{After: after})
	if err != nil {
		t.Fatalf("encode a page token: %v", err)
	}

	return token
}

func TestModuleListHostileQuery(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	encode := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

	// The parallel rows share one fixture and one call counter. Put only rows
	// that make no store call in this table: a valid row would break the
	// zero-calls check of every other row.
	for _, tc := range []struct {
		name  string
		token string
	}{
		{"token that is not base64", "!!!"},
		{"token in the old raw form", encode("vpc/aws")},
		{"token that is not JSON", encode("{")},
		{"empty cursor", moduleTokenOf(t, "")},
		{"cursor with no system", moduleTokenOf(t, "vpc")},
		{"cursor with an empty system", moduleTokenOf(t, "vpc/")},
		{"upper case system", moduleTokenOf(t, "vpc/AWS")},
		{"path traversal", moduleTokenOf(t, "../x/aws")},
		{"token over the length limit", strings.Repeat("A", 5000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wantCode(t, tc.name, f.call(t, "reader", http.MethodGet, moduleBase+"?pageToken="+tc.token, nil),
				http.StatusBadRequest)

			if calls := f.registry.addresses.Load(); calls != 0 {
				t.Errorf("the store got %d address list calls, want none", calls)
			}
		})
	}

	wantCode(t, "page size", f.call(t, "reader", http.MethodGet, moduleBase+"?pageSize=0", nil), http.StatusBadRequest)
}

// TestParseModuleTokenGuards proves each guard of the module page token
// parser alone: each row sends a token that only one guard refuses, and
// asserts the cause of that guard. The last row proves that the guards accept
// a valid token.
func TestParseModuleTokenGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raw   string
		cause error
		after string
	}{
		{
			"a token over the length limit",
			strings.Repeat("A", pagetoken.MaxLen+1),
			apiregistry.ErrModuleTokenLength,
			"",
		},
		{"a token that is not a page token", "!!!", apiregistry.ErrModuleTokenDecode, ""},
		{"a token with an empty cursor", moduleTokenOf(t, ""), apiregistry.ErrModuleTokenEmpty, ""},
		{
			"a token whose cursor is no module key",
			moduleTokenOf(t, "vpc"),
			apiregistry.ErrModuleTokenAddress,
			"",
		},
		{"a token of a module", moduleTokenOf(t, "vpc/aws"), nil, "vpc/aws"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			after, err := apiregistry.ParseModulePageToken(tt.raw, "acme")

			if tt.cause != nil {
				if !errors.Is(err, tt.cause) {
					t.Fatalf("parse %q = %q, %v, want %v", tt.raw, after, err, tt.cause)
				}

				return
			}

			if err != nil || after != tt.after {
				t.Fatalf("parse %q = %q, %v, want %q", tt.raw, after, err, tt.after)
			}
		})
	}
}
