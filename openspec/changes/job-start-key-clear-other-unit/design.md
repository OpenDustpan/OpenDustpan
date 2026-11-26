## Context

The retention change clears the key of an expired job in func sameKey. The lookup uses the namespace and the key. The key holds the subject (func scopedKey). The old job can be on another unit than the new start. The start authorizes the unit of the new job only.

## Decisions

1. **The clear needs no check on the old unit.** The key that the store keeps is a hash of the subject and the key string. A lookup finds only a job that the same principal started. The clear removes a key that this principal set. It writes no other field than the key, the version and the update time.
2. **The entry names both units.** The audit entry `job:key-expired` carries the old job, the unit of the old job, the unit of the new start and the actor of the new start.
3. **No scope with the unit.** The key stays scoped to the principal and the namespace.
4. **A test for each layer.** The conformance case runs the clear on two units. A hostile case sends the same key string as another principal and shows that the job of the first principal is not changed.

## Contract

No new type. The entry of the clear gets the NEW detail key `newUnit`. The existing `job:key-expired` entry already names the old job.

## Risks

- The clear moves the version of a job on a unit where the principal has no grant now. A holder of the old ETag gets 412. The effect is the same as for a clear on the same unit.

## Layers

| Layer | Check | Test |
| --- | --- | --- |
| Port adapter (job store) | The lookup finds only a key of the same subject. | Hostile case: another subject with the same string clears nothing. |
| Service | The scoped key holds the subject. | Existing func TestStartIdempotency, plus the case of a second principal. |
| Audit | Each clear writes one entry with both units. | Conformance case reads the entry. |

## Quality attributes

| Attribute | Target | Unit | Scope |
| --- | --- | --- | --- |
| entries of the clear for each clear | 1 | entry | one start |

## Failure modes

| Step | Failure | Retry | State after |
| --- | --- | --- | --- |
| Write the entry of the clear | The write fails. | The client sends the start again. | The transaction rolls back. The old job keeps its key. |
