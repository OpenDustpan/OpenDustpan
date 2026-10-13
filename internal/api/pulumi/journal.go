package pulumi

import (
	"strconv"
	"strings"
)

// The journal gate of ADR 43, decisions 2 and 5. It decides which journal
// version a start answers.

// oldestVerifiedJournalCLI is the oldest Pulumi CLI release that the gate lets
// through (ADR 43, decision 5.3). The value comes from the research note of RFC
// 64.
func oldestVerifiedJournalCLI() cliVersion {
	return cliVersion{3, 250, 0}
}

// newestVerifiedJournalCLI is the newest Pulumi CLI release that the gate lets
// through (ADR 43, decision 5.2). The value comes from the research note of RFC
// 64. The replay corpus of work item 7 must check each release before this
// value moves.
func newestVerifiedJournalCLI() cliVersion {
	return cliVersion{3, 268, 0}
}

// cliVersionParts is the number of parts in a release version: major, minor
// and patch.
const cliVersionParts = 3

// journalCLIPrefix starts the User-Agent that the Pulumi CLI sends, for
// example "pulumi-cli/1 (v3.268.0; linux)".
const journalCLIPrefix = "pulumi-cli/1 ("

// cliVersion is a release version: major, minor and patch.
type cliVersion [3]int

// less reports whether v comes before w.
func (v cliVersion) less(w cliVersion) bool {
	for i := range v {
		if v[i] != w[i] {
			return v[i] < w[i]
		}
	}

	return false
}

// parseCLIVersion parses a release version such as v3.268.0. The leading v is
// optional. It rejects a pre-release, a build suffix and any other form.
func parseCLIVersion(s string) (cliVersion, bool) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != cliVersionParts {
		return cliVersion{}, false
	}

	var v cliVersion

	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return cliVersion{}, false
		}

		n, err := strconv.Atoi(part)
		if err != nil {
			return cliVersion{}, false
		}

		v[i] = n
	}

	return v, true
}

// cliVersionOf reads the release version from the User-Agent that the Pulumi
// CLI sends. It returns false for any other User-Agent. The gate is not a
// security control: a client can send any value (ADR 43, decision 5.6).
func cliVersionOf(userAgent string) (cliVersion, bool) {
	rest, ok := strings.CutPrefix(userAgent, journalCLIPrefix)
	if !ok {
		return cliVersion{}, false
	}

	end := strings.IndexAny(rest, ";)")
	if end < 0 {
		return cliVersion{}, false
	}

	return parseCLIVersion(strings.TrimSpace(rest[:end]))
}

// negotiateJournalVersion answers the journal version of a start. It answers 0
// when the CLI offered no version, or when the gate rejects the CLI. Otherwise
// it answers the highest version that both the CLI offered and the server
// supports (ADR 43, decisions 2.1 and 5.4). The caller answers 0 when
// journaling is off, before it calls this function.
func negotiateJournalVersion(userAgent string, offered, supported int) int {
	if offered <= 0 || supported <= 0 {
		return 0
	}

	v, ok := cliVersionOf(userAgent)
	if !ok || v.less(oldestVerifiedJournalCLI()) || newestVerifiedJournalCLI().less(v) {
		return 0
	}

	return min(offered, supported)
}

// startUpdateOffer is the part of the start-update body that the gate reads:
// the highest journal version that the CLI offers. An absent field offers none.
type startUpdateOffer struct {
	JournalVersion int `json:"journalVersion"`
}
