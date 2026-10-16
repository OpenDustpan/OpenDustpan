# Journal limits

A journaled update that passes one of these limits fails. Each size limit
answers 413. The build time limit answers 503.

| Limit | Value | Answer |
| --- | --- | --- |
| Batch body | 8 MiB for one batch. A batch with one entry may hold up to the checkpoint limit of 32 MiB. | 413 |
| Journal entries in one update | 600000 | 413 |
| Added links in one update | 200000 | 413 |
| New states in one update | 32 MiB | 413 |
| New states plus added links in one build | 64 MiB | 413 |
| Build time at complete | 5 minutes | 503 |

The values come from a benchmark run of 10,000 resources, so they are
not final until corpus measurements exist.
