---
version: 2
---

Issue: #1639.

## Why

**Persona: developer.** A developer reads outputs of another unit through the server. When the server refuses the read the denial audit entry has no client address, user agent or request ID, and a flood of refused reads writes one entry for each request. This change records the request fields and limits denial writes so the audit log stays useful.

The user path this change must make work:

1. **API.** Developer requests unit outputs through the tool-neutral API or the tool CLI.
2. **API.** Server refuses the read without a sharing rule or for client-encrypted state.
3. **Console.** Namespace administrator reads the audit log and sees the client address, user agent and request ID of each denial.
4. **Docs page.** Operator reads the output-sharing docs page for the denial fields and the limit.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| read without sharing rule gives 403 | 403 output read refused for no sharing rule. Run `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` again after the rule exists. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` |
| read of client-encrypted state gives 409 | 409 outputs unavailable for client-encrypted state. Run `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` again after the state is readable. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit}/outputs` |

Evidence for the claims above:

- Denied output reads write an audit entry with subject, token ID, object, check name and reason and without SourceIP, UserAgent or RequestID. Proof: #1639: The audit entry that the output sharing checker writes for a denied read has the subject, the token ID, the object, the check name and the reason. It has no SourceIP, UserAgent or RequestID, and it has no rate limit.
- A holder of unit:read-outputs can write one denial entry for each refused request, for example the 409 of a client-encrypted state. Proof: #1639: A caller that holds unit:read-outputs can send requests that the checker refuses (for example the 409 of a client-encrypted state) and write one entry for each request.
- refusal.Recorder is in use by existing HTTP adapters. Proof: rg -n -F --glob '!*_test.go' 'refusal.Recorder' pkg internal cmd matches internal/api/runnerapi/runnerapi.go:132, internal/api/triggers/triggers.go:92, internal/api/objectcontext/objectcontext.go:84, internal/api/tofuhttp/tofuhttp.go:129

## What Changes

- Denied output-read audit entries carry SourceIP, UserAgent and RequestID.
- Denial audit writes for output reads go through refusal.Recorder with its limiter.

Out of scope:

- New audit kinds outside denied output reads
- Changes to sharing-rule evaluation or output values
- A new limiter implementation outside refusal.Recorder
- Authorization refusals for a missing unit:read-outputs grant before the sharing check
- Summary or count entries for dropped denials

Depends on:

- `output-sharing` (order of the milestone review)

## Usability

Where the path still costs the person time or surprises them:

- A refused output read still reports only the rule or state cause; the new fields stay in the audit entry and never leak into the read answer.
- A flood of refused reads keeps failing reads while audit writes stop, so the caller sees stable errors.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- refusal.Recorder type exists (`internal/api/refusal/refusal.go:84`)
- Existing runner adapter wires refusal.Recorder (`internal/api/runnerapi/runnerapi.go:132`)
- Existing triggers adapter wires refusal.Recorder (`internal/api/triggers/triggers.go:92`)
- Existing object-context adapter wires refusal.Recorder (`internal/api/objectcontext/objectcontext.go:84`)
- Existing tofu HTTP adapter wires refusal.Recorder (`internal/api/tofuhttp/tofuhttp.go:129`)
- Audit kinds include read-access and audit-read (`pkg/audit/audit.go:58`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Does any ADR decide the audit fields or the limiter for denied output reads? No ADR in the pack decides audit fields or the limiter for denied output reads. The pack ADRs cover sessions, local users and review requests only. The issue text decides the fix: pass the request fields to the checker or use the recorder for the append, reuse refusal.Recorder with its existing budget, keep the 403 or 409 answer on a drop or append failure, and add no new proxy-header trust and no new limiter.
- Should the fix add a new limiter or reuse refusal.Recorder? The issue directs reuse of the recorder for the append. The pack shows refusal.Recorder in use by existing adapters. The change reuses it with its existing budget, so no new limiter and no new tool are added.
- Are authorization refusals before the sharing check in scope? Authorization refusals for a missing unit:read-outputs grant happen before the sharing check and keep their existing audit path. This change covers only refusals inside the output-sharing check: no rule and client-encrypted state.
- What does the audit show when the limiter drops entries? Dropped denials write no extra entry. The limiter stops the write and the read stays refused with 403 or 409. A summary count would need a new write path and is out of scope.
- What address source and bounds apply to the recorded request fields? The handler passes only the observed connection values with no new proxy-header trust. Empty values stay empty. Long values are stored within the existing audit column bounds and never change the read answer.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
