## ADDED Requirements

### Requirement: proxy-header-fuzz

The server SHALL include a Go fuzz target FuzzClientIP for the trusted-proxy header parser.

#### Scenario: FuzzClientIP rejects hostile headers without panic
- **WHEN** Arbitrary bytes up to 4096 bytes reach the trusted-proxy header parser
- **THEN** The target records no panic and the author commits any crashing input as a seed in the corpus directory of the package.

#### Scenario: Spoofed header never changes the client IP
- **WHEN** A spoofed Forwarded value arrives from an untrusted peer
- **THEN** The parser returns the trusted address and ignores the spoofed header value.

### Requirement: sigv4-fuzz

The server SHALL include a Go fuzz target FuzzSigV4Canonical for the S3 SigV4 canonicalization.

#### Scenario: FuzzSigV4Canonical rejects hostile canonical requests without panic
- **WHEN** Arbitrary bytes up to 4096 bytes reach the S3 SigV4 canonicalization
- **THEN** The target records no panic and the author commits any crashing input as a seed in the corpus directory of the package.

#### Scenario: Canonicalization stays deterministic under CRLF
- **WHEN** A canonical request carries CRLF injection bytes
- **THEN** Equal inputs give equal canonical output and control bytes cause rejection with no panic.

### Requirement: source-pack-fuzz

The server SHALL include a Go fuzz target FuzzSourcePack for the source pack and checkout path.

#### Scenario: FuzzSourcePack rejects hostile pack bytes without panic
- **WHEN** Arbitrary bytes up to 4096 bytes reach the source pack and checkout path
- **THEN** The target records no panic and the author commits any crashing input as a seed in the corpus directory of the package.

#### Scenario: CI short fuzz run fails the build on a crash
- **WHEN** A bounded CI run of FuzzSourcePack finds a crashing input
- **THEN** The CI job fails and the author reproduces with the same fuzz command and commits the crashing input as a seed.
