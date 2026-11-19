package pulumi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestStateReadsPastTheBoundAnswer413AndLog proves that each masked read of a
// state past the nesting bound answers 413, and that the server logs one
// warning with the stack and the update, and no value of the state.
func TestStateReadsPastTheBoundAnswer413AndLog(t *testing.T) {
	t.Parallel()

	const planted = "planted-value-must-not-be-logged"

	levels := stack.MaxStateDepth + 50
	deep := strings.Repeat(`{"a":`, levels) + `"` + planted + `"` + strings.Repeat(`}`, levels)
	deployment := `{"resources":[{"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack","outputs":` + deep + `}]}`

	for _, tt := range []struct {
		name   string
		target string
	}{
		{"resource", "/api/stacks/acme/web/prod/state/latest/resources/" + stateURNStack},
		{"outputs", "/api/stacks/acme/web/prod/state/latest/outputs"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			f := loggedUpdateFixture(t, allowAll(), &buf)
			seedStateVersion(t, f, deployment)
			buf.Reset()

			status, body := f.doUpdate(t, accessRequest(http.MethodGet, tt.target, ""))
			if status != http.StatusRequestEntityTooLarge {
				t.Fatalf("GET %s answers %d, want 413 (%s)", tt.target, status, body)
			}

			checkBoundLog(t, buf.Bytes(), planted)
		})
	}
}

// checkBoundLog proves that raw holds one WARN record of the bound with the
// stack and the update, and no value of the state.
func checkBoundLog(t *testing.T, raw []byte, planted string) {
	t.Helper()

	var entry struct {
		Level  string `json:"level"`
		Msg    string `json:"msg"`
		Stack  string `json:"stack"`
		Update string `json:"update"`
	}

	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("log %q: want one JSON line: %v", raw, err)
	}

	if entry.Level != "WARN" || entry.Msg != "state past the masked read bound" {
		t.Errorf("log = %q %q, want the WARN of the bound", entry.Level, entry.Msg)
	}

	if entry.Stack != "acme/web/prod" || entry.Update == "" {
		t.Errorf("log stack %q update %q, want acme/web/prod and an update ID", entry.Stack, entry.Update)
	}

	if strings.Contains(string(raw), planted) {
		t.Errorf("log %q holds a value of the state", raw)
	}
}

// TestStateDiffPastTheWorkBoundAnswer413AndLogs proves that a diff of two
// states whose paths cost more than the work budget answers 413 and logs one
// warning with the stack and the update, and no value of the state. Each side
// is about 249 KB: 20 nested keys of 10,000 bytes over 5,000 leaves that
// differ.
func TestStateDiffPastTheWorkBoundAnswer413AndLogs(t *testing.T) {
	t.Parallel()

	side := func(base int) string {
		var out strings.Builder

		out.WriteString(`{"resources":[{"urn":"` + stateURNStack + `","type":"pulumi:pulumi:Stack","outputs":`)

		for level := range 20 {
			out.WriteString(`{"` + strings.Repeat(string(rune('a'+level)), 10000) + `":`)
		}

		out.WriteString(`[`)

		for i := range 5000 {
			if i > 0 {
				out.WriteString(`,`)
			}

			out.WriteString(strconv.Itoa(base + i))
		}

		out.WriteString(`]` + strings.Repeat(`}`, 20) + `}]}`)

		return out.String()
	}

	var buf bytes.Buffer

	f := loggedUpdateFixture(t, allowAll(), &buf)
	seedStateVersion(t, f, side(0))
	seedStateVersion(t, f, side(100000))
	buf.Reset()

	status, body := f.doUpdate(t, accessRequest(http.MethodGet, "/api/stacks/acme/web/prod/state/diff?from=1&to=2", ""))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("diff answers %d, want 413 (%.200s)", status, body)
	}

	checkBoundLog(t, buf.Bytes(), "100000")
}
