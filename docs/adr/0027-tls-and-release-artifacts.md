# 27. TLS and release artifacts

Status: accepted · 2026-10-11. Decision 2 superseded in part by ADR 212. Decision 8 (the Helm chart) decided by ADR 246

Changed by:

- ADR 212 changes decision 2: trusted proxies replace never trust X-Forwarded
  headers.
- ADR 246 extends decision 8: decides the Helm chart item.
- ADR 278 changes decision 6: only Linux and macOS archives, no zip archive.
- ADR 283 changes decision 8: no ACME, OS packages, signing or SBOM in v0.1.0.
- ADR 283 extends decision 7: the owner says yes to buildx and QEMU actions.

## Context

[RFC 35](../rfc/0035-packaging.md) asks where TLS ends and how the project
ships the server. OpenTofu refuses plain HTTP for service discovery (ADR 25).
The exit test of milestone 2 runs over TLS. A brief of 2026-10-11 informed
this ADR. This ADR decides the TLS, binary and image part of RFC 35. The Helm
chart stays in RFC 35.

The binary has no TLS flag and no `--version` flag. `Server.Run` calls
`ListenAndServe`. The server has no setting for a public URL and does not read
`X-Forwarded-*` headers. ADR 21 asks for binary archives with checksums and
for an image with the console for linux/amd64 and linux/arm64. It also asks
for a console release first. ADR 8 keeps the console in its own repository.
CLAUDE.md asks for the owner's yes before the project adds a dependency or a
tool.

The project owner delegated this decision to an agent session on 2026-10-10.
The owner allowed the reviewed recommendation of the agent to become the
decision. The owner can supersede it with a new ADR.

## Decision

1. **Built-in TLS from files.** The flags `--tls-cert` and `--tls-key` give a
   certificate file and a key file. The server uses `crypto/tls` from the
   standard library. The minimum version is TLS 1.2. The server adds no
   dependency. It does not use ACME now, because ACME adds
   `golang.org/x/crypto`. A later RFC can cover it.
2. **Reverse proxy.** A TLS-terminating reverse proxy is a supported setup,
   and the install documents describe it. The server never trusts
   `X-Forwarded-*` headers. The optional flag `--public-url` sets the external
   base URL. The server uses it for absolute URLs, such as the discovery
   document and the upload URLs. Without the flag, the server uses the scheme
   and host of the request.
3. **Warnings.** The server warns at start when it serves plain HTTP on an
   address that is not loopback. It does not refuse to start, because the
   Pulumi CLI works over plain HTTP.
4. **Version.** The flag `--version` prints the version. The build stamps the
   value with `-ldflags`.
5. **Container image.** A multi-stage Dockerfile builds the image.
   - The Go stage uses `CGO_ENABLED=0`.
   - The base image is distroless static `nonroot`, pinned by digest.
   - The image supports linux/amd64 and linux/arm64.
   - The image has a `/data` volume.
   - The console static files are in `/console`. They come from a console
     release tarball. The build pins the tarball by tag and SHA-256. The
     image build uses no Node.
   - The console repository needs a release workflow first (ADR 8, ADR 21).
6. **Binaries.** A workflow that a tag starts builds the binaries with a
   `go build` matrix. It makes `tar.gz` and `zip` archives and a `SHA256SUMS`
   file. The project does not use GoReleaser.
7. **Gate for new tools.** The image release workflow needs the GitHub Actions
   `docker/setup-buildx-action` and `docker/setup-qemu-action`. CLAUDE.md asks
   for the owner's yes before the project adds them. The Dockerfile and a
   local image smoke test land in milestone 2. The publishing workflow waits
   for the owner's yes.
8. **Later, not decided.** A Helm chart (RFC 35), ACME, image signing, an SBOM,
   the choice of registry and OS packages.

## Consequences

- Open work for milestone 2:
  1. The TLS flags in `cmd/opendustpan` and the TLS option in `dustpan.go`.
  2. An e2e variant that runs the real binary with `--tls-cert` and
     `--tls-key`.
  3. The `--public-url` flag, and a proxy test that checks the absolute URLs.
  4. The start warning and the `--version` flag with the build stamp.
  5. The Dockerfile, and a smoke test: run the image with a generated
     certificate, request the API and the console page, and check that the
     user is not root.
  6. A console release workflow that publishes a tarball with a checksum.
  7. A tag workflow for binaries.
- The image does not ship until the owner says yes to the two Actions of
  decision 7. The first tagged release (ADR 21) can wait for that yes.
- The server reads the certificate once at start. A renewal needs a restart.
- The install documents must show a proxy setup and the `--public-url` flag.
