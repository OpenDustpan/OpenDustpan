package runner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
// the server writes for a phase or a sweep, and the highest that this
// release of a runner reads. The server stores it as the version of each
// durable operation of a runner kind. A runner sends the highest version
// that it reads, and the claim never takes an operation above it
// (durable.ClaimRequest.MaxVersions). So a new field that an old launcher
// cannot honor never reaches an old launcher.
const AssignmentVersion = 1

// Release is a release of OpenDustpan: major, minor and patch, and an
// optional pre-release tag. A runner, a launcher and the server of one
// release have one Release.
type Release struct {
	Major int
	Minor int
	Patch int
	// Pre is the pre-release tag after '-', for example "rc.1", or empty.
	// CheckSkew compares the base version only, so a pre-release counts as
	// its base version.
	Pre string
}

// releaseParts is the number of parts of a release.
const releaseParts = 3

var preRelease = regexp.MustCompile(`^[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*$`)

// ParseRelease parses "1.2.3" or "v1.2.3", with an optional pre-release
// tag after '-', for example "1.2.3-rc.1", and an optional build tag after
// '+', which it drops. It returns ErrInvalid for any other text.
func ParseRelease(s string) (Release, error) {
	bad := fmt.Errorf("%w: release %q is not major.minor.patch", ErrInvalid, s)

	rest, build, hasBuild := strings.Cut(strings.TrimPrefix(s, "v"), "+")
	base, pre, hasPre := strings.Cut(rest, "-")

	if (hasBuild && !preRelease.MatchString(build)) || (hasPre && !preRelease.MatchString(pre)) {
		return Release{}, bad
	}

	parts := strings.Split(base, ".")
	if len(parts) != releaseParts {
		return Release{}, bad
	}

	var n [releaseParts]int

	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || (len(p) > 1 && p[0] == '0') {
			return Release{}, bad
		}

		n[i] = v
	}

	return Release{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre}, nil
}

// String returns the release as major.minor.patch, with "-" and the
// pre-release tag when it has one.
func (r Release) String() string {
	if r.Pre != "" {
		return fmt.Sprintf("%d.%d.%d-%s", r.Major, r.Minor, r.Patch, r.Pre)
	}

	return fmt.Sprintf("%d.%d.%d", r.Major, r.Minor, r.Patch)
}

// CheckSkew checks that the server of release server accepts a runner or a
// launcher of release client. The skew counts minor releases inside one
// major release: client may be the release of the server or up to MaxSkew
// minor releases older. A patch release and a pre-release tag never count.
// It returns:
//   - ErrVersionAhead for a client of a newer minor or major release than
//     the server. A rolling deploy or a rollback of the server causes it,
//     so the client retries with backoff.
//   - ErrVersionUnsupported, with the range that the server accepts, for a
//     client more than MaxSkew minor releases older, or of an older major
//     release.
func CheckSkew(server, client Release) error {
	lowest := max(server.Minor-MaxSkew, 0)

	switch {
	case client.Major > server.Major || (client.Major == server.Major && client.Minor > server.Minor):
		return fmt.Errorf("%w: release %s, the server is %s", ErrVersionAhead, client, server)
	case client.Major != server.Major || client.Minor < lowest:
		return fmt.Errorf("%w: release %s, the server %s accepts %d.%d.x to %d.%d.x",
			ErrVersionUnsupported, client, server, server.Major, lowest, server.Major, server.Minor)
	default:
		return nil
	}
}

// VersionAheadLimit is the longest time for which a runner or a launcher
// retries after ErrVersionAhead. A rolling deploy of the server ends in a
// few minutes. A server that stays older for longer was rolled back, and
// the client must go back to a release that the server accepts.
const VersionAheadLimit = 15 * time.Minute

// CheckVersionAhead checks a retry at now of a client that got
// ErrVersionAhead first at since, with no answer from the server between.
// It returns nil while the client may retry, and ErrVersionUnsupported
// after VersionAheadLimit. The runner then stops its polls, and its log
// and status show version-unsupported with the release of the server.
// The launcher ends with the exit code of a refused release. A launcher
// never waits past MaxSessionWait either.
func CheckVersionAhead(since, now time.Time) error {
	if now.Sub(since) > VersionAheadLimit {
		return fmt.Errorf("%w: the server stayed older than this release for %v", ErrVersionUnsupported,
			VersionAheadLimit)
	}

	return nil
}
