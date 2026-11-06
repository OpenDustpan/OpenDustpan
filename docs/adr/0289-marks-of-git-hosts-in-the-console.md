# 289. Marks of Git hosts in the console

Status: accepted · 2026-10-31. Decided by the project owner on 31 October 2026

## Context

The console names the Git hosts of an installation. A logo is a trademark of its owner. The guidelines of GitLab refuse its logo in the interface of a product. They also refuse it in the interface of an integration.

OpenDustpan is not affiliated with any vendor (see `CLAUDE.md`).

## Decision

1. **GitLab and Gitea (owner).** The console writes the name as text. It shows a generic icon that means "opens another site". It shows no logo of either host. (owner)
2. **GitHub (owner).** The console shows the GitHub mark without change, in black or white. It is smaller than the OpenDustpan mark. (owner)
3. **Forgejo (owner).** The console shows the Forgejo logo. The license of that logo is CC BY-SA 4.0. The console shows the attribution that the license needs, and a link to the license. (owner)
4. **Only where we name the integration (owner).** A mark or an icon appears only where the console names the integration. One example is the page of a Git host connection. A mark is never decoration. (owner)
5. **No other change.** The console never changes a mark: no new color, no crop, no effect.

## Consequences

- `OpenDustpan/console` implements this ADR. This repository does not change.
- `GLOSSARY.md` and the user documentation say "works with" for a Git host, and never imply an affiliation.
- A new Git host needs a check of its mark guidelines before the console shows its mark.
- This ADR is not legal advice. It records a project decision.
