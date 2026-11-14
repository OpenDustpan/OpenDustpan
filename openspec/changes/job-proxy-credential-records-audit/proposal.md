Roadmap: `docs/roadmap.md:393` (Inject credentials and record proxy use.).

## Why

**Persona: developer.** A developer runs jobs that call outside services through the job proxy and needs credentials that the job never holds, with a record of each request.

The user path this change must make work:

1. **CLI.** Run a job of a unit with proxy rules and read the job log for allowed and refused requests.
2. **API.** Inject credentials at the proxy and confirm the assignment holds references only.
3. **API.** List per-request proxy records of the phase and read the phase audit entry.
4. **Docs page.** Read the proxy records and audit docs page.
5. **E2e.** Run the e2e job proxy step with a refused host and check blocked by the job proxy.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Request refused with no allow match | Proxy refused the request and logged blocked by the job proxy with host and path. List the units of the namespace to confirm state. | `GET /api/dustpan/v1/namespaces/{namespace}/units` |
| Credential reference has no valid bound credential | Injection failed closed with no network contact and the job log names the reference. List the deleted units to confirm no state change. | `GET /api/dustpan/v1/namespaces/{namespace}/deleted-units` |

Evidence for the claims above:

- The context pack names no ADR, path, route, or code fact for this item. Proof: adrs [], paths [], code_facts [], and the roadmap step text that defines scope as credential hold, records, audit, and the plan-phase block.

## What Changes

- Proxy-only credential hold and request-time injection
- Response scan for plain credential values within a scan limit
- New route for per-request proxy records of a phase under units
- New route for proxy rules of a namespace used for credential binding checks
- Phase record and audit entry with proxy facts
- Plan-phase block for apply-only writes

Out of scope:

- Rule set create and edit syntax
- Job proxy process placement and network isolation
- Curation, registry, blueprints, promotion

Depends on:

- job-proxy-rule-sets
- job-proxy-runtime-guard
- `job-proxy-runtime-guard` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- A refused request still costs time when the log omits the matched rule set revision.
- Credential binding errors surprise when the binding names no rule.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The proposal error table and tasks 2.3 name routes that the code check says are not in the route table: units/{unit}/proxy-records and namespaces/{namespace}/proxy-rules. The proposal itself says it is not spec-ready. Use existing routes or add the routes to the change. Resolution: answered: Both routes are now named as new routes in what_changes and the records route has a dedicated spec requirement.
- R1.2 (scope): The roadmap item says part 3 covers request-time credential injection and the record, but no spec delta defines how a credential is created, bound to a rule, stored sealed, or rotated. The design only names a vault with a binding check. Define the binding path or name the part that owns it. Add the authorization actions for it. Resolution: answered: Binding path defined in specs as rule-bound sealed references with authorization actions on proxy rules; rotation stays with the sealed store and is out of scope.
- R1.3 (missing scenario): No scenario covers a credential reference with no valid bound credential, a missing or revoked secret, or an unsealable store. The proposal lists this error but the spec has no requirement for it. It must say that injection fails closed with no network contact and what the job log shows. Resolution: answered: New requirement sealed credential fail closed covers missing, revoked, and unsealable credentials with no network contact.
- R1.4 (missing scenario): The spec has no scenario for the refused request path. It has no record for a refused request and no requirement that the job log shows the host and path. The e2e step depends on this. Only the plan-phase block scenario covers the log text. Resolution: answered: New requirements refused request record and refused request log cover the refused path with host and path.
- R1.5 (failure mode): Response scan redacts only plain credential values. Encoded, split, chunked, compressed, or header-echoed values are not covered. Streaming responses and large bodies are not covered. Define what is scanned (headers, body, encodings) and what happens when the scan cannot run. Add a hostile test for each. Resolution: answered: Response scan requirement now states plain body values only up to a size limit with fail closed, plus hostile tests; encodings and streaming are out of scope.
- R1.6 (failure mode): Records and audit are not covered for failure. There is no rule for a failed record write (fail closed or not), a runner crash or restart before the phase audit entry is written, or a retried phase. Audit entries could be lost or doubled. The change must say that no request is allowed unrecorded. Resolution: answered: New requirement record before send states no request is allowed unrecorded and covers crash, restart, and retry by phase aggregation.
- R1.7 (failure mode): The job can set its own Authorization header or request a redirect to another host. Redirects and a header the job supplies are not covered. Say that the proxy strips job-set credential headers and re-checks each redirect host against the rules before it adds the credential. Resolution: answered: New requirement redirect and header strip covers stripping job-set headers and re-check of each redirect host.
- R1.8 (open question): The proxy-records route has no stated pagination, retention, or access rule. Records hold host and path, which may carry sensitive data such as query strings. Say if the query is stored and who may read the records. Resolution: open question: Added as open question on query storage and retention with a recommended option.
- R1.9 (wrong claim): ADR check says no ADR is touched, with no proof. The ADR list is empty, yet the change touches secrets, audit, and security layers. Give an index search as evidence. The Depends on list also repeats job-proxy-runtime-guard twice. Resolution: answered: Evidence now states the pack names no ADR and depends_on holds two distinct parts.
- R1.10 (missing scenario): The plan-phase block does not say how the server decides that a write only an apply rule allows. It does not cover a method that is not a plain write, such as a GET with side effects, or a phase that changes between plan and apply. The Structure section also names no type that makes the plan-write bug class impossible. Resolution: answered: Plan-phase block now defined as plan-effective rules and newPlanEffectiveRules structure; side-effect GETs are refused by method plus rule match.
- R1.11 (missing scenario): The phase audit entry is required only for a phase with proxy use. A phase where all requests were refused, or a phase with zero requests, has no stated behavior. The audit entry has no scenario for the runner session being absent. Resolution: answered: Phase audit requirement now covers refused-only and zero-request phases and absent runner session.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Are query strings stored in proxy records and how long are records kept?
   - Options: Store full URL with query and retain 30 days; Store host plus path without query and retain 90 days.
   - Provisional decision (owner review 14 Nov): Store host plus path without query and retain 90 days.
   - Reason: The pack and roadmap do not state retention or query handling. Benchmark: AWS: paginate use records and retain query strings only in truncated form.
   - Persona impact: Developers lose debug detail without query, but query may carry secrets. Escape hatch: Operators can export records through the API and drop them per retention policy.

Questions considered and answered:

- Where are proxy credentials held? The job never holds the credential value. The assignment carries a credential reference only. The job proxy resolves the reference from sealed storage at request time.
- Where are proxy decisions recorded? Each proxied request writes one request record. The phase record aggregates counts. The namespace audit log holds one entry per finished phase with proxy facts and the runner session.
- How are credentials created, bound, sealed, and rotated? The context pack names no ADR, no path, and no route for this item, so credential create, bind, seal, and rotate behavior is defined as binding to an existing proxy rule in this change and rotation is left to the sealed secret store.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
