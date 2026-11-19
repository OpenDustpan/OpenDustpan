package accesstest

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
)

// GrantID returns a valid grant ID that depends only on seed, for a test that
// builds a grant from a text that holds characters that a grant ID refuses,
// for example the text form of an object.
func GrantID(seed string) string {
	sum := sha256.Sum256([]byte(seed))

	return access.GrantIDPrefix + hex.EncodeToString(sum[:16])
}

// HostileGrantIDs holds texts that are not a grant ID: each layer that takes
// a grant ID from outside the process must refuse every one of them.
var HostileGrantIDs = []string{
	"", "grt-", "grt", "g", "GRT-abc", "grp-abc", "prn-abc", " grt-abc", "grt-abc ", "grt-a b",
	"grt-a\r\nX-Injected: 1", "grt-a\nb", "grt-a\x00b", "grt-a/b", "grt-a:b", "grt-a%00", "grt-a'b",
	"grt-a;DROP TABLE grants", "../grt-abc", "grt-é", "grt-" + strings.Repeat("a", 61),
	strings.Repeat("x", 4096),
}
