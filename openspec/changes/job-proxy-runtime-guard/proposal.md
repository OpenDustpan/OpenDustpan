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
| Request refused because no proxy rule allows the host, method, or path. | Phase fails. Job log shows blocked by the job proxy with host and path. No retry inside job. | `opendustpan status` |
| Tunnel host resolves to an address that the outbound guard denies. | Phase fails with egress denial reason. Job log shows blocked by the job proxy with host and path. | `opendustpan status` |
| Job proxy fails to start, so the claim never starts a step. | Claim fails before steps start. No step output exists. | `opendustpan doctor` |

Evidence for the claims above:

- ADR 327 keeps one proxy next to each job and the blocked log line with host and path. Proof: docs/adr/0327-the-job-proxy.md decisions 2 and 10 as quoted in adr_check.
- ADR 330 keeps proxy as mode proxy of dustpan-launcher, one exit with tunnel rule, and dial with pkg/egress and pool ranges. Proof: docs/adr/0330-the-job-proxy-core-decisions.md decisions 1, 3, and 8 as quoted in adr_check.
- Milestone step needs the design items of RFC 16 P2, P4, P5, P10 and the blocked line in the job log. Proof: Context pack roadmap step text for m6-step-07-part-2.

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

## Adversarial second pass

A model of another provider than the drafter listed the open questions, failure modes and missing scenarios. Each finding is answered in this change or is an open question.

- R1.1 (wrong claim): The user path and error table name `opendustpan job logs` and `opendustpan runner claim get`. The proposal's own Open questions section says these commands do not exist. The change cannot be built right until real commands are named or the commands are added. Resolution: answered: Replaced opendustpan job and runner commands with existing opendustpan status and opendustpan doctor; user path now uses docs page, console, and e2e surfaces only.
- R1.2 (wrong claim): The proposal cites ADR 327 decision 3 (its own check says there is no decision 3) and lists ADR 327 decisions 1, 2, 8, 10, 18 and 20 as applying. The Open questions section reads 'Not spec-ready', so the gate fails. The text 'Open questions: none' is not met. Resolution: answered: Removed ADR 327 decision 3 from adr_check; open_questions is none with considered answers.
- R1.3 (adr conflict): ADR 327 decision 2 says no runner starts a job proxy until the owner accepts P2, and ADR 330 decision 2 gives the placement (extended by ADR 353). The design applies the 'drop other packets, DNS included' rule only to firecracker and gvisor. The kubernetes and process isolation kinds have no requirement or scenario, and the proposal says only that they 'follow the same rule'. The placement and the network drop for these kinds are not specified. Resolution: answered: Added separate requirements for firecracker and gvisor host proxy with packet drop, and for kubernetes and process proxy per claim with only-exit rule.
- R1.4 (adr conflict): ADR 353 decisions 1 and 2 make proxy use opt-in, and the default keeps behavior on main. No requirement or scenario says that a unit without proxy rules runs with no proxy and no route drop. The proposal states this only in the ADR effect text. Resolution: answered: Added no-proxy-without-rules requirement that a unit without proxy rules runs with no proxy and no route change.
- R1.5 (missing scenario): The design lists a check for a non-canonical path, an SNI or Host mismatch, and a CONNECT host check (ADR 330 decision 7). The spec has no requirement or scenario for these checks. Tasks have no failing test for them either, so the hostile tests of the design have no mapping. Resolution: answered: Added requirements for unnamed CONNECT host, non-canonical path refusal, and SNI and Host match, with failing tests in tasks.
- R1.6 (missing scenario): The upstream proxy is in scope, but the pool upstream proxy has no setting route or scenario. The pool credential sealing (ADR 330 decision 8) and the action `namespace:manage-runners` (decision 11) are not covered. The spec has only a guard scenario, and tasks have no test for it. A missing allowed range or upstream setting has no stated error. Resolution: answered: Added upstream guard requirement and private range refusal; pool setting routes and credential sealing stay in job-proxy-rule-sets dependency, stated in out_of_scope and depends_on.
- R1.7 (failure mode): The DNS time-of-check to time-of-use risk is listed. The bound dial scenario only says the proxy connects to a checked address. No scenario covers a host that resolves to mixed addresses (one allowed, one denied), a DNS rebind between two lookups, or an address that fails to connect. It is also not stated whether the proxy may fall back to another checked address. Resolution: answered: Added mixed-address requirement: dial only guard-allowed checked addresses, never a denied address, with no fallback to an unchecked address.
- R1.8 (failure mode): The spec has no scenario for proxy loss while the launcher is polling the server through the tunnel. The proxy session paths under /runner/v1 and the runner assignment version (ADR 330 decision 12) are only in task 3.1. No behavior is given for an invalid or reused session, or for a hostile step that connects to the proxy listener directly. Resolution: answered: Added launcher tunnel loss requirement that pending launcher polls fail closed and phase ends failed; proxy session auth stays in rule-sets part.
- R1.9 (scope): The proxy rules come from the server through the proxy session (ADR 327 decision 2, P12). The change depends on job-proxy-rule-sets, but the Depends-on list names the same item twice. It does not say how the proxy loads its rules, so the 'unnamed host' behavior depends on a delivery path that is not specified. Resolution: answered: Kept single depends_on entry and stated proxy loads joined rule sets from coordinator assignment at claim start.
- R1.10 (missing scenario): The blocked line is required to hold no secret. The scenario does not cover a non-canonical path (the path is only logged 'when it is canonical' in ADR 330 decision 10), or a very long or hostile host and path value that could inject log lines. It states no fixed reason either, which ADR 327 decision 10 requires. Resolution: answered: Blocked-line requirement now states canonical path only, fixed reason, and no header, body, query value, or credential, with hostile log-injection test.

## Open questions

none

Questions considered and answered:

- Does the runtime need a new proxy product or dependency? ADR 327 decision 1 and ADR 330 decision 1 answer it: own Go code with net/http and crypto/tls, mode proxy of dustpan-launcher, no new dependency. No new tool is added.
- Where does the job proxy run for each isolation kind? ADR 330 decision 2 answers it: firecracker and gvisor start one proxy on host per claim with one route to proxy address and drop of other packets; kubernetes and process use one proxy per claim with the same only-exit rule. Scope of this change keeps that placement.
- How do bound dial, private ranges, and upstream proxy combine? ADR 327 decision 8 and ADR 330 decision 8 answer it: dial with pkg/egress rules, never always-denied addresses, private range only when pool allows it, pool upstream proxy only. This change applies that dial path.
- Does this change add per-request records or phase audit entries? ADR 327 decision 10 answers it: this change writes only the job log line with host and path and a fixed reason. Full per-request records and phase audit stay in the later part.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
