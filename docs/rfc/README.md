# RFCs

An RFC holds a question that is not decided yet: the problem, the options,
the arguments and the open questions. It can be rough, incomplete or wrong.
An ADR in `docs/adr/` holds only a decision (ADR 15).

## Life of an RFC

1. Copy `0000-template.md` to the next free number, with a short slug:
   `0005-short-slug.md`. Set `status: draft`.
2. When it is ready for others to read, set `status: discussing`. Edit it as
   often as you like. The pull request and the file's history hold the
   discussion.
3. When the project owner accepts it, write a short ADR with the next ADR
   number. The ADR holds the decision and links the RFC. Then set
   `status: accepted` and `adr: <number>` in the RFC.
4. When an RFC is not taken, set `status: dropped` and say why in its last
   section. Keep the file, so that the same idea does not come back without
   its history.

An accepted or dropped RFC does not change after that. A new idea gets a new
RFC.

## Front matter

```yaml
rfc: 4                 # the number in the file name
title: What sits under a namespace
status: draft          # draft | discussing | accepted | dropped
created: 2026-10-10
adr: 16                # only when accepted
was: proposed ADR 9    # only for RFCs 1 to 3, which were proposed ADRs
```

The front matter and the first lines of prose give the same state. Change
both together.

## Rules

- An RFC never says what the code does today as if it were decided. Code that
  an RFC describes can exist, but it is not a decision until an ADR says so.
- `make docs-lint` does not check RFCs, so that writing one costs little. The
  ADR that comes from an RFC follows ADR 14.
- RFCs 1 to 3 were proposed ADRs 9 to 11. ADRs 12 and 13 cite those numbers.
  ADR 1 forbids edits to accepted ADRs, so read "ADR 9" there as RFC 1,
  "ADR 10" as RFC 2 and "ADR 11" as RFC 3.

## Index

| RFC | Title | Status |
| --- | --- | --- |
| [1](0001-opentofu-via-the-tfe-compatible-api.md) | OpenTofu support through the TFE-compatible remote API | discussing |
| [2](0002-organizations-are-namespaces.md) | Organizations are namespaces, in every client | discussing |
| [3](0003-domain-modules-for-updates-secrets-and-state.md) | Domain modules for updates, secrets and OpenTofu state | discussing |
| [4](0004-what-sits-under-a-namespace.md) | What sits under a namespace | draft |
| [5](0005-profiles.md) | Profiles, shared config and secrets for every tool | draft |
| [6](0006-runners-and-remote-runs.md) | Runners and remote runs | draft |
| [7](0007-one-binary-many-roles.md) | One binary, many roles, removable features | draft |
| [8](0008-backups-and-restore.md) | Backups and restore, built in | draft |
| [9](0009-themes-and-white-labelling.md) | Themes and white-labelling | draft |
| [10](0010-artifact-registry-and-curated-upstreams.md) | Artifact registry and curated upstreams | draft |
