//go:build e2e

// Package e2e drives real Pulumi CLI and OpenTofu binaries against an
// in-process OpenDustpan server and records every HTTP request they make.
//
// Neither the Pulumi CLI API nor the TFE-compatible API that OpenTofu's cloud
// block speaks is a published specification, so this suite is the only
// authority on which endpoints and response shapes the clients expect. It is
// excluded from `make test` by the e2e build tag; run it with `make e2e`.
//
// Each CLI version in the matrix gets its own server, SQLite database and home
// directory, so versions never share state. OpenTofu gets an HTTPS server,
// because its service discovery refuses plain HTTP; that part runs on Linux
// only. CLI archives are downloaded once into a cache directory and verified
// against sha256 checksums before use.
//
// Environment variables:
//
//	E2E_PULUMI_VERSIONS  comma-separated versions, or "latest" (default: see Pulumi)
//	E2E_TOFU_VERSIONS    comma-separated versions, or "latest" (default: see OpenTofu)
//	E2E_CACHE_DIR        where CLI archives are cached (default: user cache dir)
//	E2E_REPORT_DIR       where the request log and summary are written (default: ./report)
package e2e
