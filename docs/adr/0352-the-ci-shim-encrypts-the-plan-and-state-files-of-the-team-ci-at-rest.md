# 352. The CI shim encrypts the plan and state files of the team CI at rest

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026

Changes:

- Extends ADR 41 decision 10: the server seals state at rest. The CI shim covers the files that the tool writes in the team's CI.

## Context

[RFC 94](../rfc/0094-ci-shim-secrets-at-run-time-out-of-state.md) describes the CI shim, an optional program that a team runs in its own CI. Its first job is to give secrets to the tool at run time and keep them out of state. RFC 94 did not cover the plan files and the state files that the tool writes in the CI job. OpenTofu saves a plan with `-out`, and the CI often keeps it as an artifact or a cache. These files can hold secret values in plain text. ADR 41 decision 10 seals state on the server, but it cannot seal a file that stays in the team's CI.

On 7 November 2026 the project owner decided the scope. The project owner said: "Our CI shim can take care of encrypting the plan and state files at rest. If users don't use them, that is not really our problem now is it."

## Decision

Decided by the project owner on 7 November 2026.

1. **The server seals state, and reads mask secrets (owner, 2026-11-07).** The server seals the state that it receives. A state read masks secret values. ADR 41 decided this already (decisions 2 and 10). This ADR does not change it. The server seals OpenTofu state blobs with the data key of the workspace scope. The move to an item key for each state version (ADR 151 decision 7) is open work.
2. **The shim encrypts local files at rest (owner, 2026-11-07).** The shim encrypts the plan files and state files that the tool writes in the CI. This is part of the job of the shim, next to the delivery of secrets at run time. The key comes from a profile of the unit.
3. **A team without the shim is out of scope (owner, 2026-11-07).** A team can run the tool without the shim. OpenDustpan does not encrypt the plan files and state files of that team. The user documentation says this in plain words. It gives the one command that adopts the shim.
4. **The shim stays a convenience layer (owner, 2026-11-07).** The shim runs in the same CI job as the code that it starts. Encryption at rest protects a file that leaves the job. Examples are an artifact, a cache or a copy of a disk. It does not protect the memory of the tool, the log of the tool, or the person who runs the tool. The documents list these as residual risk.
5. **The shim redacts and stores the log (owner, 2026-11-07).** The shim runs the tool as a child process. It captures the standard output and the standard error. It redacts each secret value that it knows, in the raw, base64 and URL-encoded forms. It sends the full redacted log to the backend. A person reads it with a read grant. A shim setting decides if the CI log shows the redacted stream or a summary with a link. A secret that the tool changes into a form that the shim does not know is not caught.
6. **Process memory is a residual risk (owner, 2026-11-07).** The shim cannot encrypt process memory. It applies the mitigations that the platform allows. These are no core dumps (`RLIMIT_CORE=0`) and no swap where possible. Confidential computing (SEV-SNP, TDX) belongs to the runner infrastructure. It is out of scope for the shim.

## Consequences

- RFC 94 stays a draft. It now covers both jobs of the shim. This ADR does not accept the design of RFC 94. The place of the code, the key source, the file format for Pulumi and the slices stay open questions there.
- The documents for the shim state the residual risks of decision 4. They state the scope of decision 3.
- This ADR adds no code. The OpenTofu `encryption` block and a file format for Pulumi plan files are for RFC 94 to design.
- The shim reads the key from a profile, so the profile of the unit holds one more secret. The key is subject to the rules of ADR 203 for an open of a profile.
