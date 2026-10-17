# Journal limits

A journaled update that breaks one of these bounds fails. Each size bound
answers 413. The build time bound answers 503. Each bound has the
measurement behind it.

| Bound | Number | Measurement | Answer |
| --- | --- | --- | --- |
| Batch body | 8 MiB for one batch. A batch with one entry may hold up to the checkpoint bound of 32 MiB, because the CLI cannot split one entry. | One benchmark run with 10,000 resources measured 4185585 bytes. | 413 |
| Journal entries in one update | 1118481 | 32 MiB holds 559240 smallest states of 60 bytes. A create or a destroy of each one needs 2 entries. One benchmark run measured the 60 bytes. | 413 |
| Added links in one update | 559240 | Each smallest state adds 1 link. A no-op update on a large base adds no links, so it passes. | 413 |
| New states in one update | 32 MiB | One benchmark run with 10,000 resources measured 2210000 new-state bytes. | 413 |
| New states plus added links in one build | 64 MiB | One benchmark run measured a peak heap of 25826176 bytes. This bound is not a bound on heap. | 413 |
| Build time at complete | 5 minutes | One benchmark run with 10,000 resources measured 210444745 ns/op. The project has not yet checked whether the CLI waits past 20 minutes. | 503 |
