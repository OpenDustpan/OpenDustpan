# 380. The delegation in the agent block of an audit entry

Status: accepted · 2026-11-15. Decided by the project owner

Changes:

- Changes ADR 126 decision 10: the agent block holds seven fields, with the delegation ID as the seventh. The rule that old entries hash as before goes.

## Context

ADR 126 decision 10 puts six fields of an agent step into the hash
encoding of an audit entry, as one agent block. ADR 354 (owner, 2026-11-07)
lets an agent act for a person under a delegation. A delegated step must
record the delegation that allowed it, and a change of that record must
break the audit chain like a change of any other field.

The OpenSpec change `every-step-audited` stopped on the question where the
delegation goes in the hashed entry. The options were a second block
version (`agent/2`), a field of the entry, or no hash. The project owner
answered on 15 November 2026: "approved", for a seventh field of the one
agent block, with no block version and no compatibility path.

ADR 126 decision 10 kept the bytes of old entries, so that "every audit
chain that exists still passes the chain check with no migration". No tag
of OpenDustpan shipped, so no installation holds an audit chain, and no
upgrade path exists to keep (ADR 72: only a shape that a tag shipped keeps
its upgrade path).

## Decision

1. **Seven fields in the one agent block (owner, 2026-11-15).** The agent
   block of ADR 126 decision 10 holds seven fields in this fixed order:
   `agentRun`, `agentSession`, `tool`, `argsDigest`, `breakGlass` as `true`
   or `false`, `secretStage`, and `delegationID`. `delegationID` is the
   stable ID of the delegation of ADR 354 under which the agent acted
   (ADR 362), and it is empty when the step had no delegation. The block
   starts with the length-prefixed text `agent/1`, then the seven fields,
   each length-prefixed, with an empty value when it is not set. Inside the
   block no field is optional.
2. **An entry with all seven fields empty writes no block.** An entry with
   all seven fields empty (and `breakGlass` false) writes no block. An entry
   with any of the seven fields set writes the block.
3. **No block version and no compatibility path.** The block keeps the
   text `agent/1`. No entry was hashed with six fields under a tag, so the
   chain check reads one block shape only, and no code reads a six-field
   block.
4. **The encoding stays injective.** An entry without the block has a fixed
   count of length-prefixed fields, and an entry with it has that count plus
   eight: the version text and the seven fields.

## Consequences

- The reason of a delegated step stays the existing `Reason` field of the
  entry. It is not a field of the block.
- The OpenSpec changes `audit-agent-block` and `every-step-audited` use the
  seven fields. The preflight of the field `Origin` of `audit.Entry` holds
  six fields. The seventh comes with `audit-agent-block`, inside the agent
  block type, so no literal of `audit.Entry` changes for it.
- A later field of the block is a new ADR with the owner's word.
