# Architecture review, round 14

Range: `867814fa..ce2265d3` (the 64 commits on `main` that end at `ce2265d3`).
The given base `e942f91b8` is not in the repository history, so the range is
`ce2265d3~64..ce2265d3`.

## Checks run

- `go build ./...`: pass.
- `go vet` on the 82 changed Go packages: pass. (`bench/graphindex/extract` is
  a separate module with its own `go.mod`. It builds with `go build` in its own
  directory. `make check` does not cover it.)
- `golangci-lint run` on the 82 changed packages: 0 issues.
- `scripts/adr-index.py --check`: 308 ADRs, the links and the index are correct.
- Each test package added in the range has a `leak_test.go`.
- No added Go line has an ADR or RFC number.
- The full test suites were not run (out of scope).

## Findings, ranked by severity

| Severity | Issue | Commit | Summary |
| --- | --- | --- | --- |
| medium-high | #2923 | ea5d08d9 | The 403 text of `DELETE /api/server/tokens/{id}` shows if a token ID exists and if it is a service token. |
| medium | #2924 | 97f48b65 | A data-family role on the installation (namespace access management in every namespace) does not make a group protected. Needs owner intent. |
| medium | #2925 | 97f48b65 | A create or rename of a group answers 500 and writes a failure audit entry after the change is stored. |
| medium | #2926 | 58a8a346 | The delivery history keyset cursor uses `next_at_ms`, which each attempt rewrites, so pages skip deliveries in retry. |
| medium-low | #2927 | bf64ad76 | A publish refused for an integrity failure or for no signing key writes no audit entry. |
| low | #2928 | 97f48b65 | The protected flag does not name its cause. The sensitive-action rule is in three places. `IsSensitive` has no caller. The guide implies enforcement that does not exist. |
| low | #2929 | df64fd67 | The CAS ticket store has no production caller, and nothing prunes `used_tickets`. |
| low | #2930 | 65e6990c | An owner exception to the two-week skill-age rule is not in an ADR. CLAUDE.md still states the rule. |
| low | #2931 | cbb4827d | `process_group_other.go` builds only on platforms that the project does not support. `endGroupAfterExit` is copied per OS. |
| low | #2932 | 6aa04795 | `pkg/forgelink` has no `leak_test.go` (a gap that was there before). |

An issue already covers one more finding: #2901 (a1ef4464, a failed read of
the temporary administrators is refused as "not a target", with no cause).
No new issue was filed for it.

## Checked and found clean

- depguard: no domain package added an import of `net/http`, `database/sql`,
  a driver, `pkg/store` or `internal/api`. The depguard rules cover
  `internal/forgelinksvc` (`internal/*svc`), `pkg/forgelink`, `pkg/launcher`
  and `internal/stepup`.
- Ports and suites: both SQL adapters run the new port methods through their
  conformance suites: `sessiontest.RunTicketStore`, notify `CountBefore` and
  the history cursor, unit settings `PutResult`, registry address paging,
  job plan parts and a missing namespace.
- Fences: unit settings `Put` reads the defaults inside the fenced
  transaction. The drift upsert stays fenced. The force-unlock paths are
  fenced (287601df). The ticket store belongs to the installation, so it
  carries no fence.
- SQL: the new queries are in name order in both dialects. `internal/db` is
  generated. The baselines are edited in place, with no numbered migration.
  `used_tickets` is in the backup coverage.
- No magic: the backup unverified-key limit and the PostgreSQL bounds show in
  the API with their source. A job phase whose namespace is gone ends with a
  logged reason. A failed blob purge backlog read is counted.
- Defense in depth: the redundant checks of the range carry the comment
  (ticket use, registry address keys, job body enums, launcher refusals,
  profile claim names, step-up).
- `scripts/trufflehog-gate.py` fails closed on a command error, on output that
  does not parse, on a verified result and on an unknown result after three
  runs.
- `scripts/check-nolint.sh` fails on a missing base ref and on a failing diff.
- ADR 359 has change links in ADR 299 and in the index.
