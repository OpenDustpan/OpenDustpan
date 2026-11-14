## Context

A time-boxed spike on a branch that is not for merge. It runs one open, read, note and close cycle on each named system and records six answers in a NEW spike doc. The live run is BLOCKED until the owner names the sites and credentials.

## Decisions

1. **Spike branch only.** The branch writes only the NEW path `spike/changeticket-spike.md`, the NEW path `spike/changeticket-cleanup.md` and NEW scratch code under the NEW path `spike/scratch/`. Nothing lands on main.
2. **Stop rule.** If no site or credential is named, the spike writes the blocker in the NEW path `spike/changeticket-spike.md` and runs no live call.
3. **Secret hygiene.** Credentials come only from a stored secret of the CI system of the spike, never from a file that a person names. The doc redacts ticket data. Each credential is rotated after the run. The branch is scanned for secrets before the doc is shared.
4. **Cleanup.** Each test ticket is labeled as spike data and closed or canceled. Each webhook is deregistered. A refused close is a finding in the NEW path `spike/changeticket-cleanup.md`.
5. **Poll against callback.** The doc records signed callback support, the 30-day Jira webhook expiry and what a server poll returns.

## Structure

No new structure on main.

## Risks

- Test credentials leak into the branch history
- Site-specific field IDs read as portable behavior

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| none affected | The spike merges nothing and sets no latency, storage or rate target |  |  |
