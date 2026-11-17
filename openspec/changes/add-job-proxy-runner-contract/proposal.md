---
version: 1
---

Issue: #3369.

Roadmap: `docs/roadmap.md:393` (Manage namespace and unit proxy rule sets.).

## Why

**Persona: runner pool administrator.** A runner pool administrator wants the job proxy of a pool to open only the private networks that the team names, and to send its outbound requests through the proxy that the team runs. Today the pool has no such settings, and the runner protocol has no path for a job proxy. The job proxy runtime (`job-proxy-runtime-guard`) is blocked on both.

**Persona: job proxy (a process next to the launcher).** It needs to ask the server for the rules of its claim, the ranges of its pool and the credential of the upstream proxy, and nothing else. It must not be able to read a launcher secret, and a launcher must not be able to read the upstream credential.

The user path this change must make work:

1. **API.** Set the allowed private ranges and the upstream proxy of a pool with PUT, then read the effective values with GET
2. **Protocol.** The runner asks for a proxy handoff token for its claim. The job proxy exchanges it for a proxy session and reads its assignment: the joined rules, the pool ranges and the upstream URL
3. **Protocol.** The job proxy reads the upstream credential through its session, and only through it
4. **Docs page.** Follow the docs page to set the ranges and the upstream proxy of a pool and to see what a proxy session got

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Save of the pool proxy settings fails on a bad range (below the minimum prefix, or a never-opened address such as loopback or the metadata address). | The server says the range is not valid and names the range. Fix it and send the request again. | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings` |
| Save fails on a bad upstream URL (not http or https, has user information, or names a never-opened address). | The server says the upstream URL is not valid and names the rule. The credential goes in its own field. | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings` |
| Save fails without `namespace:manage-runners`. | The server says access is denied for the runner action. Ask a namespace administrator for the grant. | `GET /api/dustpan/v1/namespaces/{namespace}/access-limits` |
| Save fails when If-Match is not the stored version. | The server answers 412 and says the version is stale. Read the settings again and send the save again with the new version. | `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings` |
| The job proxy opens its session and the rendered rules need a value that exists only at job start. | The session open is refused with the fixed reason `rules-not-renderable`. Remove the `inputs.` or `profile.` template from the rule, or wait for the change that renders them at job start. | `GET /api/dustpan/v1/namespaces/{namespace}/units/{unit_id}/proxy-rules/rendered` |
| A launcher token is sent to a proxy path, or a proxy token to a launcher path. | The server answers 403 with the fixed reason `wrong-session-kind` and writes an audit entry. | `GET /api/dustpan/v1/audit/chain` |

Evidence for the claims above:

- Main has no proxy session path and no pool proxy setting. Proof: `rg -c -i proxy pkg/runner/protocol/protocol.go pkg/runner/store.go pkg/runner/runner.go internal/api/runnerapi/routes.go internal/api/runnerpools/routes.go` finds 0 matches in each file.
- Main has no stored allowed range or upstream URL for a pool. Proof: `rg -i 'allowed_ranges|upstream' pkg/store/sqlite/migrations/0168_baseline.sql pkg/store/postgres/migrations/0168_baseline.sql pkg/runner internal/api/runnerpools` finds no pool setting. `egress.ParseAllowedRanges` is called only for the git host, audit sink and LDAP flags (`cmd/opendustpan/main.go`, `cmd/opendustpan/ldap.go`).
- `job-proxy-runtime-guard` waits for this change. Proof: its `tasks.md` tasks 2.2 and 2.3 say BLOCKED by this change, and `job-proxy-rule-sets/proposal.md` lists the upstream proxy and the allowed private ranges under "Out of scope".
- `docs/rfc/0016-egress-proxy.md` item X5 lists "the proxy handoff token, the proxy session, the assignment, the CA and record uploads, the pool settings (allowed ranges, upstream proxy)". This change takes the proxy handoff token, the proxy session, the assignment and the pool settings. The CA and record uploads stay in `job-proxy-credential-records-audit`.

## What Changes

- New pool setting `proxy-settings`: allowed private ranges and upstream proxy (URL and sealed credential), with a version, who set it and when
- New route `GET /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings` (effective values and their place; the credential is never returned)
- New route `PUT /api/dustpan/v1/namespaces/{namespace}/runner-pools/{pool}/proxy-settings` (validates, seals the credential, writes the audit entry in the same transaction; optional If-Match version)
- New runner protocol path `POST /runner/v1/claims/{operation}/proxy-handoff` (runner key; one proxy handoff token for one live claim)
- New runner protocol path `POST /runner/v1/proxy/session` (proxy handoff token; opens a proxy session; the answer holds the proxy assignment)
- New runner protocol path `POST /runner/v1/proxy/session/upstream-credential` (proxy session key; the unsealed upstream credential, one audit entry for each call)
- A session and a handoff token get a kind (`launcher` or `proxy`). The kind comes from the route and is stored. A token of one kind opens nothing of the other kind
- New audit events for a change of the pool proxy settings, for the open of a proxy session and for the issue of the upstream credential
- No new identity action. Each route declares an existing action (table in design.md)
- Schema edited in place in both baselines: a `kind` column on `runner_handoff_tokens` and `runner_sessions`, and the new table `runner_pool_proxy_settings`
- The assignment version stays 1: this change adds no field to the launcher assignment (design.md, decision 7)
- New docs page `docs/guides/job-proxy-pool-settings.md` and the proxy session paths in `docs/reference/runner-api.md`
- E2e step with the real launcher binary

