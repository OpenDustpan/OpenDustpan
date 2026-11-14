---
version: 1
---

Issue: #674.

## Problem

As a Developer who runs the Pulumi CLI against the server, the person needs large real updates to pass a bound that reflects real provider states, because the current bound still uses the 32 MiB checkpoint bound and a synthetic 60-byte smallest state.

## Evidence

- MaxNewStateBytesPerUpdate equals the 32 MiB checkpoint bound until corpus measurements exist. Proof: Issue #674: `MaxNewStateBytesPerUpdate` equals the checkpoint bound until corpus measurements exist.
- The count bounds derive from the 32 MiB checkpoint bound divided by a 60-byte synthetic smallest state. Proof: Issue #674: The count bounds derive from the 32 MiB checkpoint bound divided by the smallest measured resource state (60 bytes). The smallest state is synthetic.
- The replay check enforces MaxNewStateBytesPerUpdate in internal/pulumijournal. Proof: internal/pulumijournal/replay.go:265:	if s.newStateBytes > int64(MaxNewStateBytesPerUpdate) {

## Fix boundary

- Measure real provider resource state sizes from a corpus recorded by the Pulumi CLI and publish the distribution.
- Set MaxNewStateBytesPerUpdate and the count bounds from the measured values with comments that cite the measurement.

Out of scope:

- Change of journal wire format or checkpoint protocol
- Per-tool or per-namespace tunable bounds

## Check

- Record anonymized real provider states to NEW internal/pulumijournal/testdata/real-states.jsonl with CLI version and provider list, and report the distribution in internal/pulumijournal/corpus_test.go. Check: TestCorpusStateSizeDistribution in internal/pulumijournal/corpus_test.go fails on main and passes after, reporting p50, p95, p99, p1 size and largest per-update total. No route: the test reads the NEW corpus file only.
- Set MaxNewStateBytesPerUpdate and the count bound in internal/pulumijournal/bounds.go by the fixed formula and cite the corpus file and date in each comment. Check: TestNewStateBoundEqualsFormula in internal/pulumijournal/bounds_test.go fails on main and passes after. No route: the test reads the corpus file and the constant only.
- Cover exactly equal acceptance, one byte under acceptance, one byte over rejection, and verifier and replayer agreement. Check: TestVerifierAndReplayerAgreeOnNewStatePatches in internal/pulumijournal/agreement_test.go passes with exactly equal, one byte under, and one byte over cases. No route: the test replays journal bytes in memory only.
- Exercise the existing replay check through ANY /api/stacks/{}/{}/{}/update/{}/checkpoint with a personal access token. Check: Servertest against ANY /api/stacks/{}/{}/{}/update/{}/checkpoint with a personal access token still enforces the bound.
- Write the journal bounds docs page at NEW docs/guides/journal-bounds.md in the same pull request. Check: NEW docs/guides/journal-bounds.md lists the corpus source, the distribution and the tuned values.
- Run the Developer path from the docs page alone and run full checks. Confirmed by: CI (make check). Check: `make check` with zero findings.

## What main already does

Each fact names its file, line and function on main. The change specifies only the difference.

- Main defines MaxNewStateBytesPerUpdate as a 32 MiB tunable global with a comment that it stays tunable in tests. (`internal/pulumijournal/bounds.go:37`)
- Main enforces the new-state byte limit during replay and reports the limit. (`internal/pulumijournal/replay.go:265`)
- Main counts entry state bytes for the bound. (`internal/pulumijournal/replay.go:278 (func EntryStateBytes)`)
- Main has a corpus ordering test for journal sequences. (`internal/pulumijournal/corpus_test.go:124 (func TestCorpusSequenceOrder)`)

## ADR check

Each quote is copied from the file in `docs/adr/`. Code adds the owner tags, the "Changed by" chain and the "Current rule" from `docs/adr/INDEX.md`. A decision is owner-tagged when its text carries "(owner)" or "(owner, ADR N)", or when the ADR records decisions of the project owner.

**ADR 43: Pulumi journaling support** (not owner-tagged).

- Decisions that apply: 6 (changed).
- Changed by: ADR 151 changes decisions 6 and 10; ADR 220 changes decision 6. Chain to the end: ADR 151, ADR 220, ADR 154, ADR 210, ADR 217, ADR 273, ADR 286, ADR 363, ADR 238, ADR 247, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 151, 220.
- Quote: "The corpus test passes, and the e2e run of that CLI with journaling on and off gives the same normalized export."
- Effect: Tuning keeps the corpus comparison rule unchanged and changes no journal wire format. Tuning changes only the constants that the replay check enforces.
- Conflict: none.

**ADR 151: Redaction, and an item key for each history item** (not owner-tagged).

- Decisions that apply: 12 (current).
- Changed by: ADR 154 changes decisions 3, 8, 15, 28 and 29; ADR 210 changes; ADR 217 changes decision 2; ADR 220 changes decision 2. Chain to the end: ADR 154, ADR 210, ADR 217, ADR 220, ADR 238, ADR 247, ADR 286, ADR 273, ADR 363, ADR 315, ADR 356.
- Current rule: this ADR, with ADR 154, 210, 217, 220.
- Quote: "The latest state item. The latest state item of a unit is the set that ADR 150 decision 2 names, together with the replay base of each update that has not ended or whose journal the server did not replay yet."
- Effect: Lowering the bound does not change the latest state item set or the replay base. Stored items stay readable under the bound at their write.
- Conflict: none.

## Open questions

none

Each question below has a provisional answer by the PM decision rule (a benchmark, no persona upset or an escape hatch, not wrong after a step back). The owner reviews them on 14 Nov. No provisional answer changes the words of an owner decision.

1. Which providers and Pulumi CLI version does the corpus record, and does the corpus live as a committed offline file?
   - Options: Commit an offline corpus at internal/pulumijournal/testdata/real-states.jsonl with CLI version, provider list and date, read by tests only; Fetch live provider states in CI at test time.
   - Provisional decision (owner review 14 Nov): Commit an offline corpus at internal/pulumijournal/testdata/real-states.jsonl with CLI version, provider list and date, read by tests only
   - Reason: A committed offline corpus keeps tests deterministic and names the exact CLI version and providers that sized the bound. Benchmark: AWS: publish the measured corpus and the derivation rule with the change, as AWS publishes service limit derivations from measured load.
   - Persona impact: Developer path is unchanged either way. The committed offline corpus upsets no persona because tests stay offline and deterministic. Escape hatch: If the corpus cannot cover a provider, keep the 32 MiB cap for the byte bound so large updates still pass while the corpus grows.

Questions considered and answered:

- Does tuning the bound change the journal protocol or the corpus rule? No. ADR 43 decision 6 keeps the corpus test and the journaling on and off comparison. The change alters only constant values in internal/pulumijournal/bounds.go.
- What part of the bound already exists on main? Main defines MaxNewStateBytesPerUpdate as 32 MiB, enforces it in replay.go, and counts bytes with EntryStateBytes. The change keeps that enforcement point and re-derives the constants from measured states.
- What sets the tuned value of the bound? The pack proves only that the byte bound equals the checkpoint bound until corpus measurements exist. The tuned value follows the fixed formula from the new corpus, so no value is fixed up front.

Drafted by the spec factory of the spec compiler from the roadmap, the issues and the ADRs. Every quote, path and ADR citation was checked by code.
