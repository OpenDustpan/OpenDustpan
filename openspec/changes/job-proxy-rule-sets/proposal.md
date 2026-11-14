---
version: 2
---

Roadmap: `docs/roadmap.md:393` (Manage namespace and unit proxy rule sets.).

## Why

**Persona: namespace administrator.** A namespace administrator manages proxy rule sets for a namespace and for each unit. Today there is no route to save, read, or debug them, so a team cannot limit job network access. This change adds the save, read, and rendered view without the proxy runtime.

The user path this change must make work:

1. **API.** Save the namespace proxy rule set with PUT, then read it with GET
2. **API.** Save the unit proxy rule set with PUT, then read the joined set and the rendered view for operation plan
3. **Docs page.** Follow the docs page to write a first proxy rule set with one host, one method, and one canonical path

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save fails when a proxy rule has a bad host, port, method, path, or operation. | The server says the proxy rule set is not valid and names the proxy rule id and the field. Fix the field and send the request again. | `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` |
| Save fails without `namespace:manage-proxy-rules` or `unit:manage-proxy-rules`. | The server says access is denied for the proxy rule action. Ask a namespace administrator for the grant and check reach. | `GET /api/dustpan/v1/namespaces/{}/access-limits` |
| Save fails without fresh authentication. | The server says fresh authentication is needed. Sign in again and send the request again. | `GET /api/dustpan/v1/namespaces/{}/settings` |
| Save fails when If-Match is not the stored digest. | The server answers 412 and says the digest is stale. Read the set again and send the save again with the new digest. | `GET /api/dustpan/v1/units/{unit_id}/proxy-rules` |

Evidence for the claims above:

- This change is part 1 of 3 of a split of job-proxy for rule routes, validation, save, and joined set. Proof: docs/roadmap.md:393 says its scope specifies namespace and unit proxy rule routes, validation, save, and joined set with operation, host, port, method and canonical path match.

## What Changes

- New route `GET /api/dustpan/v1/namespaces/{namespace}/proxy-rules` to read the namespace set
- New route `PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules` to save the namespace set with digest and audit
- New route `GET /api/dustpan/v1/units/{unit_id}/proxy-rules` to read the unit set
- New route `PUT /api/dustpan/v1/units/{unit_id}/proxy-rules` to save the unit set with digest and audit
- New route `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` for debug only with unit read access
- Optional If-Match digest check on each save
- Strict validation of operation, host, port, method, and canonical path match
- Joined set of namespace and unit scopes in fixed order
- Rendered plan view of the joined set for debug only

Out of scope:

- Proxy runtime next to the job, one exit, and network wiring
- Credential hold in the job proxy and response scan
- Blocking log line with host and path
- Proxy records, record retention, and audit entry of a phase
- Upstream proxy of a runner pool and allowed private ranges
- Gate fact group for proxy
- Blueprint version proxy field

## Usability

Where the path still costs the person time or surprises them:

- The rendered view shows stored patterns for debug and may surprise a reader who expects rendered templates; the page states debug only.
- Join order is fixed and the API shows the effective joined set, so the writer sees the result before a job runs.
- The optional If-Match digest lets a script skip the check, so the page shows how to send it.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- This change is part 1 of 3 with scope for rule routes, validation, save, and joined set. (`docs/roadmap.md:393`)
- Proxy rule sets are plain data joined across scopes with namespace and unit save actions and routes. (`docs/adr/0330-the-job-proxy-core-decisions.md:1`)
- Proxy rules match host, method, and path and the words proxy rule and proxy rule set apply. (`docs/adr/0327-the-job-proxy.md:1`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 327: The job proxy** (owner-tagged (all decisions)).

- Decisions that apply: 4 (current), 18 (current).
- Changed by: ADR 330 extends. Chain to the end: ADR 330, ADR 353.
- Current rule: this ADR, with ADR 330.
- Quote: "One allow line of the job proxy is a **proxy rule**."
- Quote: "The set of proxy rules of a unit is a **proxy rule set**."
- Quote: "A proxy rule matches the host, the method and the path of a request, with `*` and `**` in a path pattern, and with templates that render when the job starts."
- Effect: This change uses the words proxy rule and proxy rule set, and matches host, method, and path. It does not change runtime or words.
- Conflict: none.

**ADR 330: The job proxy: core decisions P1 to P12** (owner-tagged (all decisions)).

- Decisions that apply: 4 (extended), 11 (current), 12 (current).
- Changed by: ADR 353 extends decisions 2, 4 and 7. Chain to the end: ADR 353.
- Current rule: this ADR, with ADR 353.
- Quote: "Proxy rules are plain data."
- Quote: "A proxy rule only allows."
- Quote: "`namespace:manage-proxy-rules` on the namespace and `unit:manage-proxy-rules` on the unit save a proxy rule set."
- Quote: "Routes. `GET` and `PUT .../namespaces/{namespace}/proxy-rules`, `GET` and `PUT .../units/{unit_id}/proxy-rules`"
- Effect: This change saves proxy rule sets at namespace and unit scopes, joins them, and serves the routes. It follows the plain data rule and the route and action rules.
- Conflict: none.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every control is a setting that a team turns on (owner, 2026-11-07)."
- Quote: "Until a team turns a setting on, a unit behaves as it does on `main`."
- Effect: This change adds proxy rule sets as opt-in settings. A unit with no proxy rule set behaves as before.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Does a save of a proxy rule set need a required If-Match digest, and not an optional one?
   - Options: Keep the If-Match digest optional; Require If-Match digest on each save and refuse a request with no digest.
   - Provisional decision (owner review 14 Nov): Keep the If-Match digest optional
   - Reason: A proxy rule set widens job network access, but a required header breaks simple scripts. Optional keeps both safe and simple until the owner decides. Benchmark: AWS requires an explicit version check for a policy write that widens network access.
   - Persona impact: Namespace administrator gets 412 on a stale digest and reads again. Scripts keep working without the header. Escape hatch: This change ships an optional If-Match digest. A script that sends none keeps last write wins, and each save keeps an audit entry with old and new digest.
   - Owner-tagged ADRs involved: ADR 330 (their words do not change).

Questions considered and answered:

- Does a proxy rule deny a request or only allow it? Only allow. ADR 330 decision 4 says a proxy rule only allows, and ADR 327 decision 4 says a proxy rule matches host, method, and path. Deny stays out of scope.
- Does this change start the job proxy or write proxy records? No runtime in this change. ADR 330 decision 12 lists the namespace and unit proxy rule routes and the rendered route. Proxy runtime, credential hold, blocking log line, records, and audit of a phase stay in later parts.
- Does this change join blueprint proxy rules too? Joined set only. ADR 330 decision 4 says the proxy rule sets of the scopes of a unit are joined. Blueprint proxy field handling ships with blueprints, not here.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
