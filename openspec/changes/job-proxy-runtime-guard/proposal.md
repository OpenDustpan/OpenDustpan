---
version: 2
---

Roadmap: `docs/roadmap.md:393` (Run the job proxy as the only exit.).

## Why

**Persona: runner operator.** Runner operator hosts pools that run untrusted job code. Today a job can open direct egress around policy, and a proxy failure can leave an open path. The operator needs one job proxy as the only exit, with bound dial and fail-closed behavior, so a refused request stops with a clear log line.

The user path this change must make work:

1. **Docs page.** Operator sets allowed private ranges and upstream proxy of runner pool from docs page, then starts runner.
2. **E2e.** Developer runs job on unit with proxy rules; launcher and tool reach server through tunnel; allowed hosts pass.
3. **Console.** Developer opens job log for refused request and reads blocked by the job proxy with host and path.

Errors the person may hit, and the one command that helps:

| Error | What the answer says | The command that helps |
| --- | --- | --- |
| Request refused because no proxy rule allows the host, method, or path. | Request gets 403. Job log shows blocked by the job proxy with host and path. Run a new job after the author widens the proxy rule set. | `opendustpan status` |
| Tunnel host resolves to an address that the outbound guard denies. | Connection gets refusal. Job log shows blocked by the job proxy with host and path. Run a new job after the pool range is fixed. | `opendustpan status` |
| Job proxy fails to start, so the claim never starts a step. | Claim ends failed before any step starts. No step output exists. Fix the pool or proxy rule sets and start a new job. | `opendustpan doctor` |

Evidence for the claims above:

- ADR 327 keeps one proxy next to each job and the blocked log line with host and path. Proof: docs/adr/0327-the-job-proxy.md decisions 2 and 10 as quoted in adr_check.
- ADR 330 keeps proxy as mode proxy of dustpan-launcher, one exit with tunnel rule, and dial with pkg/egress and pool ranges. Proof: docs/adr/0330-the-job-proxy-core-decisions.md decisions 1, 3, and 8 as quoted in adr_check.
- Milestone step needs the design items of RFC 16 P2, P4, P5, P10 and the blocked line in the job log. Proof: Context pack roadmap step text for m6-step-07-part-2.
- The outbound guard tunnel path already has a test in the proxy package. Proof: pkg/launcher/proxy/guard_test.go:31 (TestOutboundGuardTunnel)

## What Changes

- One job proxy process next to each job of a unit with proxy rules as the only sandbox exit
- Coordinator start per isolation kind with proxy-only route, drop of other packets, and fail-closed start and loss
- Tunnel rule for server host with launcher TLS check kept
- Bound dial that resolves each rule host, checks each address with pkg/egress, and connects only to a checked address
- Pool private ranges and pool upstream proxy applied in dial path
- Blocked line in job log with host and path and no secret

Out of scope:

- Credential injection from profile exports proxy and response scan for plain credential values
- Per-request proxy records, record retention, and phase audit entry
- Proxy rule set create, edit, render, and read routes
- Plan phase write limits tied to apply-only proxy rules
- Curated upstreams, artifact registry, blueprints, ephemeral units, environment promotion, upgrade pull requests, console catalog

Depends on:

- job-proxy-rule-sets
- `job-proxy-rule-sets` (the part before it in the split)

## Usability

Where the path still costs the person time or surprises them:

