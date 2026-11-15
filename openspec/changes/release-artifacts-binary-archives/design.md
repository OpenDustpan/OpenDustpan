## Context

Operator needs reproducible install archives before the first tag. Build runs from a clean tree for one given version and writes four archives plus SHA256SUMS.

## Decisions

1. **Reproducible build script.** One NEW shell build script owns archive layout and byte determinism: sorted file order, uid 0, gid 0, mode 0755 for the binary and 0644 for license files, mtime 1970-01-01T00:00:00Z, gzip -n with level 9, go build with CGO_ENABLED=0 GOOS and GOARCH per target plus -trimpath and -buildvcs=false, ldflags stamp of version. No new tool, no GoReleaser, no custom analyzer. No Go type applies because no Go code changes; determinism comes from fixed script inputs.
2. **Fail closed verify script.** The NEW verify script is read only and fails closed: it cleans no input, it refuses path traversal entries, absolute paths, unexpected files, missing or extra SHA256SUMS entries, missing license files, checksum mismatch, and version stamp mismatch. Foreign arch stamp check reads the embedded version string from the binary bytes and never executes the binary.

## Structure

The NEW build script is the only writer of archive layout; the NEW verify script is read only and fails closed on any mismatch.

## Risks

- Nondeterministic tar or gzip metadata breaks byte equality across hosts.
- Missing license file ships an archive without license text.
- Unstamped binary reports a wrong version.

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| reproducibility | 100 | percent byte equal archives | two builds of one version on one platform |
| build time | 600 | s | release-build for four targets on CI runner |
