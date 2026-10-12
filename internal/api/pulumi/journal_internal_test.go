package pulumi

import "testing"

// TestCLIVersionOf checks the release version that the gate reads from the
// User-Agent of the Pulumi CLI (ADR 43, decision 5.1).
func TestCLIVersionOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userAgent string
		want      cliVersion
		ok        bool
	}{
		{name: "release with OS", userAgent: "pulumi-cli/1 (v3.268.0; linux)", want: cliVersion{3, 268, 0}, ok: true},
		{
			name:      "release with command and agent",
			userAgent: "pulumi-cli/1 (v3.250.0; darwin; cmd=up; agent=x)",
			want:      cliVersion{3, 250, 0},
			ok:        true,
		},
		{name: "no leading v", userAgent: "pulumi-cli/1 (3.260.1; linux)", want: cliVersion{3, 260, 1}, ok: true},
		{name: "empty", userAgent: "", ok: false},
		{name: "browser", userAgent: "Mozilla/5.0 (X11; Linux x86_64)", ok: false},
		{name: "other protocol version", userAgent: "pulumi-cli/2 (v3.268.0; linux)", ok: false},
		{name: "no closing parenthesis", userAgent: "pulumi-cli/1 (v3.268.0", ok: false},
		{name: "development build", userAgent: "pulumi-cli/1 (v3.269.0-dev.0; linux)", ok: false},
		{name: "pre-release suffix", userAgent: "pulumi-cli/1 (v3.268.0-rc1; linux)", ok: false},
		{name: "two parts", userAgent: "pulumi-cli/1 (v3.268; linux)", ok: false},
		{name: "four parts", userAgent: "pulumi-cli/1 (v3.268.0.1; linux)", ok: false},
		{name: "not a number", userAgent: "pulumi-cli/1 (vx.268.0; linux)", ok: false},
		{name: "signed part", userAgent: "pulumi-cli/1 (v+3.268.0; linux)", ok: false},
		{name: "empty version", userAgent: "pulumi-cli/1 (; linux)", ok: false},
		{name: "dev word", userAgent: "pulumi-cli/1 (dev; linux)", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := cliVersionOf(tc.userAgent)
			if ok != tc.ok {
				t.Fatalf("cliVersionOf(%q) ok = %t, want %t", tc.userAgent, ok, tc.ok)
			}

			if ok && got != tc.want {
				t.Fatalf("cliVersionOf(%q) = %v, want %v", tc.userAgent, got, tc.want)
			}
		})
	}
}

// TestNegotiateJournalVersion checks the gate of ADR 43, decisions 2 and 5. The
// production server supports 0 only, so the tests pass the supported version
// in order to exercise the answers 1 and 2.
func TestNegotiateJournalVersion(t *testing.T) {
	t.Parallel()

	const (
		inRange  = "pulumi-cli/1 (v3.260.0; linux)"
		oldest   = "pulumi-cli/1 (v3.250.0; linux)"
		newest   = "pulumi-cli/1 (v3.268.0; linux)"
		tooOld   = "pulumi-cli/1 (v3.249.9; linux)"
		tooNew   = "pulumi-cli/1 (v3.268.1; linux)"
		devBuild = "pulumi-cli/1 (v3.260.0-dev.0; linux)"
		unknown  = "curl/8.0"
	)

	tests := []struct {
		name      string
		userAgent string
		offered   int
		supported int
		disabled  bool
		want      int
	}{
		{
			name:      "production server answers 0",
			userAgent: inRange,
			offered:   1,
			supported: maxSupportedJournalVersion,
			want:      0,
		},
		{
			name:      "offer 2 to production server",
			userAgent: inRange,
			offered:   2,
			supported: maxSupportedJournalVersion,
			want:      0,
		},
		{name: "in range answers the offer", userAgent: inRange, offered: 1, supported: 2, want: 1},
		{name: "answers the lower of offer and support", userAgent: inRange, offered: 2, supported: 1, want: 1},
		{name: "answers 2 when both allow it", userAgent: inRange, offered: 2, supported: 2, want: 2},
		{name: "oldest verified is accepted", userAgent: oldest, offered: 1, supported: 2, want: 1},
		{name: "newest verified is accepted", userAgent: newest, offered: 2, supported: 2, want: 2},
		{name: "below the oldest is rejected", userAgent: tooOld, offered: 1, supported: 2, want: 0},
		{name: "above the newest is rejected", userAgent: tooNew, offered: 1, supported: 2, want: 0},
		{name: "development build is rejected", userAgent: devBuild, offered: 1, supported: 2, want: 0},
		{name: "unknown client is rejected", userAgent: unknown, offered: 1, supported: 2, want: 0},
		{name: "no offer answers 0", userAgent: inRange, offered: 0, supported: 2, want: 0},
		{name: "negative offer answers 0", userAgent: inRange, offered: -1, supported: 2, want: 0},
		{name: "disabled answers 0", userAgent: inRange, offered: 2, supported: 2, disabled: true, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := negotiateJournalVersion(tc.userAgent, tc.offered, tc.disabled, tc.supported)
			if got != tc.want {
				t.Fatalf("negotiateJournalVersion = %d, want %d", got, tc.want)
			}
		})
	}
}
