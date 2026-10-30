package issuersvc_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/issuersvc"
)

// logLine is one line of the JSON log.
type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Type  string `json:"type"`
	NS    string `json:"namespace"`
}

// logLines decodes the JSON lines in buf.
func logLines(t *testing.T, buf *bytes.Buffer) []logLine {
	t.Helper()

	var out []logLine

	for raw := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}

		var l logLine
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}

		out = append(out, l)
	}

	return out
}

// TestANoticeThatHasNoOutboxIsLogged proves that the issuer does not drop a
// notice in silence: with no outbox, the server logs each notice that it would
// send, at WARN for a revoked key and at INFO for a new next key, and the read
// of the issuer says that the notices are off.
func TestANoticeThatHasNoOutboxIsLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	h := newHarness(t, func(c *issuersvc.Config, _ *harness) {
		c.Notifier = nil
		c.Log = slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	})

	h.createKeys()

	_, err := h.svc.Rotate(t.Context(), h.admin, h.ns.Name,
		issuersvc.RotateRequest{Reason: "the store was copied", Emergency: true})
	if err != nil {
		t.Fatalf("emergency rotation: %v", err)
	}

	want := map[string]string{"issuer.key-revoked": "WARN", "issuer.key-next": "INFO"}
	seen := map[string]bool{}

	for _, l := range logLines(t, &buf) {
		if !strings.Contains(l.Msg, "the notice was not sent") {
			continue
		}

		if l.NS != "acme" {
			t.Errorf("the log line names the namespace %q, want acme", l.NS)
		}

		if level, ok := want[l.Type]; ok && l.Level == level {
			seen[l.Type] = true
		}
	}

	for typ := range want {
		if !seen[typ] {
			t.Errorf("no log line for the skipped notice %s at level %s: %s", typ, want[typ], buf.String())
		}
	}

	st, err := h.svc.Get(t.Context(), h.admin, h.ns.Name)
	if err != nil || st.NoticesOn {
		t.Errorf("Get = notices on %v, %v; want off", st.NoticesOn, err)
	}
}

func TestTheReadSaysThatTheNoticesAreOnWithAnOutbox(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	st, err := h.svc.Get(t.Context(), h.admin, h.ns.Name)
	if err != nil || !st.NoticesOn {
		t.Errorf("Get = notices on %v, %v; want on", st.NoticesOn, err)
	}
}
