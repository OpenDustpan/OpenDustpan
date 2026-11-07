# 340. Measure background blob families by total job time

Status: accepted · 2026-11-07. Decided by the project owner on 7 November 2026. Accepts option 2 ("accept the background cost") of [RFC 116](../rfc/0116-background-blob-families-are-measured-by-total-job-time.md)

Changes:

- Changes ADR 281 decision 2: for a background blob family, the measure is the total time of one job. The time of one operation does not apply.

## Context

ADR 281 decision 2 accepts the new blob write path under a condition. For each blob family, the median p50 and the median p99 must be at most 5 % higher than the old path. The median throughput must be at most 5 % lower.

The benchmark of 2026-11-01 found two families that miss this measure. Both run in the background:

- The restore copy with an absent key misses at the profile `eu-metro` only (p50 +8.6 %, throughput -8.0 %).
- The audit checkpoint write misses at every profile.

A person or a CLI waits for neither family. The cost that counts for a background family is the run time of the whole job. The delay to other work counts too. RFC 116 asks if the per-operation measure fits these families.

On 7 November 2026 the owner chose option 2 of RFC 116. The text of this option is: accept the background cost. Background families are measured by total job time.

## Decision

Decided by the project owner on 7 November 2026.

1. **Background families are measured by total job time (owner, 2026-11-07).** Accept the background cost. A background blob family is measured by the total time of its job. This changes ADR 281 decision 2 for these families.
2. **Hot paths keep the per-operation measure (from the RFC 116 proposal).** A path where a person or a CLI waits keeps the per-operation measure. ADR 281 decision 2 stays for these paths.
   - The hot paths are the Pulumi checkpoint, the journal batch, the job log chunk and the backup target copy.
   - The unit import and the audit checkpoint check keep it too, because they meet it now.
3. **The record states the job (from the RFC 116 proposal).** For each background family, the benchmark record states these items:
   - the job and its size
   - the profile
   - how many jobs run at the same time
   - the limit
4. **A miss goes to the owner (from the RFC 116 proposal).** A family that misses its limit goes back to the owner with the numbers.

## Consequences

- The code does not change. The meaning of "meets" changes for the background families only.
- This ADR does not decide the job size or the limit of any family. The limit may be a ratio, an absolute cap, or both. The benchmark record states it.
- This ADR does not decide if the audit checkpoint write drops its second LIST (option 3 of RFC 116). That option removes a safety layer. It needs its own design and its own tests.
- The restore model needs one more pass when the pending fence check merges.
- `docs/reference/blob-write-benchmark-record.md` must state the items of decision 3. Then it can show if these two families meet the measure.