Out of scope:

- The proxy runtime, the guard dialer and the upstream CONNECT client (`job-proxy-runtime-guard`)
- The CA upload, the proxy records upload and the pod address of the proxy (`job-proxy-credential-records-audit` and the coordinator change)
- The render of `inputs.` and `profile.` templates at job start and the digest in the job spec
- Where the proxy runs for each isolation kind (the coordinator and the launcher changes)
- The proxy rule sets and their routes (`job-proxy-rule-sets`, merged)
- The routes of the job session paths for source, plan and credentials (`add-job-session-paths-runner-2`)
- The job token issue (ADR 386 moves it to the credentials route; `add-job-session-paths-runner-token-issuer` needs a rewrite)
- The gate fact group for proxy and the blueprint proxy field
- Fresh authentication on the pool proxy settings: no other pool route needs it

## Usability

Where the path still costs the person time or surprises them:

- The upstream credential is write-only. The page says that a person who forgot it sets a new one, and GET shows `credentialSet`.
- The job proxy still resolves and checks each rule host before it sends CONNECT to the upstream proxy. The page says that the upstream proxy must apply its own rules to the final address.
- A pool with no settings keeps today's behavior: no private range opens and no upstream proxy is used. GET shows the default with `source: "default"`.
- A rule with a template that needs a job-start value fails the proxy session open until the job-start change lands. The error names the fixed reason.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- The launcher session path to open a session exists. (`pkg/runner/protocol/protocol.go:57 (PathSession)`)
- The launcher handoff path of a claim exists. (`pkg/runner/protocol/protocol.go:39 (PathClaimHandoff)`)
- The route table joins the runner, launcher and plan routes. (`internal/api/runnerapi/routes.go:56 (runnerRoutes)`)
- The handoff handler issues the handoff token of a claim. (`internal/api/runnerapi/protocol.go:608 (handoff)`)
- The session store issues handoff tokens and opens sessions. (`pkg/runner/store.go:168 (IssueHandoffToken)`)
- The session store opens a session with no kind. (`pkg/runner/store.go:177 (OpenSession)`)
- A handoff token checks its use with no kind. (`pkg/runner/token.go:169 (CheckUse)`)
- A runner pool has no proxy column. (`pkg/store/sqlite/migrations/0168_baseline.sql:748 (runner_pools)`)
- Proxy rule sets join. (`pkg/jobproxy/join.go:16 (Join)`)
- Proxy rule sets render. (`pkg/jobproxy/render.go:188 (Render)`)
- The egress rules parse allowed ranges. (`pkg/egress/egress.go:124 (ParseAllowedRanges)`)
- The egress rules check one allowed range. (`pkg/egress/egress.go:141 (CheckAllowedRange)`)
- The guard dialer refuses the whole host when one answer fails the check. (`pkg/launcher/proxy/dial.go:104 (resolve)`)
- The secret service seals data under a scope. (`pkg/secret/secret.go:268 (Encrypt)`)
- The assignment version is 1. (`pkg/runner/version.go:28 (AssignmentVersion)`)

## Owner decisions that apply

No decision of the owner is new in this change. The decisions in the ADR check below bind it. Each one is quoted there.

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 327: The job proxy** (owner-tagged (all decisions)).

- Decisions that apply: 1, 2, 4, 7, 8, 10, 18 (current).
- Changed by: ADR 330 extends. Chain to the end: ADR 330, ADR 353.
- Current rule: this ADR, with ADR 330.
- Quote: "One allow line of the job proxy is a **proxy rule**."
- Quote: "The set of proxy rules of a unit is a **proxy rule set**."
- Effect: This change uses the words of the ADR for the rules that a proxy session carries. It adds no word.
- Conflict: none.

**ADR 330: The job proxy: core decisions P1 to P12** (owner-tagged (all decisions)).

