package stategate_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestRefusedLogLineNamesTheJob checks the "state write refused" log line: it
// has the job ID and the phase name of the job claim of the refusal, and both
// fields are empty for a person and for a refusal with no claim. The line
// stays one line.
func TestRefusedLogLineNamesTheJob(t *testing.T) {
	t.Parallel()

	claim := &gate.JobClaim{Job: "42", Phase: "plan", PassedApply: false, Units: nil}
	modelErr := fmt.Errorf("%w: phase plan of job 42 writes only the lock of its plan", identity.ErrDenied)

	cases := []struct {
		name       string
		credential identity.Credential
		err        error
		wantJob    string
		wantPhase  string
	}{
		{
			name: "job token refused by a gate", credential: identity.CredentialJobToken,
			err: &gate.RefusalError{
				Err: gate.ErrDenied, Record: "rec-1", Messages: []string{"Frozen."}, Job: claim,
			},
			wantJob: "42", wantPhase: "plan",
		},
		{
			name: "job token refused by the job model", credential: identity.CredentialJobToken,
			err:     &gate.RefusalError{Err: modelErr, Record: "", Messages: nil, Job: claim},
			wantJob: "42", wantPhase: "plan",
		},
		{
			name: "job token refusal with no claim", credential: identity.CredentialJobToken,
			err:     fmt.Errorf("state.write: %w: job 42 is canceled", identity.ErrDenied),
			wantJob: "", wantPhase: "",
		},
		{
			name: "person", credential: identity.CredentialAccessToken,
			err:     &gate.RefusalError{Err: gate.ErrDenied, Record: "rec-2", Messages: []string{"Frozen."}, Job: nil},
			wantJob: "", wantPhase: "",
		},
		{
			name: "job claim with a line break", credential: identity.CredentialJobToken,
			err: &gate.RefusalError{
				Err: gate.ErrDenied, Record: "rec-3", Messages: nil,
				Job: &gate.JobClaim{Job: "42\nFAKE", Phase: "plan\r\nFAKE: allowed", PassedApply: false, Units: nil},
			},
			wantJob: "42 FAKE", wantPhase: "plan  FAKE: allowed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			guard := &fakeGuard{mu: sync.Mutex{}, decision: decision("", ""), err: tc.err, got: nil}

			a, err := stategate.New(guard, slog.New(slog.NewJSONHandler(&buf, nil)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			rec := &recorder{mu: sync.Mutex{}, reasons: nil}

			out := a.Admit(t.Context(), write(t, gate.OperationLock, principal(tc.credential, "42")), rec.record)
			checkRefused(t, out, rec, "gate.denied")

			line := refusedLine(t, buf.String())

			if line["job"] != tc.wantJob || line["phase"] != tc.wantPhase {
				t.Fatalf("job, phase = %q, %q, want %q, %q", line["job"], line["phase"], tc.wantJob, tc.wantPhase)
			}
		})
	}
}

// TestRefusedLogLineCarriesTheGateMessages checks that the "state write
// refused" log line has the gate messages of the refusal, with each control
// and format character removed and each message cut to 256 runes. The
// messages are hostile, as if the gate and the log handler did not clean
// them.
func TestRefusedLogLineCarriesTheGateMessages(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", 300)

	// The right-to-left override, the line separator and the paragraph
	// separator, built from their code points.
	formatted := "Fro" + string(rune(0x202e)) + "zen." + string(rune(0x2028)) + string(rune(0x2029))

	cases := []struct {
		name     string
		messages []string
		want     []string
	}{
		{name: "plain message", messages: []string{"Frozen."}, want: []string{"Frozen."}},
		{
			name:     "CR and LF",
			messages: []string{"Frozen.\r\nlevel=INFO msg=\"state write allowed\""},
			want:     []string{"Frozen.level=INFO msg=\"state write allowed\""},
		},
		{
			name:     "ANSI escape and other control characters",
			messages: []string{"\x1b[2J\x1b[31mFrozen.\x1b[0m\x00\t\x7f" + string(rune(0x85))},
			want:     []string{"[2J[31mFrozen.[0m"},
		},
		{
			name:     "format and line separator characters",
			messages: []string{formatted},
			want:     []string{"Frozen."},
		},
		{
			name:     "long message",
			messages: []string{long},
			want:     []string{strings.Repeat("é", 256) + "..."},
		},
		{
			name:     "two messages",
			messages: []string{"Frozen.\n", "\x1bOn call."},
			want:     []string{"Frozen.", "On call."},
		},
		{name: "no message", messages: nil, want: []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			guard := &fakeGuard{
				mu: sync.Mutex{}, decision: decision("", ""), got: nil,
				err: &gate.RefusalError{Err: gate.ErrDenied, Record: "rec-1", Messages: tc.messages, Job: nil},
			}

			a, err := stategate.New(guard, slog.New(slog.NewJSONHandler(&buf, nil)))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			rec := &recorder{mu: sync.Mutex{}, reasons: nil}

			person := principal(identity.CredentialAccessToken, "")
			out := a.Admit(t.Context(), write(t, gate.OperationLock, person), rec.record)
			checkRefused(t, out, rec, "gate.denied")

			if got := refusedMessages(t, buf.String()); !slices.Equal(got, tc.want) {
				t.Fatalf("messages = %q, want %q", got, tc.want)
			}
		})
	}
}

// refusedMessages returns the messages field of the one "state write
// refused" line of the JSON log text.
func refusedMessages(t *testing.T, text string) []string {
	t.Helper()

	raw, ok := refusedLine(t, text)["messages"].([]any)
	if !ok {
		t.Fatalf("log line has no messages list: %q", text)
	}

	got := make([]string, 0, len(raw))

	for _, m := range raw {
		s, isString := m.(string)
		if !isString {
			t.Fatalf("message %v is not a string", m)
		}

		got = append(got, s)
	}

	return got
}

// refusedLine returns the fields of the one "state write refused" line of
// the JSON log text, and fails t unless the text has that line one time.
func refusedLine(t *testing.T, text string) map[string]any {
	t.Helper()

	var found []map[string]any

	for raw := range strings.SplitSeq(strings.TrimSpace(text), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatalf("log line %q: %v", raw, err)
		}

		if fields["msg"] == "state write refused" {
			found = append(found, fields)
		}
	}

	if len(found) != 1 {
		t.Fatalf("log = %q, want one state write refused line", text)
	}

	for _, key := range []string{"job", "phase"} {
		if _, ok := found[0][key]; !ok {
			t.Fatalf("log line %v has no field %q", found[0], key)
		}
	}

	return found[0]
}
