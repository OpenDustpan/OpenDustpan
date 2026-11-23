package runnerpools_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
)

const poolsPath = "/api/dustpan/v1/namespaces/acme/runner-pools"

// TestPoolCreateNamesTheInvalidField proves that a create with one invalid
// field answers 400 with the field and the rule, and never the input.
func TestPoolCreateNamesTheInvalidField(t *testing.T) {
	t.Parallel()

	const wantPrefix = "runner request is not valid: "

	tests := []struct {
		name string
		body map[string]any
		want string
	}{
		{
			"name with a capital",
			map[string]any{"name": "Prod", "purpose": "jobs"},
			"name: use 1 to 63 lowercase letters, digits and '-'. Start and end with a letter or digit.",
		},
		{
			"name empty",
			map[string]any{"name": "", "purpose": "jobs"},
			"name: use 1 to 63 lowercase letters, digits and '-'. Start and end with a letter or digit.",
		},
		{"purpose missing", map[string]any{"name": "prod"}, "purpose: use jobs or sweeps."},
		{"purpose unknown", map[string]any{"name": "prod", "purpose": "scan"}, "purpose: use jobs or sweeps."},
		{
			"purpose sweeps on a namespace pool",
			map[string]any{"name": "prod", "purpose": "sweeps"},
			"purpose: use jobs. A namespace pool cannot run sweeps.",
		},
		{
			"lease too short",
			map[string]any{"name": "prod", "purpose": "jobs", "lease": 1000},
			"lease: use 0, or 30000 to 600000 milliseconds.",
		},
		{
			"lease too long",
			map[string]any{"name": "prod", "purpose": "jobs", "lease": 700000},
			"lease: use 0, or 30000 to 600000 milliseconds.",
		},
		{
			"concurrency too high",
			map[string]any{"name": "prod", "purpose": "jobs", "concurrency": 65},
			"concurrency: use 0 to 64 phases.",
		},
		{
			"concurrency negative",
			map[string]any{"name": "prod", "purpose": "jobs", "concurrency": -1},
			"concurrency: use 0 to 64 phases.",
		},
		{
			"isolation unknown",
			map[string]any{"name": "prod", "purpose": "jobs", "isolation": []string{"vm"}},
			"isolation: use kubernetes, firecracker, gvisor or process.",
		},
		{
			"git credential mode unknown",
			map[string]any{
				"name": "prod", "purpose": "jobs", "gitCredentials": map[string]any{"mode": "magic"},
			},
			"gitCredentials.mode: use none, forge or custom.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			status, body := e.admin(http.MethodPost, poolsPath, tc.body)

			var got struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}

			decode(t, body, &got)

			if status != http.StatusBadRequest || got.Message != wantPrefix+tc.want {
				t.Fatalf("status %d message %q, want 400 and %q", status, got.Message, wantPrefix+tc.want)
			}

			if got.ErrorCode != errcode.RunnerInvalid {
				t.Errorf("errorCode = %q, want %q", got.ErrorCode, errcode.RunnerInvalid)
			}
		})
	}
}

// TestPoolCreateNeverEchoesHostileInput is the table of hostile values of the
// handler layer. An answer for any of them holds none of the value.
func TestPoolCreateNeverEchoesHostileInput(t *testing.T) {
	t.Parallel()

	marker := "ZZ-marker"
	tests := []struct {
		name  string
		field string
		value any
	}{
		{"name with CR and LF", "name", marker + "\r\nSet-Cookie: x=1"},
		{"name with NUL", "name", marker + "\x00"},
		{"name with unicode", "name", marker + "\u202eé"},
		{"name of 10 KB", "name", marker + strings.Repeat("a", 10<<10)},
		{"purpose with CR and LF", "purpose", marker + "\r\nX: y"},
		{"purpose with NUL", "purpose", marker + "\x00"},
		{"purpose of 10 KB", "purpose", marker + strings.Repeat("p", 10<<10)},
		{"isolation with CR and LF", "isolation", []string{marker + "\r\n"}},
		{"isolation of 10 KB", "isolation", []string{marker + strings.Repeat("i", 10<<10)}},
		{"mode with NUL", "gitCredentials", map[string]any{"mode": marker + "\x00"}},
		{"mode with unicode", "gitCredentials", map[string]any{"mode": marker + "\u202e"}},
		{"lease negative", "lease", -9223372036854775807},
		{"lease huge", "lease", 9223372036854775807},
		{"concurrency negative", "concurrency", -9223372036854775807},
		{"concurrency huge", "concurrency", 9223372036854775807},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := map[string]any{"name": "prod", "purpose": "jobs"}
			body[tc.field] = tc.value

			e := newEnv(t)
			status, answer := e.admin(http.MethodPost, poolsPath, body)

			if status != http.StatusBadRequest && status != http.StatusRequestEntityTooLarge {
				t.Fatalf("status %d %q, want 400", status, truncate(answer))
			}

			checkNoEcho(t, answer)

			var got struct {
				Message string `json:"message"`
			}

			if json.Unmarshal(answer, &got) != nil || len(got.Message) > 200 {
				t.Errorf("message is not short JSON text: %q", truncate(answer))
			}
		})
	}
}

func truncate(b []byte) string {
	if len(b) > 200 {
		return string(b[:200]) + "..."
	}

	return string(b)
}

// checkNoEcho fails when the answer holds a part of a hostile input. It checks
// the raw bytes and the decoded message. The JSON encoder escapes a CR, a NUL
// and a right-to-left mark, so only the decoded message can show an echo of
// one of them.
func checkNoEcho(t *testing.T, answer []byte) {
	t.Helper()

	var decoded struct {
		Message string `json:"message"`
	}

	if err := json.Unmarshal(answer, &decoded); err != nil {
		t.Errorf("the answer is not JSON: %q", truncate(answer))

		return
	}

	for _, bad := range []string{"ZZ-marker", "\r", "\x00", "9223372036854775807", "\u202e"} {
		if strings.Contains(string(answer), bad) {
			t.Errorf("the answer holds %q of the input: %q", bad, truncate(answer))
		}
	}

	// The decoded message must hold no control byte and no part of an input.
	for _, bad := range []string{"ZZ-marker", "\r", "\n", "\x00", "9223372036854775807", "\u202e"} {
		if strings.Contains(decoded.Message, bad) {
			t.Errorf("the decoded message holds %q of the input: %q", bad, truncate(answer))
		}
	}
}
