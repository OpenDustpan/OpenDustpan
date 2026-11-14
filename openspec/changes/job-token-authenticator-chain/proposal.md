---
version: 2
---

Short form: the job token authenticator chain and its authorization rule.
It replaces the parked work of PR #2920, which stacked on the closed #2918.

## Problem

**Persona: runner operator.** A runner job presents its job token on the
state routes of its unit. On main, `jobtoken.Service.Authenticate`
(`pkg/jobtoken/service.go:145`) verifies the token, but the server's
authenticator chain does not ask it, and no authorization rule limits what a
job token may do. So a job token is either refused everywhere or, once wired,
gets the grants of an ordinary principal.

## Evidence

- `pkg/jobtoken/service.go:145` holds `func (s *Service) Authenticate(`, and
  `rg -n "jobtoken\." pkg/dustpan --glob '!*_test.go'` finds no line: the
  server does not wire it.
- `pkg/identity/identity.go:1385` holds `type Chain []Authenticator`. The
  chain of the server is built in `newAuthPorts` (`pkg/dustpan/dustpan.go:878`)
  and `gatedAuthenticator` (`pkg/dustpan/area_signin.go:64`).
- `pkg/identity/identity.go:77` holds `JobID string`, the job of the token
  that authenticated a request.
- `pkg/tempadmin/authenticator.go:81` holds `type Decorator struct`.

## Fix boundary

- Wire `jobtoken.Service` into the authenticator chain of `newAuthPorts`
  behind its own failure-budget gate in `gatedAuthenticator`, so that a guess
  at one kind of token does not spend the budget of another kind.
- Add an authorization rule for a job token, in the NEW file
  `pkg/identity/jobtoken_rule.go`, outside the authorizer of an embedder, so
  that no embedder can widen a job token:
  - A principal that carries a job ID and is not a consistent job token is
    denied every action.
  - A job token acts on the unit of its job only, by the stable unit ID of
    the job, read from the job store at each request. The one exception is
    the output read of ADR 213 decision 1, which `outputsharesvc` decides with
    its two checks.
  - The rule clears the administrator and temporary administrator marks of a
    job token before it asks the next authorizer (defense in depth).
  - `ListObjects` gives a job token its unit only.
- `tempadmin.Decorator` passes a job token through and reads no record for it
  (defense in depth).
- A runner push for the phases of a job accepts only the job token of that
  job. The error names no job ID.
- Out of scope: the action `job:write-cost` (no spec-ready change needs it),
  the field `Principal.JobUnit` (it does not exist; the rule reads the unit of
  the job by `Principal.JobID`), and the plan-only refusal, which
  `job-token-rules-state-write` decides at the state.write gate.

## Check

The hostile tests of `tasks.md` pass, one for each layer. A job token of
another unit, of another namespace, with an administrator mark, or with a
job ID that does not match is refused, and each refusal writes an audit entry.

## ADR check

**ADR 213: Job tokens and outputs ...** (decision 1).
- Quote: "The job gets the intersection of the two checks, never more. It reads
  no other part of the other unit. This replaces "limited to the unit of the
  job" in ADR 100 decision 20 for output reads only. Each other part of that
  decision stays."
- Changed by: ADR 241, ADR 244, ADR 265, ADR 267 (extends decision 1: the
  principal of check 2 for a trigger start). Current rule: this ADR with
  those ADRs.
- Effect: the unit limit of a job token stays, except the output read of
  decision 1.

**ADR 364: Unexported fields and golangci-lint before added checks (owner)**
(owner-tagged: all decisions; decision 2).
- Changed by: none. Current rule: this ADR.
- Quote: "No code outside its package makes or changes it field by field."
- Effect: the change adds no field to `identity.Principal` and writes no
  field-by-field literal of it outside `pkg/identity`. Code that needs a job
  token principal gets it from a constructor in `pkg/identity`.

**ADR 362: Owner decisions of 8 November 2026: stable IDs, ...** (owner-tagged).
- Quote: "stable IDs" in the title; CLAUDE.md: "Use the stable ID for each stored reference to an entity that a user can rename, or remove and make again."
- Effect: the rule compares the stable unit ID of the job, never a unit name.

## Open questions

none

Questions considered and answered:

- Does a job token read the outputs of another unit? Yes, only on the path of
  ADR 213 decision 1, which `outputsharesvc` checks.
- Is `job:write-cost` part of this change? No. No spec-ready change names it.
- Does the change add `Principal.JobUnit`? No. ADR 364 decision 2 and the
  closure of #2918 rule it out; the job store gives the unit of the job.
