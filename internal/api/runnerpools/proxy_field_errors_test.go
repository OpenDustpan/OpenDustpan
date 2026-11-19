package runnerpools_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestProxySettingsSaveNamesTheInvalidField proves that a save with one
// invalid field answers 400 with the field and the rule, and never the input.
func TestProxySettingsSaveNamesTheInvalidField(t *testing.T) {
	t.Parallel()

	const prefix = "runner request is not valid: "

	manyRanges := `"10.0.0.0/8"` + strings.Repeat(`,"10.0.0.0/8"`, 64)

	tests := []struct {
		name string
		body string
		want string
	}{
		{"range is not an address", `{"allowedRanges":["bad"]}`, "allowedRanges: "},
		{"range has a host bit", `{"allowedRanges":["10.0.0.1/8"]}`, "allowedRanges: "},
		{"range is too wide", `{"allowedRanges":["0.0.0.0/0"]}`, "allowedRanges: "},
		{"65 ranges", `{"allowedRanges":[` + manyRanges + `]}`, "allowedRanges: use 0 to 64 ranges."},
		{"upstream scheme", `{"upstreamProxy":"ftp://x"}`, "upstreamProxy: "},
		{"upstream does not parse", `{"upstreamProxy":"http://[::1"}`, "upstreamProxy: "},
		{"upstream user information", `{"upstreamProxy":"http://u:p@proxy.example.com:3128"}`, "upstreamProxy: "},
		{"upstream fragment", `{"upstreamProxy":"http://proxy.example.com#x"}`, "upstreamProxy: "},
		{"upstream has no host", `{"upstreamProxy":"http://"}`, "upstreamProxy: "},
		{"upstream literal host", `{"upstreamProxy":"http://127.0.0.1:3128"}`, "upstreamProxy: "},
		{
			"credential with no upstream",
			`{"allowedRanges":[],"upstreamProxy":"","upstreamCredential":{"username":"u","password":"p"}}`,
			"upstreamCredential: ",
		},
		{
			"user name with a colon",
			`{"upstreamProxy":"https://proxy.example.com","upstreamCredential":{"username":"a:b","password":"p"}}`,
			"upstreamCredential.username: ",
		},
		{
			"user name empty",
			`{"upstreamProxy":"https://proxy.example.com","upstreamCredential":{"username":"","password":"p"}}`,
			"upstreamCredential.username: ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSettingsEnv(t, identity.AdminOnly{})
			status, _, body := e.do(put(tc.body))

			var got struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}

			decode(t, body, &got)

			if status != http.StatusBadRequest || !strings.HasPrefix(got.Message, prefix+tc.want) ||
				got.Message == strings.TrimSuffix(prefix, ": ") {
				t.Fatalf("status %d message %q, want 400 and the prefix %q", status, got.Message, prefix+tc.want)
			}

			if got.ErrorCode != errcode.RunnerInvalid {
				t.Errorf("errorCode = %q, want %q", got.ErrorCode, errcode.RunnerInvalid)
			}
		})
	}
}

// TestProxySettingsSaveNeverEchoesHostileInput is the table of hostile values
// of the handler layer. An answer for any of them holds none of the value.
func TestProxySettingsSaveNeverEchoesHostileInput(t *testing.T) {
	t.Parallel()

	const marker = "ZZ-marker"

	tests := []struct {
		name string
		body string
	}{
		{"range text", `{"allowedRanges":["` + marker + `\r\nX: y"]}`},
		{"range with a host bit", `{"allowedRanges":["10.0.0.1/8` + marker + `"]}`},
		{"upstream scheme", `{"upstreamProxy":"` + marker + `://x"}`},
		{"upstream text", `{"upstreamProxy":"http://` + marker + `\u0000@x"}`},
		{
			"user name with a colon",
			`{"upstreamProxy":"https://proxy.example.com","upstreamCredential":{"username":"` + marker +
				`:x","password":"` + marker + `"}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			e := newSettingsEnv(t, identity.AdminOnly{})
			status, _, body := e.do(put(tc.body))

			if status != http.StatusBadRequest {
				t.Fatalf("status %d %s, want 400", status, body)
			}

			if strings.Contains(string(body), marker) {
				t.Errorf("the answer holds the input: %s", body)
			}
		})
	}
}

// TestJoinTokenCreateNamesTheTTLField proves that a ttl above the limit and a
// negative ttl answer 400 with the field and the rule.
func TestJoinTokenCreateNamesTheTTLField(t *testing.T) {
	t.Parallel()

	const want = "runner request is not valid: ttl: use 0, or 1 to 604800000 milliseconds."

	for name, ttl := range map[string]int64{"above 7 days": 604_800_001, "negative": -1} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			e := newEnv(t)
			pool := e.createPool()

			status, body := e.admin(http.MethodPost, poolsPath+"/"+pool+"/join-tokens", map[string]any{"ttl": ttl})

			var got struct {
				Message   string `json:"message"`
				ErrorCode string `json:"errorCode"`
			}

			decode(t, body, &got)

			if status != http.StatusBadRequest || got.Message != want || got.ErrorCode != errcode.RunnerInvalid {
				t.Fatalf("status %d answer %s, want 400 with %q", status, body, want)
			}
		})
	}
}
