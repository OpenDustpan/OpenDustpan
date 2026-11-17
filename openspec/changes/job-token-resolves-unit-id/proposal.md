---
version: 1
---

Issue: #3393.

Short form: bug (SECURITY). Owner lane: M6.

## Why

**Persona: runner operator.** A step of a job reads and writes the state of its own unit with its job token. The tool sends the unit by name, as the real CLI does. The server answers 403 for the unit of the job, so no step can use its token on state. The change `add-job-session-paths-runner-token-issuer` depends on a token that works on state routes (its tasks 4.1 and 4.2).

## What Changes

- `identity.JobTokenRule` gets a NEW field `UnitIDs` of the NEW port `identity.UnitIDReader`. When the object of a request is a unit with no stable ID, the rule asks the port for the stable ID of that unit and then compares it with the stable ID of the unit of the job. The rule asks for no other principal.
- A unit that the port does not find is refused as every other unit that is not the unit of the job. A port that fails refuses with an error that is not a denial (fail closed).
- `pkg/dustpan` wires the same store that already meets `access.Units` as `UnitIDs`.
- No adapter changes: the OpenTofu HTTP backend, the TFE-compatible API, the Pulumi Cloud-compatible API and the console keep building a unit object from the name.

Considered and refused: each adapter resolves the stable ID before it authorizes (about every route that builds a unit object, and a new route that forgets the call would fail only for job tokens). One place in the rule makes that bug class impossible.

## Failure the user sees

| Case | Answer |
| --- | --- |
| A job token on the state routes of its own unit. | 200 as the starter principal. |
| A job token on the state routes of another unit. | 403 with the job token message. |
| A job token on a unit that was removed and made again with the same name. | 401 "job token ended" (the token is bound to the stable unit ID of the old unit). |

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- A job token is allowed only when the stable ID of the object equals the stable ID of the unit of the job. (`pkg/identity/jobtoken_rule.go:123 (JobTokenRule.Authorize)`)
- The rule refuses a unit of a job that has no stable ID. (`pkg/identity/jobtoken_rule.go:41 (NewJobUnit)`)
- The access authorizer already reads the stable ID of a unit that has none, once for each request, through `access.Units`. (`Authorizer.withUnitID` in the access package)
- The OpenTofu HTTP backend builds the unit object from the path with no stable ID and then authorizes. (`internal/api/tofuhttp/tofuhttp.go:270 (Handler.enforce)`)
- The rule wraps the whole chain of the server. (`pkg/dustpan/dustpan.go:966 (newAuthPorts)`)

## ADR check

**ADR 100 decision 20** (owner-tagged chain: ADR 386 changes its timing, not its reach). Quote: "It is an access token of the principal that started the job, limited to the unit of the job". Effect: the token reaches its own unit. Today it reaches no unit by name. Conflict: none; this change makes the words true.

**ADR 213**. Quote: "limited to the unit of the job". Effect: the same. Conflict: none.

**ADR 19 and ADR 362 decision 1**. Quote (362): "Every stored reference to an entity uses the stable ID of the entity when a user can rename the entity, or delete and recreate it." Quote (362, on routes by name): "keep a route by name for the tool-compatible APIs. The real CLIs send names, so those routes stay. Stored references inside the server use the ID." Effect: the route keeps the name, the rule compares the stable ID. A recreated unit has a new ID and the old token does not reach it. Conflict: none.

**ADR 386** (owner). Effect: none on the timing; the token is made at the launcher request. Conflict: none.

**ADR 250**. Effect: the rule is security code; it keeps a test for each layer. Conflict: none.

## Open questions

none. The change widens no reach: the rule still allows only the stable ID of the unit of the job, and it reads the ID that the access authorizer reads for every other principal.

Depends on: none.
