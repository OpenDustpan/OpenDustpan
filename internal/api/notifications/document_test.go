package notifications_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	apinotifications "opendustpan.org/opendustpan/internal/api/notifications"
	"opendustpan.org/opendustpan/pkg/notify"
)

// documentWithLog returns the JSON that the API answers for s and the log of
// the conversion, so a test checks the diagnostic of a target that the
// answer leaves out.
func documentWithLog(t *testing.T, s *notify.Sink) ([]byte, *bytes.Buffer) {
	t.Helper()

	logs := &bytes.Buffer{}

	body, err := apinotifications.DocumentJSONTo(slog.New(slog.NewTextHandler(logs, nil)), s)
	if err != nil {
		t.Fatalf("DocumentJSONTo: %v", err)
	}

	return body, logs
}

// checkTargetLog checks the log of the conversion of s against shown, the
// target that the answer shows: an empty target wants one diagnostic that
// names the sink and holds no target or token; any other target wants no
// diagnostic.
func checkTargetLog(t *testing.T, what string, logs *bytes.Buffer, s *notify.Sink, shown string) {
	t.Helper()

	got := logs.String()

	if shown != "" {
		if logs.Len() != 0 {
			t.Errorf("%s: log = %q, want no diagnostic", what, got)
		}

		return
	}

	if !strings.Contains(got, s.ID) || strings.Contains(got, canary) || strings.Contains(got, s.Target) {
		t.Errorf("%s: log = %q, want a diagnostic that names %s and no target", what, got, s.ID)
	}
}

// storedSink returns a stored sink of the kind with the target. A chat sink
// has the platform mattermost, and only an email sink has no secret item.
func storedSink(kind notify.SinkKind, target string) notify.Sink {
	s := notify.Sink{
		ID: "snk-1", Scope: "acme", Kind: kind, Name: "Ops", Platform: "", Target: target,
		SecretRefs: []string{"itm-1", "itm-2"}, CreatedBy: "ana", Version: 1,
	}

	if kind == notify.SinkChat {
		s.Platform = "mattermost"
	}

	if kind == notify.SinkEmail {
		s.SecretRefs = nil
	}

	return s
}

// TestDocumentFlagsAStoredHostThatNeedsAnUpdate pins the answer of a stored
// sink whose host fails the allowlist of a new write: the answer has
// hostNeedsUpdate true, an empty target, and a diagnostic that names the
// sink and holds no target. A sink whose host passes the allowlist, and an
// email sink, have no hostNeedsUpdate field and no diagnostic.
func TestDocumentFlagsAStoredHostThatNeedsAnUpdate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		sink       notify.Sink
		wantTarget string
		wantFlag   bool
	}{
		{"a webhook host with an underscore", storedSink(notify.SinkWebhook, "hk_1.example.com"), "", true},
		{"a webhook port with leading zeros", storedSink(notify.SinkWebhook, "hk.example.com:0443"), "", true},
		{"a chat host with an underscore", storedSink(notify.SinkChat, "mm_chat.example.com"), "", true},
		{"a chat host with a trailing dot", storedSink(notify.SinkChat, "mm.example.com."), "", true},
		{
			"a webhook host that passes",
			storedSink(notify.SinkWebhook, "hooks.example.com"), "hooks.example.com", false,
		},
		{
			"a chat host that passes",
			storedSink(notify.SinkChat, "mm.example.com:8443"), "mm.example.com:8443", false,
		},
		{"an email address", storedSink(notify.SinkEmail, "ops@example.com"), "ops@example.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body, logs := documentWithLog(t, &tc.sink)

			var doc struct {
				Target          string `json:"target"`
				HostNeedsUpdate *bool  `json:"hostNeedsUpdate"`
			}
			if err := json.Unmarshal(body, &doc); err != nil {
				t.Fatalf("decode %s: %v", body, err)
			}

			// The field is in the answer only when it is true.
			if got := doc.HostNeedsUpdate != nil && *doc.HostNeedsUpdate; got != tc.wantFlag ||
				(doc.HostNeedsUpdate != nil) != tc.wantFlag {
				t.Errorf("answer = %s, want hostNeedsUpdate %t", body, tc.wantFlag)
			}

			if doc.Target != tc.wantTarget {
				t.Errorf("answer = %s, want the target %q", body, tc.wantTarget)
			}

			checkTargetLog(t, tc.name, logs, &tc.sink, tc.wantTarget)
		})
	}
}
