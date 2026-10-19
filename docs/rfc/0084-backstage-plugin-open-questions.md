---
rfc: 84
title: Open questions of the Backstage plugin
status: discussing
created: 2026-10-19
---

# RFC 84. Open questions of the Backstage plugin

Status: discussing. ADR 247 gives the design. These questions need the
project owner.

## Q1. Where does the plugin code live? (blocks the plugin work)

The plugin is TypeScript and npm. `opendustpan/opendustpan` runs a Go and
docs gate, so the plugin does not fit there.

| Option | For | Against |
| --- | --- | --- |
| A. A new repository `OpenDustpan/backstage-plugins` (proposed) | Own CI, own release, own Backstage version bumps. The same shape as the community plugins repository. | Only the owner can make it. One more repository to keep. |
| B. A directory in `OpenDustpan/console` | The repository exists, and it already runs npm. | The console and the plugin release on two schedules. The console gate then also gates Backstage bumps. |
| C. The Backstage community plugins repository from the start | Backstage maintainers bump the Backstage version each month. | Its maintainers then also review our changes. Our gates (strict linting, nilaway, semgrep, trivy and the license check) do not run there. |

The product manager proposes A now, and C as a later choice when the
plugin is stable. The answer also says who owns the publish right of the
npm scope `@opendustpan` for the new packages.

## Q2. Does a later slice read as the Backstage user? (does not block)

ADR 247 decision 5 reads as one read-only service principal. So each
Backstage user who can see the tab sees what that principal can read.

A later slice can exchange a token of the Backstage user for an
OpenDustpan token, so that each user sees only the units that the user
can read. This needs:

- one identity provider for Backstage and OpenDustpan, or a mapping of
  the subjects between them;
- a token exchange for a person. ADR 54 decision 6 says that a trust
  rule gives a principal that is not a person, so this is a new decision;
- an act-as edge or a session for the person on each request of the
  backend plugin.

Is per-user access a goal for a later release, or is one principal per
Backstage installation enough?

## Outcome

None yet.
