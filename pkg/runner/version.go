package runner

import (
	"fmt"
	"strconv"
	"strings"
)

// ProtocolVersion is the version of the runner protocol in the path
// /runner/v1. A change that an older runner cannot read needs a new path.
// Inside one path, the server and the runner add fields and never remove
// or rename one.
const ProtocolVersion = 1

// MaxSkew is the number of releases by which a runner or a launcher may be
// older than the server: a server of release N accepts N, N-1 and N-2.
const MaxSkew = 2

// AssignmentVersion is the version of the assignment that this release of
// the server writes for a phase or a scan, and the highest that this
// release of a runner reads. The server stores it as the version of each
// durable operation of a runner kind. A runner sends the highest version
// that it reads, and the claim never takes an operation above it
// (durable.ClaimRequest.MaxVersions). So a new field that an old launcher
// cannot honor never reaches an old launcher.
const AssignmentVersion = 1

// Release is a release of OpenDustpan: major, minor and patch. A runner, a
// launcher and the server of one release have one Release.
type Release struct {
	Major int
	Minor int
	Patch int
}

// releaseParts is the number of parts of a release.
const releaseParts = 3

// ParseRelease parses "1.2.3" or "v1.2.3". It returns ErrInvalid for any
// other text.
func ParseRelease(s string) (Release, error) {
	parts := strings.Split(strings.TrimPrefix(s, "v"), ".")
	if len(parts) != releaseParts {
		return Release{}, fmt.Errorf("%w: release %q is not major.minor.patch", ErrInvalid, s)
	}

	var n [releaseParts]int

	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || (len(p) > 1 && p[0] == '0') {
			return Release{}, fmt.Errorf("%w: release %q is not major.minor.patch", ErrInvalid, s)
		}

		n[i] = v
	}

	return Release{Major: n[0], Minor: n[1], Patch: n[2]}, nil
}

// String returns the release as major.minor.patch.
func (r Release) String() string {
	return fmt.Sprintf("%d.%d.%d", r.Major, r.Minor, r.Patch)
}

// CheckSkew checks that the server of release server accepts a runner or a
// launcher of release client. The skew counts minor releases inside one
// major release: client may be the release of the server or up to MaxSkew
// minor releases older. A patch release never counts. It returns
// ErrVersionUnsupported, with the range that the server accepts, for a
// client that is newer than the server, more than MaxSkew minor releases
// older, or of another major release. The rolling deploys of the server
// have the same number, so a runner never needs an upgrade before the
// server.
func CheckSkew(server, client Release) error {
	lowest := max(server.Minor-MaxSkew, 0)

	if client.Major != server.Major || client.Minor > server.Minor || client.Minor < lowest {
		return fmt.Errorf("%w: release %s, the server %s accepts %d.%d.x to %d.%d.x",
			ErrVersionUnsupported, client, server, server.Major, lowest, server.Major, server.Minor)
	}

	return nil
}
