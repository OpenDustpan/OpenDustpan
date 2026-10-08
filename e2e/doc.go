//go:build e2e

// Package e2e drives real Pulumi CLI binaries against an in-process OpenDustpan
// server and records every HTTP request the CLI makes.
//
// The Pulumi CLI API is not a published specification, so this suite is the
// only authority on which endpoints and response shapes the CLI expects. It is
// excluded from `make test` by the e2e build tag; run it with `make e2e`.
//
// Each CLI version in the matrix gets its own server, SQLite database and
// PULUMI_HOME, so versions never share state. CLI archives are downloaded once
// into a cache directory and verified against sha256 checksums before use.
//
// Environment variables:
//
//	E2E_PULUMI_VERSIONS  comma-separated versions, or "latest" (default: see DefaultVersions)
//	E2E_CACHE_DIR        where CLI archives are cached (default: user cache dir)
//	E2E_REPORT_DIR       where the request log and summary are written (default: ./report)
package e2e