- Decisions that apply: 2 (extended), 3 (current), 8 (current), 10 (current), 11 (current), 12 (current).
- Changed by: ADR 353 extends decisions 2, 4 and 7. Chain to the end: ADR 353.
- Current rule: this ADR, with ADR 353.
- Quote: "A pool can name an upstream proxy, with a URL and a credential. The credential comes from the pool, never from the profile of a unit. The pool seals it with the data key of the namespace, and only a proxy session gets it."
- Quote: "It opens a private address only in a range that the runner pool allows (`egress.ParseAllowedRanges`)."
- Quote: "An error text never holds the URL or the credential of the upstream proxy: the job proxy maps each error to a fixed reason."
- Quote: "`namespace:manage-runners` sets the allowed ranges and the upstream proxy of a pool."
- Quote: "The runner protocol gets the proxy session paths under `/runner/v1`. The proxy fields raise the assignment version."
- Effect: This change adds the pool settings, the sealed credential that only a proxy session gets, the action `namespace:manage-runners` for the settings, and the proxy session paths. The last quote says that the proxy fields raise the assignment version. This change adds no proxy field to the launcher assignment, so the version stays (design.md, decision 7). The first change that adds such a field raises it.
- Conflict: none.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1, 2, 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Until a team turns a setting on, a unit behaves as it does on `main`. The docs and the console make each setting visible. The API shows the effective value of each setting, with the place that set it (see \"No magic\" in `CLAUDE.md`)."
- Quote: "A setting can turn a control off only by an explicit stored value that has a record of who set it and when."
- Effect: A pool with no stored settings behaves as today. GET shows the effective value and `source`. Each stored setting has `updatedBy` and `updatedAt`.
- Conflict: none.

**ADR 133: Runner identities and tokens** (decisions 2, 6, 7, 9 and 11 are marked (owner)).

- Decisions that apply: 1 (current), 5 (current), 8, 9 (current), 11 (current), 12 and 13 (extended).
- Changed by: ADR 204 extends, ADR 350 extends decision 2, ADR 386 extends decisions 12 and 13. Chain to the end: ADR 204, ADR 248, ADR 286, ADR 350, ADR 386.
- Current rule: ADR 133, with ADR 204, ADR 350 and ADR 386.
- Quote: "It holds only `runner:claim` on its pool, and only through the protocol. It never calls the REST API."
- Quote: "For each claim, the runner asks the server for one handoff token, and gives it to the launcher."
- Quote: "lives 10 minutes (`runner.HandoffTokenTTL`),"
- Quote: "The session lives as long as the claim. Each later request of the launcher carries a proof of the session key. A session of a claim that a takeover replaced authenticates nothing (`claim-lost`)."
- Effect: The proxy session copies decisions 5, 9 and 11 for a second holder of the claim. The runner key still holds only `runner:claim`. No new identity action exists. The proxy session never holds a job token (ADR 133 decision 13, ADR 386 decision 3).
- Conflict: none.

**ADR 386: The server makes the job token when the launcher asks for step credentials** (owner, 2026-11-17).

- Decisions that apply: 3 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "The runner never holds a credential of a step. ADR 133 decision 13 stays. The claim answer, the handoff token and the runner state hold no job token."
- Effect: The proxy handoff answer, the proxy assignment and the proxy session hold no job token. The upstream credential is a credential of the pool, not of a step, and only a proxy session gets it.
- Conflict: none.

**ADR 132: Runner protocol versions and skew**.

- Decisions that apply: 6 and 7 (current).
- Changed by: -. Chain to the end: none.
- Quote: "A field that an old launcher can ignore with no change of what the phase does does not raise the assignment version. A field that changes what a step does, which credentials it gets or what it changes raises it."
- Effect: This change adds no field to `Assignment`. The proxy assignment is a separate message that only a proxy session reads. The version stays 1.
- Conflict: none.

**ADR 130: Runner pools, runners and coordinators** (Current rule: ADR 130 with ADR 164, ADR 300 and ADR 343).

- Decisions that apply: the pool is the object of `runner:claim` and of `namespace:manage-runners`.
- Effect: The pool settings are one sub-resource of a pool and use the pool routes' actions.
- Conflict: none known. The text of ADR 130 and of ADR 343 was not read in full for this draft. The first test task (0.1) checks them again.

**ADR 250: Defense in depth for security code**.

- Quote: "Never use only one layer." (`CLAUDE.md`, "Security code: defense in depth")
- Effect: Each security task has one hostile test for each layer (design.md, "Layers").

## Open questions

none

Design calls that follow an existing pattern and change no word of an owner decision (the PM sees them in the report):

- The credential is sealed under the scope of the namespace and the stable ID of the pool, with `secret.Service.Encrypt`. ADR 330 says "the data key of the namespace"; the scope uses that key through the service.
- The credential has the shape of a user name and a password for `Proxy-Authorization: Basic`. A bearer form is a later change.
- The proxy handoff token starts with `dpp_`. The launcher token keeps `dph_`.
- The optional If-Match version on a save follows `job-proxy-rule-sets` (decision 3 there).