- Runner operator must read pool ranges and upstream setting from API; missing value fails closed with no hint of which range to add.
- Blocked line names host and path but not the proxy rule id, so author of proxy rules guesses which line to widen.
- Proxy loss ends phase failed with no resume, so a short proxy restart costs a full rerun.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Outbound guard tunnel path has a test at guard_test.go. (`pkg/launcher/proxy/guard_test.go:31 (TestOutboundGuardTunnel)`)
- Proxy-only export check for job proxy consumer has a test. (`pkg/profile/exports_internal_test.go:13 (TestProxyOnlyForJobProxy)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 327: The job proxy** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 8 (current), 10 (current), 18 (current), 20 (current).
- Changed by: ADR 330 extends. Chain to the end: ADR 330, ADR 353.
- Current rule: this ADR, with ADR 330.
- Quote: "Our own proxy (owner, 26 October 2026: Recommendation item 1). The job proxy is our own code in Go, with `net/http` and `crypto/tls`, and no new dependency."
- Quote: "Next to each job (owner, 26 October 2026: Recommendation item 1). The job proxy runs next to each job of a unit with proxy rules."
- Quote: "The outbound guard and the upstream proxy (owner, 26 October 2026: Recommendation item 7)."
- Quote: "Fixed reasons (owner, 26 October 2026: Recommendation item 9). The job proxy writes each failure as a fixed reason in the audit record. The job log shows "blocked by the job proxy", with the host and the path."
- Effect: Change keeps Go net/http and crypto/tls with no new dependency, one proxy next to each job, only exit with tunnel, outbound guard on resolved address, fixed reasons and log line. It does not add credentials, records, or audit entries.
- Conflict: none.

**ADR 330: The job proxy: core decisions P1 to P12** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (extended), 3 (current), 7 (extended), 8 (current).
- Changed by: ADR 353 extends decisions 2, 4 and 7. Chain to the end: ADR 353.
- Current rule: this ADR, with ADR 353.
- Quote: "One binary, a separate process. The job proxy is our own code in Go, with `net/http` and `crypto/tls`, and no new dependency."
- Quote: "One exit. The job proxy is the only exit of the sandbox. The launcher and the tool reach the OpenDustpan server through it too, with a built-in tunnel rule for the host of the server only."
- Quote: "The outbound guard and the upstream proxy. The job proxy dials with the rules of `pkg/egress`."
- Quote: "For `tunnel`, it resolves the host, checks each address with the outbound guard, and connects to the address that it checked."
- Effect: Change runs proxy mode of dustpan-launcher as own process and user outside step sandbox, starts one proxy per claim by isolation kind, enforces one exit with tunnel rule to server host, bound dial with egress rules, allowed private ranges, and pool upstream proxy.
- Conflict: none.

**ADR 353: Gates at every step of a plan that runs untrusted code: every control is an opt-in setting** (owner-tagged (all decisions)).

- Decisions that apply: 1 (current), 2 (current), 6 (current).
- Changed by: -. Chain to the end: none.
- Current rule: this ADR.
- Quote: "Every control is a setting that a team turns on (owner, 2026-11-07). The owner said: "they're opt in settings not forced"."
- Quote: "The default keeps today's behavior (owner, 2026-11-07). Until a team turns a setting on, a unit behaves as it does on `main`."
- Quote: "Security rules stay (owner, 2026-11-07). A control that a team turns on is never weaker than the rules of ADR 250 in `CLAUDE.md`."
- Effect: Change does not force proxy use on any unit. Proxy runs only where the team turned proxy rules on. Default behavior without proxy rules stays as on main.
- Conflict: none.

## Open questions

none

Questions considered and answered:

- Does the runtime need a new proxy product or dependency? ADR 327 decision 1 and ADR 330 decision 1 answer it: own Go code with net/http and crypto/tls, mode proxy of dustpan-launcher, no new dependency.
- Where does the job proxy run for each isolation kind? ADR 330 decision 2 answers it: firecracker and gvisor start one proxy on host per claim with one route to proxy address and drop of other packets; kubernetes and process use one proxy per claim with the same only-exit rule.
- How do bound dial, private ranges, and upstream proxy combine? ADR 327 decision 8 and ADR 330 decision 8 answer it: dial with pkg/egress rules, never always-denied addresses, private range only when pool allows it, pool upstream proxy only.
- Does this change add per-request records or phase audit entries? ADR 327 decision 10 answers it: this change writes only the job log line with host and path and a fixed reason. Full records and phase audit stay in the later part.
- Is the proxy on by default for every job? ADR 353 decisions 1 and 2 answer it: proxy runs only where the team set proxy rules.
- Where is the upstream proxy credential stored? ADR 330 decision 8 answers it: the pool seals it. Pool storage is built in job-proxy-rule-sets, which this change depends on.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
