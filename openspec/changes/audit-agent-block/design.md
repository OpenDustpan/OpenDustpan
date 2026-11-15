## Context

The audit entry is hashed into a chain per scope. The agent data must enter the hash. An entry with no agent data hashes as on main, because it writes no block (ADR 380 decision 2); no compatibility path exists for other shapes, because no tag shipped.

## Decisions

1. **One block encoding.** ComputeHash appends the length-prefixed text agent/1 then the seven fields in the fixed order agentRun, agentSession, tool, argsDigest, breakGlass as true or false, secretStage, delegationID, each length-prefixed. No field is optional inside the block. It appends nothing when Entry.Origin is nil. A non-nil Origin always appends the block.
2. **Job entries use the block.** The conversion from Actor.Agent to job.AgentRef sets agentRun to the run ID for the channel agent, agentSession to the MCP session ID or the run ID, and breakGlass to true for a break-glass credential. tool, argsDigest and secretStage stay empty.
3. **The delegation is the seventh field (ADR 380, owner).** The block holds `delegationID`, the stable ID of the ADR 354 delegation (ADR 362), last in the fixed order, and empty when the step had no delegation. The block text stays agent/1: no block version and no path for old entries, because no tag shipped. The reason of a delegated step stays the field `Reason` of `audit.Entry`.

## Structure

The NEW type `audit.Origin` in pkg/audit holds the seven fields as unexported fields. Only the NEW constructor `audit.NewOrigin` makes one: it checks the length and character limits, and it refuses a partial origin (an empty agentRun or agentSession) with the NEW sentinel `audit.ErrInvalidOrigin`. tool, argsDigest and secretStage may be empty, and breakGlass may be false (decision 2). Entry holds it by pointer in the NEW field `Entry.Origin`; nil means an ordinary entry with no agent block. Only ComputeHash encodes it, so no caller can hash a field outside the block.

Why a pointer to one value type: seven more fields in Entry put it over the hugeParam limit of golangci-lint (gocritic) in the four structs that embed Entry. One pointer keeps Entry below the limit, so the embedding structs and the ports do not change, and no pointer is threaded through each port.

Nil is safe in every reader: each reader of Entry.Origin checks for nil first, and `make nilaway` checks this. The seven columns are not nullable and have empty defaults (`''` and false), as the other columns of audit_entries. The store maps a nil Origin to empty values and false, and six empty values and false back to nil. A row with an empty agent_run or agent_session and any other of the seven set is refused on read with `audit.ErrInvalidOrigin` (fail closed). `fromAuditRow` returns this error, and every read path returns it.

## Risks

- A wrong encoding order changes hashes of new entries only. The tests of two entries that differ in one field catch it.
- Dialect baselines can drift. The store conformance suite checks both dialects.

## Layers (defense in depth)

| Layer | Guard | Hostile test |
| --- | --- | --- |
| boundary check | NewOrigin accepts only the allowlisted channel values and refuses control characters, values over 256 bytes and a partial origin (empty agentRun or agentSession). | TestNewOriginRefusesHostileFields, TestNewOriginRefusesPartialOrigin |
| encode or contain | Each field is length-prefixed in the hash, so a joined value cannot mimic two fields. SQL uses placeholders. | TestComputeHashDiffersForEachAgentField |
| limit | A size limit of 256 bytes for each field. | TestNewOriginRefusesOversizedField |
| authorize at point of use | The read route keeps its existing audit read action. The block shows only to a reader of the entry. | TestAuditEntriesRouteKeepsItsAction |
| fail closed | A write with an invalid origin fails and stores nothing. A nil Origin writes empty columns and hashes with no block. A stored row with a partial set of the seven columns fails the read. The chain check fails on a changed field. | TestChainCheckFailsOnChangedAgentField, TestOriginNilIsNoBlock, TestStoreRefusesPartialOriginRow |

## Diagrams

After this change:

```mermaid
sequenceDiagram
 participant Client
 participant JobService as pkg/job Service
 participant Audit as pkg/audit
 participant Store
 Client->>JobService: start with agent actor
 JobService->>Audit: entry with agent block
 Audit->>Audit: ComputeHash with agent/1
 Audit->>Store: write entry and columns
```

Modules that the change touches, and their imports on main:

```mermaid
flowchart LR
P1[pkg/audit]
P2[pkg/job]
P2 --> P1
```

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| hash cost | 5 | percent increase of ComputeHash time | an entry with no agent data |
| storage growth | 6 | columns of at most 256 bytes each | each audit row |

## Threat model

Data flow and trust boundaries:

```mermaid
flowchart LR
 subgraph caller[Untrusted caller]
 A[Request]
 end
 subgraph server[Server]
 B[Job service] --> C[Audit entry and hash]
 end
 subgraph store[Store]
 D[(Audit table)]
 end
 A --> B
 C --> D
```

| Boundary | STRIDE | Threat | Layer | Guard | Hostile test |
| --- | --- | --- | --- | --- | --- |
| Server to store | T | A changed agent field in a stored row hides who acted. | boundary check | The block constructor allowlists the channel and the characters. | TestAgentBlockRefusesHostileFields |
| Server to store | T | A changed agent field in a stored row hides who acted. | encode or contain | Length-prefixed fields in the hash and SQL placeholders. | TestComputeHashDiffersForEachAgentField |
| Server to store | T | A changed agent field in a stored row hides who acted. | limit | 256 bytes for each field. | TestAgentBlockRefusesOversizedField |
| Server to store | T | A changed agent field in a stored row hides who acted. | authorize at point of use | The audit read route keeps its action. | TestAuditEntriesRouteKeepsItsAction |
| Server to store | T | A changed agent field in a stored row hides who acted. | fail closed | The chain check fails on a changed field and the write fails on an invalid block. | TestChainCheckFailsOnChangedAgentField |

## Contracts

**audit.Entry**

- Invariant: An entry with seven empty agent fields and breakGlass false has no block in its hash.
- Invariant: Every field of `audit.Origin` is in the hash of the agent block and in a column of each store, from the commit that adds it; no commit holds a field that the hash chain does not protect.
- Invariant: Each agent field has at most 256 bytes and no control character.
- `ComputeHash`: pre: The entry fields are set.. Post: The hash has no block when the seven fields are empty, else it has the agent/1 block in the fixed order..

## Change impact (computed from main)

Tests:

- `internal/removalalertsvc/fakes_test.go` names `audit.Entry`
- `internal/removalalertsvc/removalalertsvc_test.go` names `audit.Entry`
- `internal/cost/costtest/mem_test.go` names `audit.Entry`

ADRs:

- `docs/adr/0265-a-unit-shows-all-its-states-with-one-fixed-set-of-words.md` names `audit.Entry`
- `docs/adr/0154-history-gaps-found-before-the-implementation.md` names `audit.Entry`
- `docs/adr/0200-profiles-config-secrets-and-credentials-for-every-tool.md` names `audit.Entry`
