package audit_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/audit"
)

// cefExport serves the CEF export of acme and returns its lines.
func cefExport(t *testing.T, handler http.Handler) (*httptest.ResponseRecorder, []string) {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(t, "/api/audit/export?organization=acme&format=cef"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	body := strings.TrimSuffix(rec.Body.String(), "\n")
	if body == "" {
		return rec, nil
	}

	return rec, strings.Split(body, "\n")
}

// TestExportCEFMapsTheFields proves the header and the keys of a CEF line.
func TestExportCEFMapsTheFields(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seed := seedEntry(t, store, "acme", "alice")

	rec, lines := cefExport(t, handler)

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("content type %q", got)
	}

	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}

	for _, want := range []string{
		"CEF:0|OpenDustpan|OpenDustpan|1|unit:decrypt|reveal|3|",
		" suser=alice", " src=192.0.2.1", " act=unit:decrypt", " outcome=success",
		" requestClientApplication=pulumi-cli/3.200.0",
		" cn1=1 cn1Label=sequence", " cs1=acme cs1Label=scope", " cs3=api cs3Label=channel",
		" cs5=" + seed.Hash + " cs5Label=hash", " flexString1=id-alice flexString1Label=objectID",
	} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("line lacks %q:\n%s", want, lines[0])
		}
	}

	// The first entry of a scope has an empty previous hash: no key.
	if strings.Contains(lines[0], "cs6") {
		t.Fatalf("line holds a previous hash key for the first entry:\n%s", lines[0])
	}
}

// TestExportCEFEscapesHostileValues proves that a value cannot end a header
// field, start another key or end the line, whatever a store holds.
func TestExportCEFEscapesHostileValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		want    string
	}{
		{"pipe in extension is literal", "a|b", " suser=a|b "},
		{"equals", "a=b", ` suser=a\=b `},
		{"backslash", `a\b`, ` suser=a\\b `},
		{"line feed", "a\nb", ` suser=a\nb `},
		{"carriage return", "a\rb", ` suser=a\rb `},
		{"key injection", "x act=evil", ` suser=x act\=evil `},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
			seedEntry(t, store, "acme", "alice")
			// A store row from another version can hold any text: write it
			// behind the validation of the port.
			store.entries["acme"][0].Subject = tt.subject

			_, lines := cefExport(t, handler)
			if len(lines) != 1 {
				t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
			}

			if !strings.Contains(lines[0]+" ", tt.want) {
				t.Fatalf("line lacks %q:\n%s", tt.want, lines[0])
			}
		})
	}
}

// TestExportCEFEscapesTheHeader proves that the action and the kind cannot
// add a header field.
func TestExportCEFEscapesTheHeader(t *testing.T) {
	t.Parallel()

	handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
	seedEntry(t, store, "acme", "alice")

	seeded := store.entries["acme"]
	if len(seeded) == 0 {
		t.Fatal("seed wrote no entry")
	}

	seeded[0].Action = `unit|x\y`

	_, lines := cefExport(t, handler)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], `CEF:0|OpenDustpan|OpenDustpan|1|unit\|x\\y|reveal|3|`) {
		t.Fatalf("lines %q", lines)
	}
}

// TestExportCEFSeverity proves the severity of each outcome.
func TestExportCEFSeverity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		outcome  audit.Outcome
		severity string
	}{
		{audit.OutcomeSuccess, "3"},
		{audit.OutcomeFailure, "5"},
		{audit.OutcomeDenied, "7"},
		{audit.Outcome("unknown"), "10"},
	}

	for _, tt := range tests {
		t.Run(string(tt.outcome), func(t *testing.T) {
			t.Parallel()

			handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
			seedEntry(t, store, "acme", "alice")
			store.entries["acme"][0].Outcome = tt.outcome

			_, lines := cefExport(t, handler)
			if len(lines) != 1 || !strings.Contains(lines[0], "|reveal|"+tt.severity+"|") {
				t.Fatalf("lines %q, want severity %s", lines, tt.severity)
			}
		})
	}
}

// TestExportCEFKeepsKeysApart proves that the end of a value never swallows
// the next key and that no control character leaves raw, in the extension and
// in the header.
func TestExportCEFKeepsKeysApart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      string
	}{
		{"ends in pipe", "curl|", "requestClientApplication=curl| cn1=1 cn1Label=sequence"},
		{"ends in backslash", `curl\`, `requestClientApplication=curl\\ cn1=1 cn1Label=sequence`},
		{"ends in equals", "curl=", `requestClientApplication=curl\= cn1=1 cn1Label=sequence`},
		{"null", "curl\x00x", `requestClientApplication=curl\u0000x cn1=1`},
		{"tab", "curl\tx", `requestClientApplication=curl\u0009x cn1=1`},
		{"delete", "curl\x7fx", `requestClientApplication=curl\u007Fx cn1=1`},
		{"line separator", "curl\u2028x", `requestClientApplication=curl\u2028x cn1=1`},
		{"paragraph separator", "curl\u2029x", `requestClientApplication=curl\u2029x cn1=1`},
		{"literal escape text", `curl\u0000`, `requestClientApplication=curl\\u0000 cn1=1`},
		// The escape is lossy for a byte that is not valid UTF-8: the range
		// over the value reads the byte as utf8.RuneError, and isCEFControl
		// writes that as \uFFFD. The byte itself is gone.
		{"curlxffx", "curl\xffx", `requestClientApplication=curl\uFFFDx cn1=1 cn1Label=sequence`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, store := newEnforcingHandler(t, &recordingAuthorizer{calls: nil})
			seedEntry(t, store, "acme", "alice")
			store.entries["acme"][0].UserAgent = tt.userAgent
			store.entries["acme"][0].Action = "unit:" + tt.userAgent

			_, lines := cefExport(t, handler)
			if len(lines) != 1 {
				t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
			}

			if !strings.Contains(lines[0], tt.want) {
				t.Fatalf("line lacks %q:\n%s", tt.want, lines[0])
			}

			if r, raw := rawControl(lines[0]); raw {
				t.Fatalf("line holds the raw control character %U:\n%q", r, lines[0])
			}
		})
	}
}

// rawControl returns the first control character that the line holds.
func rawControl(line string) (rune, bool) {
	for _, r := range line {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) || r == '\u2028' || r == '\u2029' {
			return r, true
		}
	}

	return 0, false
}
