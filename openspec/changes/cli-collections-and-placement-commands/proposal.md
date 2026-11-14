---
version: 1
---

Short form: CLI commands for collections and placement rules. It covers the
parked PR #2623, which must be rebuilt on main.

## Problem

**Persona: platform engineer.** The engineer follows the quickstart and must
make a collection and a placement rule before the first `pulumi up` or
`tofu apply`. Today the quickstart does this with curl, because the
`opendustpan` CLI has `namespace create` but no command for collections or
placement rules. A typo in a curl body gives a raw JSON error, and the 422
answer `placement.no-match` names no command that fixes it.

## Evidence

- `internal/cli/cli.go:39` holds `commandNamespace = "namespace"`, and
  `internal/cli/namespace_create.go` is the only namespace-level command.
  `ls internal/cli` lists no collection or placement file.
- The routes exist: `internal/api/apidesc/openapi.json` lists
  `/api/dustpan/v1/namespaces/{namespace}/collections` and
  `/api/dustpan/v1/namespaces/{namespace}/placement-rules` (and
  `/{rule}`).
- `internal/errcode/catalog_placement.go:16` holds
  `PlacementNoMatch = "placement.no-match"`.
- `cmd/opendustpan/admintoken.go` reads `DUSTPAN_ADMIN_TOKEN_FILE`, which
  `namespace create` uses.

## Fix boundary

- The NEW commands `opendustpan collection create <namespace>/<name>` and
  `opendustpan collection list <namespace>`.
- The NEW commands `opendustpan placement add --tool <tool> --project <project> --stack <stack>
  --collection <collection> [--position <n>] <namespace>`,
  `opendustpan placement list <namespace>` and
  `opendustpan placement delete <namespace> <rule-id>`. The flags map one to
  one to the fields of a rule; the collection is sent by its stable ID, which
  the command reads from the collection list when the flag gives a name.
- Each command is a client of an existing route. No route, request or answer
  changes.
- The commands read the admin token from `DUSTPAN_ADMIN_TOKEN` or
  `DUSTPAN_ADMIN_TOKEN_FILE`, as `namespace create` does.
- The text of the 422 `placement.no-match` names `opendustpan placement add`.
  The code stays.
- `docs/reference/configuration.md` lists the commands, and
  `docs/guides/quickstart.md` makes its collection and rule with them.

## Check

The quickstart runs without curl, and an e2e step runs its commands.

## ADR check

**ADR 17: every feature has a REST API, and every client uses only that API.**
- Effect: the commands are clients of the existing routes; no CLI-only
  behavior.

**ADR 362: stable IDs** (owner-tagged).
- Effect: the rule names its collection by stable ID. A name on the command
  line is a label that the command resolves once.

`rg -n -i "placement" docs/adr | head` finds the placement decisions of the
collections ADRs; this change adds no rule and changes no decision.

## Open questions

none

Questions considered and answered:

- Does the CLI add a route? No. Each command calls an existing route.
- How does a rule name its collection? By stable ID (ADR 362), resolved from
  the name on the command line.
