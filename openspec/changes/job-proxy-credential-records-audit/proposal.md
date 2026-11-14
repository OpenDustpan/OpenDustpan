---
version: 2
---

Roadmap: `docs/roadmap.md:393` (Inject credentials and record proxy use.).

## Why

**Persona: developer.** A developer runs jobs that call outside services through the job proxy and needs credentials that the job never holds, with a record of each request.

The user path this change must make work:

1. **CLI.** Run a job of a unit with proxy rules and read the job log for allowed and refused requests.
2. **API.** Read the assignment of a phase and confirm it holds credential references only.
3. **API.** List the per-request proxy records of the phase and read the phase audit entry.
4. **Docs page.** Read the proxy records and audit docs page.
5. **E2e.** Run the e2e job proxy step with a refused host and check blocked by the job proxy.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Request refused with no allow match | The job proxy refused the request and the job log shows blocked by the job proxy with host and path. Read the audit entries to see the phase facts. | `GET /api/audit/entries` |
| Credential reference has no valid bound credential | Injection failed closed with no network contact and the job log names the reference. Read the audit entries to see the refused resolve. | `GET /api/audit/entries` |

Evidence for the claims above:

- The pack names no ADR, path, route or code fact for this item that proves a negative claim. Proof: adrs [], paths [], code_facts [], evidence {} in the context pack.
- Main already has a proxy package with injection and guard tests. Proof: The pack lists TestInjectionHeader (pkg/launcher/proxy/inject_test.go:48) and TestOutboundGuardTunnel (pkg/launcher/proxy/guard_test.go:31).

## What Changes

- Proxy-only credential hold and request-time injection
- Response scan for plain credential values within a scan limit
- New route for per-request proxy records of a phase
- Phase record and audit entry with proxy facts and the runner session
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

- A refused request costs time when the log omits the matched rule set revision.
- A credential binding error surprises when it names no rule.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The proxy package has a test of the injection header. (`pkg/launcher/proxy/inject_test.go:48`)
- The proxy package has a test of the outbound guard tunnel. (`pkg/launcher/proxy/guard_test.go:31`)
- The audit kind audit-read exists. (`pkg/audit/audit.go:69`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

No ADR is touched.

## Open questions

none

Questions considered and answered:

- Where are proxy credentials held? The assignment carries a credential reference only. The job proxy resolves it from sealed storage at request time, by the stable ID of the credential.
- Where are proxy decisions recorded? Each proxied request writes one record before send and updates it after response. The audit log holds one idempotent entry per finished phase.
- How are credentials rotated? Rotation stays with the sealed secret store and is out of scope. A revoked credential fails closed at resolve time.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
