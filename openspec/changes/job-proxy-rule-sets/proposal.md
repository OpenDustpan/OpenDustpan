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
| Save fails when a proxy rule has a bad host, port, method, path, or operation. | The server says the proxy rule set is not valid and names the proxy rule id and the field. Fix the field and send the request again. | `GET /api/dustpan/v1/namespaces/{namespace}/proxy-rules` |
| Save fails without `namespace:manage-proxy-rules` or `unit:manage-proxy-rules`. | The server says access is denied for the proxy rule action. Ask a namespace administrator for the grant and check reach. | `GET /api/dustpan/v1/units/{unit_id}/proxy-rules` |
| Save fails without fresh authentication. | The server says fresh authentication is needed. Sign in again and send the request again. | `PUT /api/dustpan/v1/units/{unit_id}/proxy-rules` |

Evidence for the claims above:

- This change is part 1 of 3 of a split of job-proxy for rule routes, validation, save, and joined set. Proof: docs/roadmap.md:393 says its scope specifies namespace and unit proxy rule routes, validation, save, and joined set with operation, host, port, method and canonical path match.

## What Changes

- New route `GET /api/dustpan/v1/namespaces/{namespace}/proxy-rules` to read the namespace set
- New route `PUT /api/dustpan/v1/namespaces/{namespace}/proxy-rules` to save the namespace set with digest and audit
- New route `GET /api/dustpan/v1/units/{unit_id}/proxy-rules` to read the unit set
- New route `PUT /api/dustpan/v1/units/{unit_id}/proxy-rules` to save the unit set with digest and audit
- New route `GET /api/dustpan/v1/units/{unit_id}/proxy-rules/rendered` for debug only with unit read access
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

- The rendered view shows values for debug and may surprise a reader who expects masked values; the page states debug only.
- Join order is fixed and the API shows the effective joined set, so the writer sees the result before a job runs.

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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The user path and the error table name commands that the Open questions section says are not in the API description. GET and PUT `/api/dustpan/v1/.../proxy-rules` are new routes, so they cannot be the 'one command that helps' until built. The section also says the change is not spec-ready, so the gate is not met. Resolution: answered: New routes are now named in what_changes; errors and user path use those new routes and the docs page ships in the same pull request.
- R1.2 (open question): The Open questions section does not read 'none'. It holds a failed code check and no named decider, so the spec-ready gate refuses the change. Resolution: answered: No question remains that the ADRs and pack do not answer; open_questions reads none with considered questions listed.
- R1.3 (missing scenario): No requirement or scenario covers a save without the action grant or without fresh authentication, although the design and the error table name both refusals. The scenarios cover only the success path and the bad-field path. Resolution: answered: Added requirements refuse save without grant and refuse save without fresh authentication, each with a scenario.
- R1.4 (missing scenario): ADR 330 decision 4 defines more fields than the spec lists: id form, mode (tunnel/inspect), templates and `*`/`**` patterns. The spec and design name only operation, host, port, method and path. The design does not say how the id rules (1 to 64 bytes), id uniqueness within a set, or the port default are checked. The design is silent on `mode`, on `*`/`**`, and on the port default. Resolution: answered: Added id form, id uniqueness, port default, `*` and `**`, mode-free match of operation host port method canonical path, and validation scenarios.
- R1.5 (missing scenario): No scenario covers the audit entry with the old and new digest (ADR 330 decision 11). It appears only in tasks 2.1 and 2.2, with no scenario and no named test. The spec says a digest is returned but not that the audit entry is written. Resolution: answered: Added requirement write audit entry with old and new digest with a scenario and a named test.
- R1.6 (failure mode): Concurrent saves to the same scope are not covered. The design names no precondition (for example If-Match on the digest) to stop a lost update, and no scenario covers a stale digest. Resolution: answered: Design now states last write wins with no optimistic check in this part, with a scenario for sequential saves.
- R1.7 (failure mode): The rendered view shows rule values, but its read access is only unit read. The spec has no scenario for a reader without read access. The design says it 'leaks secret values' as a risk but gives no mitigation. ADR 330 decision 12 says the view is for a person who debugs, so the required grant is an open question. Resolution: answered: Added scenario that the rendered view refuses a reader without unit read access; rendered view holds no secret values in this part.
- R1.8 (missing scenario): Missing: an oversize document or rule count, a wrong content type, and a unit or namespace that does not exist. The design names a handler size limit but gives no value. No scenario covers GET of an unset set, or deletion and rename of the namespace or unit. No scenario covers the effective-value view when no set exists, which ADR 353 decision 2 requires. Resolution: answered: Added scenarios for oversize document, bad content type, unknown namespace or unit, GET of an unset set, and effective empty set.
- R1.9 (open question): The design does not decide the order of the unit set and namespace set for the same host, nor whether a duplicate rule id across scopes is allowed in the joined set. It also leaves open the behavior of the join when one scope is empty. The joined set needs an id rule for scoped duplicates. Resolution: answered: Design now fixes join order, allows duplicate ids across scopes with scope label, and defines empty scope behavior.
- R1.10 (scope): The roadmap scope says the joined set carries operation, host, port, method and canonical path. Task 1.1 names only a namespace validation test. No named test covers the unit save, the rendered view, or the hostile layer tests of the HTTP handler. The spec-to-test mapping is incomplete. Resolution: answered: Tasks now map each requirement to a named failing test for namespace save, unit save, join, rendered view, audit, and handler hostile cases.
- R1.11 (wrong claim): The design says 'Keep no normalized copy of the path' and that the rendered view shows values for debug. Rendering of templates that 'render when the job starts' (ADR 327 decision 4) is not addressed. The ADR check does not say what the rendered view does with templates. Resolution: answered: Design and rendered requirement now state the rendered view shows stored patterns without template render.

## Open questions

none

Questions considered and answered:

- Does a proxy rule deny a request or only allow it? Yes. ADR 330 decision 4 says a proxy rule only allows, and ADR 327 decision 4 says a proxy rule matches host, method, and path. Deny stays out of scope.
- Does this change start the job proxy or write proxy records? No runtime in this change. ADR 330 decision 12 lists the namespace and unit proxy rule routes and the rendered route. Proxy runtime, credential hold, blocking log line, records, and audit stay in later parts.
- Does this change join blueprint proxy rules too? Joined set only. ADR 330 decision 4 says the proxy rule sets of the scopes of a unit are joined. Blueprint proxy field handling ships with blueprints, not here.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
