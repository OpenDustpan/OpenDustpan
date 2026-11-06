# 127. Agent context comes from a reserved output and object metadata

Status: accepted · 2026-10-17 · the output schema and its parser superseded in part by ADR 160

Changed by:

- ADR 160 changes decisions 1, 2 and 5: the output schema, the parser and the
  metadata read.
- ADR 160 extends decision 6: describe_unit adds the resolved values with
  provenance.

## Context

The project owner decided that the context for the agent comes from the
reserved `opendustpan` output of each unit, which has a schema version, and
also accepts the `readme` output of the Pulumi CLI. Context also comes from
the docs, links, owners and on-call that each object can carry.

A team writes an output in its code, so the review of the code also reviews
the output. A person can change object metadata through the API. Either
source holds text that someone other than the person of a run wrote.

## Decision

1. **The reserved output.** A unit of any tool can have an output with the
   name `opendustpan`. Its value is a JSON object, or a string that holds
   the JSON of one, with these fields:

   | Field | Type | Meaning |
   | --- | --- | --- |
   | `schemaVersion` | integer, required | the version of this schema, 1 today |
   | `summary` | string | what the unit is for |
   | `runbook` | Markdown | how to operate the unit, what is safe to change |
   | `owners` | list of strings, at most 20 | the teams or persons who own the unit |
   | `onCall` | string | where to page the people on call |
   | `links` | list of `{title, url}`, at most 50 | links with an `http` or `https` URL |

   The encoded value is at most 64 KiB. The function
   `agent.ParseContextOutput` holds these rules, and a table-driven test
   covers them.
2. **Strict reads.** The parser refuses an unknown field, so a typing error
   shows at the first read. A value with a `schemaVersion` above the
   newest that the server reads fails with `ErrContextVersion`. A new field
   that an older server can ignore needs a new version. `describe_unit`
   shows a parse error as a warning with the reason, and answers the other
   context. A unit with no output has no warning.
3. **The `readme` output.** `describe_unit` also answers the output
   `readme`, which the Pulumi CLI shows as the readme of a stack, as
   Markdown text of at most 64 KiB. It accepts it from a unit of any tool.
4. **The server reads outputs through the masked state read.** The server
   reads both outputs from the latest state version with the masked state
   read (ADR 41). An output that the tool marks as secret answers masked,
   and the agent ignores it.
5. **Object metadata.** `describe_unit` also answers the docs, links,
   owners and on-call that the object metadata feature stores for the
   unit, its collection and its namespace. That feature owns the API and
   the storage. The agent reads it through a read port of its own and
   defines no storage.
6. **Each source keeps its label.** `describe_unit` answers each source in
   its own field: the output of the code, the readme, and the metadata of
   the unit, the collection and the namespace. It does not merge them. So
   the model and the person see where each fact comes from, and a
   difference between two sources stays visible.
7. **Context is data, never instructions.** The system prompt tells the
   model that context text comes from other people (ADR 120 decision 23).
   ADR 124 decision 13 lists each path of such text and its defense.
   An instruction in a runbook cannot give the agent an action that the
   person does not hold, and cannot open the secret gate or a direct
   change.
8. **The schema is published.** The reference docs get the JSON Schema of
   each version of the output, and an example for the Pulumi CLI and for
   OpenTofu.

## Consequences

- A team adds context with one output in its code, and gets the review of
  its code for it. No new API is necessary for that path.
- The object metadata feature must exist before `describe_unit` can answer
  metadata. Until then, `describe_unit` answers the outputs only.
- A large runbook costs tokens in each run that reads it. The 64 KiB bound
  and the byte limit of a tool result bound that cost.

## Not decided here

- The object metadata feature itself: its fields, its API and its actions.
- Whether the output can point to docs in the source repository of the
  unit, and whether the agent reads them.
