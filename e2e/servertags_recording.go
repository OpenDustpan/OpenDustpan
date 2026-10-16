//go:build e2e && journalrecording

package e2e

// serverBuildTags are the build tags of the server binary. A suite built
// with journal recording builds a server with journal recording, so the
// journal steps run against the same server in both server modes.
const serverBuildTags = "journalrecording"
